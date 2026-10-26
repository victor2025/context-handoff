package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type errorBody struct {
	Error apiError `json:"error"`
}

// saveRequest is the POST /api/contexts payload.
type saveRequest struct {
	Payload   string                 `json:"payload"`
	TTL       *int                   `json:"ttl"`
	Title     string                 `json:"title"`
	Format    string                 `json:"format"`
	Metadata  map[string]interface{} `json:"metadata"`
	TokenHash string                 `json:"tokenHash"`
}

// publicRecord is every API projection of a record. TokenHash is NEVER present.
type publicRecord struct {
	ID         string                 `json:"id"`
	Title      string                 `json:"title"`
	Format     string                 `json:"format"`
	Metadata   map[string]interface{} `json:"metadata"`
	Payload    string                 `json:"payload,omitempty"`
	Plaintext  string                 `json:"plaintext,omitempty"`
	CreatedAt  int64                  `json:"createdAt"`
	ExpiresAt  int64                  `json:"expiresAt"`
	TTL        int64                  `json:"ttl"`
	ServerTime int64                  `json:"serverTime"`
}

func toPublic(rec *record, includePayload bool, plaintext string) publicRecord {
	now := time.Now().UnixMilli()
	ttl := (rec.ExpiresAt - now + 999) / 1000
	if ttl < 0 {
		ttl = 0
	}
	pub := publicRecord{
		ID:         rec.ID,
		Title:      rec.Title,
		Format:     rec.Format,
		Metadata:   rec.Metadata,
		CreatedAt:  rec.CreatedAt,
		ExpiresAt:  rec.ExpiresAt,
		TTL:        ttl,
		ServerTime: now,
	}
	if includePayload {
		pub.Payload = rec.Payload // ciphertext envelope
	}
	if plaintext != "" {
		pub.Plaintext = plaintext
	}
	return pub
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	w.Header().Set("x-content-type-options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorBody{Error: apiError{Code: code, Message: msg}})
}

type server struct {
	store Store
}

func newServer(store Store) *server { return &server{store: store} }

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/api/contexts", s.handleCollection)
	mux.HandleFunc("/api/contexts/", s.handleItem)
	return mux
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusNotFound, errNotFoundRoute, "route not found: "+r.Method+" "+r.URL.Path)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":  true,
		"now": time.Now().UTC().Format(time.RFC3339),
		"defaults": map[string]interface{}{
			"ttlSeconds":    defaultTTLSeconds,
			"maxTtlSeconds": maxTTLSeconds,
		},
		"limits": map[string]interface{}{
			"bodyBytes":     maxBodyBytes,
			"metadataBytes": maxMetadataBytes,
		},
	})
}

func (s *server) handleCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusNotFound, errNotFoundRoute, "route not found: "+r.Method+" "+r.URL.Path)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, errInvalidRequest, "cannot read body")
		return
	}
	if len(body) > maxRequestBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, errPayloadTooLarge, "request body too large")
		return
	}
	var req saveRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, errInvalidJSON, "request body is not valid JSON")
		return
	}

	if strings.TrimSpace(req.Payload) == "" {
		writeErr(w, http.StatusBadRequest, errEmptyBody, "payload (encrypted context) is required and must be a non-empty string")
		return
	}
	if len([]byte(req.Payload)) > maxBodyBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, errBodyTooLarge, "payload exceeds limit")
		return
	}
	if err := quickEnvelopeShape(req.Payload); err != nil {
		writeErr(w, http.StatusBadRequest, errInvalidRequest, err.Error())
		return
	}

	ttl := defaultTTLSeconds
	if req.TTL != nil {
		ttl = *req.TTL
		if ttl <= 0 {
			writeErr(w, http.StatusBadRequest, errInvalidTTL, "ttl must be a positive integer (seconds)")
			return
		}
	}
	if ttl > maxTTLSeconds {
		writeErr(w, http.StatusBadRequest, errInvalidTTL, "ttl must not exceed 86400 seconds (1 day)")
		return
	}

	metadata := req.Metadata
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	metaJSON, _ := json.Marshal(metadata)
	if len(metaJSON) > maxMetadataBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, errMetadataTooLarge, "metadata exceeds limit")
		return
	}

	if !isSHA256Hex(req.TokenHash) {
		writeErr(w, http.StatusBadRequest, errInvalidRequest, "tokenHash must be 64 hex chars (sha256 of upper(trim(token)))")
		return
	}

	title := req.Title
	if len(title) > 200 {
		title = title[:200]
	}
	format := req.Format
	if format == "" {
		format = "markdown"
	}
	if len(format) > 40 {
		format = format[:40]
	}

	now := time.Now()
	rec := &record{
		ID:        newID(),
		Title:     title,
		Format:    format,
		Metadata:  metadata,
		Payload:   req.Payload,
		TokenHash: strings.ToLower(req.TokenHash),
		CreatedAt: now.UnixMilli(),
		ExpiresAt: now.Add(time.Duration(ttl) * time.Second).UnixMilli(),
	}
	if err := s.store.Save(rec); err != nil {
		log.Printf("store save: %v", err)
		writeErr(w, http.StatusServiceUnavailable, errStorageUnavailable, "failed to persist record")
		return
	}
	// 201 response: public projection without ciphertext and without hash.
	writeJSON(w, http.StatusCreated, toPublic(rec, false, ""))
}

func (s *server) handleItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/contexts/")
	parts := strings.Split(rest, "/")
	var id string
	load := false
	switch {
	case len(parts) == 1 && parts[0] != "":
		id = parts[0]
	case len(parts) == 2 && parts[1] == "load":
		id, load = parts[0], true
	default:
		writeErr(w, http.StatusNotFound, errNotFoundRoute, "route not found: "+r.Method+" "+r.URL.Path)
		return
	}
	if !idPattern.MatchString(id) {
		writeErr(w, http.StatusNotFound, errNotFound, "context does not exist")
		return
	}
	if !load && r.Method != http.MethodGet {
		writeErr(w, http.StatusNotFound, errNotFoundRoute, "route not found: "+r.Method+" "+r.URL.Path)
		return
	}
	if load && r.Method != http.MethodPost {
		writeErr(w, http.StatusNotFound, errNotFoundRoute, "route not found: "+r.Method+" "+r.URL.Path)
		return
	}

	rec, err := s.store.Get(id)
	if err != nil {
		if errors.Is(err, errNotFoundRecord) {
			writeErr(w, http.StatusNotFound, errNotFound, "context does not exist")
			return
		}
		log.Printf("store get: %v", err)
		writeErr(w, http.StatusServiceUnavailable, errStorageUnavailable, "failed to read record")
		return
	}
	if time.Now().UnixMilli() >= rec.ExpiresAt {
		_ = s.store.Delete(id) // opportunistic cleanup
		writeErr(w, http.StatusGone, errExpired, "context has expired")
		return
	}

	if !load {
		// No token: return ciphertext only (and never the hash).
		writeJSON(w, http.StatusOK, toPublic(rec, true, ""))
		return
	}

	// Load with token.
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil {
		writeErr(w, http.StatusBadRequest, errInvalidRequest, "cannot read body")
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			writeErr(w, http.StatusBadRequest, errInvalidJSON, "request body is not valid JSON")
			return
		}
	}
	if strings.TrimSpace(in.Token) == "" {
		writeErr(w, http.StatusBadRequest, errInvalidToken, "token is required")
		return
	}
	if !tokenHashMatches(hashTokenHex(in.Token), rec.TokenHash) {
		writeErr(w, http.StatusForbidden, errWrongToken, "token does not match")
		return
	}
	plaintext, err := decryptEnvelope(rec.Payload, in.Token)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, errDecryptionFailed, "failed to decrypt context with the supplied token")
		return
	}
	// Plaintext is returned for this request only; it is not stored.
	writeJSON(w, http.StatusOK, toPublic(rec, false, plaintext))
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range strings.ToLower(s) {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
