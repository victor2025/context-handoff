package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testEnv(t *testing.T) (*httptest.Server, string, func()) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ts := httptest.NewServer(newServer(store).routes())
	return ts, dir, func() { ts.Close(); store.Close() }
}

func saveReq(t *testing.T, ts string, body map[string]interface{}) (*http.Response, map[string]interface{}) {
	t.Helper()
	b, _ := json.Marshal(body)
	res, err := http.Post(ts+"/api/contexts", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	var out map[string]interface{}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res, out
}

func validBody(t *testing.T, plaintext, token string) map[string]interface{} {
	t.Helper()
	payload, err := encryptEnvelope(plaintext, token, 200000)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return map[string]interface{}{
		"payload":   payload,
		"tokenHash": hashTokenHex(token),
		"title":     "t",
	}
}

func TestHealth(t *testing.T) {
	ts, _, done := testEnv(t)
	defer done()
	res, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	var h map[string]interface{}
	json.NewDecoder(res.Body).Decode(&h)
	if h["ok"] != true {
		t.Fatal("health not ok")
	}
}

func TestSaveDefaultTTLAndNoHashLeak(t *testing.T) {
	ts, _, done := testEnv(t)
	defer done()
	token := "ABCDEFGHJKMNPQRSTVWXYZ"
	res, out := saveReq(t, ts.URL, validBody(t, "hi", token))
	if res.StatusCode != 201 {
		t.Fatalf("status=%d body=%v", res.StatusCode, out)
	}
	id, _ := out["id"].(string)
	if len(id) != 36 {
		t.Fatalf("bad id %q", id)
	}
	if int(out["ttl"].(float64)) != defaultTTLSeconds {
		t.Fatalf("ttl=%v", out["ttl"])
	}
	raw, _ := json.Marshal(out)
	if bytes.Contains(raw, []byte("tokenHash")) {
		t.Fatalf("tokenHash leaked: %s", raw)
	}
}

func TestMaxTTLRejected(t *testing.T) {
	ts, _, done := testEnv(t)
	defer done()
	body := validBody(t, "hi", "TOKEN0000000000000000001")
	body["ttl"] = maxTTLSeconds + 1
	res, out := saveReq(t, ts.URL, body)
	if res.StatusCode != 400 || out["error"].(map[string]interface{})["code"] != errInvalidTTL {
		t.Fatalf("got %d %v", res.StatusCode, out)
	}
}

func TestEmptyBody(t *testing.T) {
	ts, _, done := testEnv(t)
	defer done()
	res, out := saveReq(t, ts.URL, map[string]interface{}{"payload": "", "tokenHash": strings.Repeat("a", 64)})
	if res.StatusCode != 400 || out["error"].(map[string]interface{})["code"] != errEmptyBody {
		t.Fatalf("got %d %v", res.StatusCode, out)
	}
}

func TestBodyTooLarge(t *testing.T) {
	ts, _, done := testEnv(t)
	defer done()
	token := "TOKENTOOLARGETOKEN0000000001"
	payload, err := encryptEnvelope(strings.Repeat("x", maxBodyBytes+1), token, 1000)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]interface{}{"payload": payload, "tokenHash": hashTokenHex(token)}
	res, out := saveReq(t, ts.URL, body)
	if res.StatusCode != 413 || out["error"].(map[string]interface{})["code"] != errBodyTooLarge {
		t.Fatalf("got %d %v", res.StatusCode, out)
	}
}

func TestMetadataTooLarge(t *testing.T) {
	ts, _, done := testEnv(t)
	defer done()
	body := validBody(t, "hi", "TOKENTOOLARGETOKEN0000000002")
	body["metadata"] = map[string]interface{}{"blob": strings.Repeat("y", maxMetadataBytes)}
	res, out := saveReq(t, ts.URL, body)
	if res.StatusCode != 413 || out["error"].(map[string]interface{})["code"] != errMetadataTooLarge {
		t.Fatalf("got %d %v", res.StatusCode, out)
	}
}

func TestInvalidJSON(t *testing.T) {
	ts, _, done := testEnv(t)
	defer done()
	res, err := http.Post(ts.URL+"/api/contexts", "application/json", strings.NewReader("{nope"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]interface{}
	json.NewDecoder(res.Body).Decode(&out)
	if res.StatusCode != 400 || out["error"].(map[string]interface{})["code"] != errInvalidJSON {
		t.Fatalf("got %d %v", res.StatusCode, out)
	}
}

func TestUnknownRouteAndMissingID(t *testing.T) {
	ts, _, done := testEnv(t)
	defer done()
	r1, _ := http.Get(ts.URL + "/nope")
	if r1.StatusCode != 404 {
		t.Fatalf("route status=%d", r1.StatusCode)
	}
	r2, _ := http.Get(ts.URL + "/api/contexts/00000000-0000-4000-8000-000000000000")
	if r2.StatusCode != 404 {
		t.Fatalf("missing status=%d", r2.StatusCode)
	}
}

func TestReadWithoutTokenReturnsCiphertext(t *testing.T) {
	ts, _, done := testEnv(t)
	defer done()
	token := "TOKENTOOLARGETOKEN0000000003"
	_, out := saveReq(t, ts.URL, validBody(t, "secret-ish", token))
	id := out["id"].(string)
	res, err := http.Get(ts.URL + "/api/contexts/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got map[string]interface{}
	json.NewDecoder(res.Body).Decode(&got)
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	if !strings.Contains(got["payload"].(string), "ciphertext") {
		t.Fatalf("no ciphertext: %v", got)
	}
	raw, _ := json.Marshal(got)
	if bytes.Contains(raw, []byte("tokenHash")) {
		t.Fatal("hash leaked")
	}
}

func TestCorrectTokenReturnsPlaintext(t *testing.T) {
	ts, _, done := testEnv(t)
	defer done()
	token := "TOKENTOOLARGETOKEN0000000004"
	secret := "# Goal\n\nwin"
	_, out := saveReq(t, ts.URL, validBody(t, secret, token))
	id := out["id"].(string)
	res, body := loadPOST(t, ts.URL, id, map[string]interface{}{"token": token})
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%v", res.StatusCode, body)
	}
	if body["plaintext"] != secret {
		t.Fatalf("plaintext=%v", body["plaintext"])
	}
	raw, _ := json.Marshal(body)
	if bytes.Contains(raw, []byte("tokenHash")) {
		t.Fatal("hash leaked")
	}
}

func TestWrongToken(t *testing.T) {
	ts, _, done := testEnv(t)
	defer done()
	token := "TOKENTOOLARGETOKEN0000000005"
	_, out := saveReq(t, ts.URL, validBody(t, "hi", token))
	id := out["id"].(string)
	res, body := loadPOST(t, ts.URL, id, map[string]interface{}{"token": "WRONGWRONGWRONGWRONG000"})
	if res.StatusCode != 403 || body["error"].(map[string]interface{})["code"] != errWrongToken {
		t.Fatalf("got %d %v", res.StatusCode, body)
	}
}

func TestMissingToken(t *testing.T) {
	ts, _, done := testEnv(t)
	defer done()
	token := "TOKENTOOLARGETOKEN0000000006"
	_, out := saveReq(t, ts.URL, validBody(t, "hi", token))
	id := out["id"].(string)
	res, body := loadPOST(t, ts.URL, id, map[string]interface{}{})
	if res.StatusCode != 400 || body["error"].(map[string]interface{})["code"] != errInvalidToken {
		t.Fatalf("got %d %v", res.StatusCode, body)
	}
}

func TestCaseInsensitiveToken(t *testing.T) {
	ts, _, done := testEnv(t)
	defer done()
	token := "TOKENTOOLARGETOKEN0000000007"
	_, out := saveReq(t, ts.URL, validBody(t, "mixed", token))
	id := out["id"].(string)
	res, body := loadPOST(t, ts.URL, id, map[string]interface{}{"token": "  " + strings.ToLower(token) + " "})
	if res.StatusCode != 200 || body["plaintext"] != "mixed" {
		t.Fatalf("got %d %v", res.StatusCode, body)
	}
}

func TestExpiredReturnsExpiredAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	rec := &record{ID: newID(), Payload: "x", TokenHash: strings.Repeat("a", 64), CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(-time.Second).UnixMilli()}
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(newServer(store).routes())
	defer ts.Close()
	res, _ := http.Get(ts.URL + "/api/contexts/" + rec.ID)
	if res.StatusCode != 410 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(dir, rec.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("expired file not removed: %v", err)
	}
}

func TestPersistPermissionsAndPurge(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := &record{ID: newID(), Payload: "x", TokenHash: strings.Repeat("a", 64), ExpiresAt: now.Add(-time.Second).UnixMilli()}
	fresh := &record{ID: newID(), Payload: "y", TokenHash: strings.Repeat("b", 64), ExpiresAt: now.Add(time.Hour).UnixMilli()}
	if err := store.Save(old); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(fresh); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, fresh.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm=%o", info.Mode().Perm())
	}
	// persisted file contains hash but never plaintext field
	data, _ := os.ReadFile(filepath.Join(dir, fresh.ID+".json"))
	if !bytes.Contains(data, []byte("tokenHash")) {
		t.Fatal("hash should be stored on disk")
	}
	if bytes.Contains(data, []byte("plaintext")) {
		t.Fatal("plaintext must never be stored")
	}
	n, err := store.PurgeExpired(now)
	if err != nil || n < 1 {
		t.Fatalf("purge n=%d err=%v", n, err)
	}
	if _, err := store.Get(fresh.ID); err != nil {
		t.Fatalf("fresh record removed: %v", err)
	}
	if _, err := store.Get(old.ID); err != errNotFoundRecord {
		t.Fatalf("old record not purged: %v", err)
	}
}

func loadPOST(t *testing.T, ts, id string, body map[string]interface{}) (*http.Response, map[string]interface{}) {
	t.Helper()
	b, _ := json.Marshal(body)
	res, err := http.Post(ts+"/api/contexts/"+id+"/load", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("post load: %v", err)
	}
	defer res.Body.Close()
	var out map[string]interface{}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res, out
}
