package main

// Interoperable crypto with the Node client envelope:
//   v=1, AES-256-GCM, PBKDF2-HMAC-SHA256, base64 salt/nonce/ciphertext/tag.
// Decryption is ephemeral per /load request; plaintext is never persisted.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type kdfParams struct {
	Iterations int    `json:"iterations"`
	KeyLength  int    `json:"keyLength"`
	SaltLength int    `json:"saltLength"`
	NonceLen   int    `json:"nonceLength"`
	Digest     string `json:"digest"`
}

type envelope struct {
	V         int       `json:"v"`
	Alg       string    `json:"alg"`
	KDF       string    `json:"kdf"`
	KDFParams kdfParams `json:"kdfParams"`
	Salt      string    `json:"salt"`
	Nonce     string    `json:"nonce"`
	Cipher    string    `json:"ciphertext"`
	Tag       string    `json:"tag"`
}

func normalizeToken(token string) string {
	return strings.ToUpper(strings.TrimSpace(token))
}

func hashTokenHex(token string) string {
	sum := sha256.Sum256([]byte(normalizeToken(token)))
	return hex.EncodeToString(sum[:])
}

// constant-time hex hash comparison; both are 32-byte sha256 digests.
func tokenHashMatches(provided, expected string) bool {
	a, err1 := hex.DecodeString(strings.ToLower(strings.TrimSpace(provided)))
	b, err2 := hex.DecodeString(strings.ToLower(strings.TrimSpace(expected)))
	if err1 != nil || err2 != nil || len(a) != len(b) || len(a) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}

// pbkdf2HMACSHA256 implements RFC 2898 PBKDF2 with HMAC-SHA256.
func pbkdf2HMACSHA256(password, salt []byte, iter, keyLen int) []byte {
	const hLen = sha256.Size
	numBlocks := (keyLen + hLen - 1) / hLen
	dk := make([]byte, 0, numBlocks*hLen)
	var blockIndex [4]byte

	prf := hmac.New(sha256.New, password)

	U := make([]byte, hLen)
	T := make([]byte, hLen)
	for block := 1; block <= numBlocks; block++ {
		blockIndex[0] = byte(block >> 24)
		blockIndex[1] = byte(block >> 16)
		blockIndex[2] = byte(block >> 8)
		blockIndex[3] = byte(block)

		prf.Reset()
		prf.Write(salt)
		prf.Write(blockIndex[:])
		U = prf.Sum(U[:0])
		copy(T, U)

		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(U)
			U = prf.Sum(U[:0])
			for x := range T {
				T[x] ^= U[x]
			}
		}
		dk = append(dk, T...)
	}
	return dk[:keyLen]
}

var errDecrypt = errors.New("decryption failed")

// encryptEnvelope is the inverse of decryptEnvelope. It is used by the server
// test-suite to construct well-formed ciphertext, and documents the wire
// format. Production clients run the Node (or equivalent) implementation.
func encryptEnvelope(plaintext, token string, iter int) (string, error) {
	if iter <= 0 {
		iter = 200000
	}
	salt := make([]byte, 16)
	nonce := make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	key := pbkdf2HMACSHA256([]byte(normalizeToken(token)), salt, iter, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	tag := sealed[len(sealed)-gcm.Overhead():]
	ct := sealed[:len(sealed)-gcm.Overhead()]
	env := envelope{
		V:   1,
		Alg: "AES-256-GCM",
		KDF: "PBKDF2-HMAC-SHA256",
		KDFParams: kdfParams{
			Iterations: iter, KeyLength: 32, SaltLength: 16, NonceLen: 12, Digest: "sha256",
		},
		Salt:   base64.StdEncoding.EncodeToString(salt),
		Nonce:  base64.StdEncoding.EncodeToString(nonce),
		Cipher: base64.StdEncoding.EncodeToString(ct),
		Tag:    base64.StdEncoding.EncodeToString(tag),
	}
	b, err := json.Marshal(env)
	return string(b), err
}

func decryptEnvelope(payload, token string) (string, error) {
	var env envelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		return "", fmt.Errorf("%w: envelope is not valid JSON", errDecrypt)
	}
	if env.V != 1 || env.Alg != "AES-256-GCM" || env.KDF != "PBKDF2-HMAC-SHA256" {
		return "", fmt.Errorf("%w: unsupported envelope", errDecrypt)
	}
	iter := env.KDFParams.Iterations
	if iter <= 0 {
		iter = 200000
	}
	keyLen := env.KDFParams.KeyLength
	if keyLen != 32 {
		keyLen = 32
	}

	salt, err := base64.StdEncoding.DecodeString(env.Salt)
	if err != nil {
		return "", fmt.Errorf("%w: bad salt", errDecrypt)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return "", fmt.Errorf("%w: bad nonce", errDecrypt)
	}
	cipherB64, err := base64.StdEncoding.DecodeString(env.Cipher)
	if err != nil {
		return "", fmt.Errorf("%w: bad ciphertext", errDecrypt)
	}
	tag, err := base64.StdEncoding.DecodeString(env.Tag)
	if err != nil {
		return "", fmt.Errorf("%w: bad tag", errDecrypt)
	}
	if len(salt) == 0 || len(nonce) == 0 || len(cipherB64) == 0 || len(tag) == 0 {
		return "", fmt.Errorf("%w: incomplete envelope", errDecrypt)
	}

	key := pbkdf2HMACSHA256([]byte(normalizeToken(token)), salt, iter, keyLen)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errDecrypt, err)
	}
	gcm, err := cipher.NewGCMWithTagSize(block, len(tag))
	if err != nil {
		return "", fmt.Errorf("%w: %v", errDecrypt, err)
	}
	if len(nonce) != gcm.NonceSize() {
		return "", fmt.Errorf("%w: bad nonce size", errDecrypt)
	}
	// Go's gcm expects ciphertext||tag; Node serializes them separately.
	sealed := make([]byte, 0, len(cipherB64)+len(tag))
	sealed = append(sealed, cipherB64...)
	sealed = append(sealed, tag...)
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("%w: authentication failed", errDecrypt)
	}
	return string(plaintext), nil
}

// quickEnvelopeShape sanity-checks an uploaded payload parses and has the
// required fields. The server cannot validate the tag without the token.
func quickEnvelopeShape(payload string) error {
	var env envelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		return errors.New("payload is not a valid JSON envelope")
	}
	if env.V != 1 || env.Cipher == "" || env.Salt == "" || env.Nonce == "" || env.Tag == "" {
		return errors.New("payload is not a valid encryption envelope")
	}
	return nil
}
