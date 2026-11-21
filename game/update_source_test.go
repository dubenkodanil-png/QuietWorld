package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, version string) []byte {
	t.Helper()
	exe, e := os.ReadFile("../setup/QuietWorld.exe")
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(exe)
	m, _ := json.Marshal(updateManifest{Product: "QuietWorld", Version: version, SHA256: hex.EncodeToString(sum[:]), Notes: []string{"Тестовый пакет"}})
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, _ := z.Create("manifest.json")
	f.Write(m)
	f, _ = z.Create("QuietWorld.exe")
	f.Write(exe)
	z.Close()
	return b.Bytes()
}
func TestSource(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Saves")
	os.MkdirAll(dir, 0700)
	mux := http.NewServeMux()
	origin := "http://127.0.0.1:17843"
	registerUpdateSource(mux, origin, dir)
	do := func(method, path, body string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if auth {
			r.Header.Set("Origin", origin)
			r.Header.Set("X-QuietWorld-Update", "source")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if r := do("POST", "/update/source", `{"url":""}`, true); r.Code != 200 {
		t.Fatal(r.Code)
	}
	if r := do("POST", "/update/check", "", false); r.Code != 403 {
		t.Fatal(r.Code)
	}
	if r := do("POST", "/update/check", "", true); r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(`"available":false`)) {
		t.Fatal(r.Code, r.Body.String())
	}
	updates := filepath.Join(root, "Updates")
	b := fixture(t, "7.0.2")
	os.WriteFile(filepath.Join(updates, "QuietWorld_7.0.2.qwupdate"), b, 0600)
	os.WriteFile(filepath.Join(updates, "QuietWorld_7.2.0.qwupdate"), []byte("bad"), 0600)
	r := do("POST", "/update/check", "", true)
	if r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(`"version":"7.0.2"`)) {
		t.Fatal(r.Code, r.Body.String())
	}
	r = do("POST", "/update/candidate", "", true)
	if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), b) {
		t.Fatal(r.Code)
	}
	if r = do("POST", "/update/candidate", "", true); r.Code != 404 {
		t.Fatal(r.Code)
	}
	if r = do("POST", "/update/source", `{"url":"http://example.com/latest.json"}`, true); r.Code != 400 {
		t.Fatal(r.Code)
	}
	if r = do("POST", "/update/source", `{"url":"https://example.com/latest.json"}`, true); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = do("GET", "/update/source", "", false); r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte("https://example.com/latest.json")) {
		t.Fatal(r.Code)
	}
	if r = do("POST", "/update/source", `{"url":""}`, true); r.Code != 200 {
		t.Fatal(r.Code)
	}
	os.Remove(filepath.Join(updates, "QuietWorld_7.0.2.qwupdate"))
	if r = do("POST", "/update/check", "", true); r.Code != 502 {
		t.Fatal(r.Code)
	}
}

func TestChangelog(t *testing.T) {
	mux := http.NewServeMux()
	registerUpdateSource(mux, "http://127.0.0.1:17843", t.TempDir())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/update/source", nil))
	var result struct {
		Version string   `json:"version"`
		Notes   []string `json:"notes"`
		History []struct {
			Version string   `json:"version"`
			Notes   []string `json:"notes"`
		} `json:"history"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if result.Version != appVersion || len(result.History) < 22 || result.History[0].Version != appVersion || result.History[len(result.History)-1].Version != "1.0.0" {
		t.Fatal("Incomplete history")
	}
	a, _ := json.Marshal(result.Notes)
	b, _ := json.Marshal(result.History[0].Notes)
	if !bytes.Equal(a, b) {
		t.Fatal("Current notes mismatch")
	}
	seen := map[string]bool{}
	for i, r := range result.History {
		if seen[r.Version] || len(r.Notes) == 0 || (i > 0 && !newer(result.History[i-1].Version, r.Version)) {
			t.Fatal("Invalid history", r.Version)
		}
		seen[r.Version] = true
	}
}
