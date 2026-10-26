package main

// File-backed storage. The backend interface is intentionally tiny so the
// persistence layer can be replaced (SQL, Redis, ...) without touching HTTP.

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// record is everything the server persists. It never holds plaintext context
// or a plaintext token: only the ciphertext envelope and the token hash.
type record struct {
	ID        string                 `json:"id"`
	Title     string                 `json:"title"`
	Format    string                 `json:"format"`
	Metadata  map[string]interface{} `json:"metadata"`
	Payload   string                 `json:"payload"`
	TokenHash string                 `json:"tokenHash"`
	CreatedAt int64                  `json:"createdAt"`
	ExpiresAt int64                  `json:"expiresAt"`
}

var (
	errNotFoundRecord = errors.New("record not found")
	idPattern         = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)
)

type Store interface {
	Save(rec *record) error
	Get(id string) (*record, error)
	Delete(id string) error
	PurgeExpired(now time.Time) (int, error)
	Close() error
}

// FileStore stores one <uuid>.json file per record.
type FileStore struct {
	root string
	mu   sync.Mutex // serializes tmp-name generation + rename within process
}

func NewFileStore(root string) (*FileStore, error) {
	if root == "" {
		return nil, errors.New("store directory is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, dirMode); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	// Best-effort tighten even if the directory pre-existed.
	_ = os.Chmod(abs, dirMode)
	return &FileStore{root: abs}, nil
}

func (s *FileStore) pathFor(id string) (string, error) {
	if !idPattern.MatchString(id) || strings.Count(id, "-") != 4 {
		return "", errors.New("invalid record id")
	}
	return filepath.Join(s.root, id+".json"), nil
}

// Save writes atomically: temp file in the same directory, fsync, then rename,
// with restrictive permissions throughout.
func (s *FileStore) Save(rec *record) (err error) {
	target, err := s.pathFor(rec.ID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tmp, err := os.CreateTemp(s.root, "."+rec.ID+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp record: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if chmodErr := tmp.Chmod(fileMode); chmodErr != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp record: %w", chmodErr)
	}
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write record: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("fsync record: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close record: %w", err)
	}
	if err = os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("rename record: %w", err)
	}
	_ = os.Chmod(target, fileMode)
	return nil
}

func (s *FileStore) Get(id string) (*record, error) {
	p, err := s.pathFor(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errNotFoundRecord
		}
		return nil, fmt.Errorf("read record: %w", err)
	}
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("stored record is corrupt: %w", err)
	}
	return &rec, nil
}

func (s *FileStore) Delete(id string) error {
	p, err := s.pathFor(id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete record: %w", err)
	}
	return nil
}

func (s *FileStore) PurgeExpired(now time.Time) (int, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return 0, fmt.Errorf("list store: %w", err)
	}
	nowMs := now.UnixMilli()
	removed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		rec, err := s.Get(id)
		if err != nil || nowMs >= rec.ExpiresAt {
			if dErr := s.Delete(id); dErr == nil {
				removed++
			}
		}
	}
	return removed, nil
}

func (s *FileStore) Close() error { return nil }

func newID() string {
	// RFC 4122 v4 UUID from crypto/rand.
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is unrecoverable on a healthy host; surface it.
		panic(fmt.Errorf("read random bytes for uuid: %w", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
