package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var currentNotes = []string{
	"История всех версий с V1.0.0 в окне «Что нового».",
	"Поиск по версии и содержанию, архив доступен без интернета.",
	"Автоматическое добавление новых записей при сборке.",
}

type remoteRelease struct {
	Product string   `json:"product"`
	Version string   `json:"version"`
	Package string   `json:"package"`
	SHA256  string   `json:"sha256"`
	Notes   []string `json:"notes"`
}
type sourceConfig struct {
	URL string `json:"url"`
}
type checkResult struct {
	Version   string   `json:"version"`
	Notes     []string `json:"notes"`
	Source    string   `json:"source"`
	Available bool     `json:"available"`
}

func httpsURL(s string) (*url.URL, error) {
	u, e := url.Parse(s)
	if e != nil || len(s) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("Нужен HTTPS-адрес файла latest.json без логина и пароля")
	}
	return u, nil
}
func fetchLimit(client *http.Client, u *url.URL, limit int64) ([]byte, error) {
	req, e := http.NewRequest("GET", u.String(), nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "QuietWorld/"+appVersion)
	req.Header.Set("Cache-Control", "no-cache")
	r, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("Сервер обновлений: HTTP %d", r.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, errors.New("Ответ сервера повреждён или слишком большой")
	}
	return b, nil
}
func sameSourceClient(origin *url.URL) *http.Client {
	return &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 4 || req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Host, origin.Host) || req.URL.User != nil {
			return errors.New("Перенаправление на другой источник запрещено")
		}
		return nil
	}}
}
func remoteCandidate(source string) (checkResult, []byte, error) {
	out := checkResult{Version: appVersion, Source: "HTTPS"}
	u, e := httpsURL(source)
	if e != nil {
		return out, nil, e
	}
	client := sameSourceClient(u)
	b, e := fetchLimit(client, u, 64<<10)
	if e != nil {
		return out, nil, e
	}
	var m remoteRelease
	if json.Unmarshal(b, &m) != nil || m.Product != "QuietWorld" {
		return out, nil, errors.New("Неверный latest.json")
	}
	if _, ok := versionParts(m.Version); !ok {
		return out, nil, errors.New("Неверная версия в latest.json")
	}
	if !newer(m.Version, appVersion) {
		return out, nil, nil
	}
	digest, e := hex.DecodeString(m.SHA256)
	if e != nil || len(digest) != 32 {
		return out, nil, errors.New("Некорректная SHA-256 пакета")
	}
	p, e := u.Parse(m.Package)
	if e != nil || p.Scheme != "https" || !strings.EqualFold(p.Host, u.Host) || p.User != nil || p.Fragment != "" || !strings.HasSuffix(strings.ToLower(p.Path), ".qwupdate") {
		return out, nil, errors.New("Пакет должен находиться на том же HTTPS-сервере")
	}
	data, e := fetchLimit(client, p, maxUpdate)
	if e != nil {
		return out, nil, e
	}
	hash := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(hash[:]), m.SHA256) {
		return out, nil, errors.New("Контрольная сумма пакета не совпала")
	}
	vm, _, e := validateUpdate(data)
	if e != nil {
		return out, nil, e
	}
	if vm.Version != m.Version {
		return out, nil, errors.New("Версия пакета не соответствует latest.json")
	}
	notes := vm.Notes
	if len(notes) == 0 {
		notes = m.Notes
	}
	return checkResult{vm.Version, notes, "HTTPS", true}, data, nil
}
func localCandidate(dir string) (checkResult, []byte, error) {
	out := checkResult{Version: appVersion, Source: "Папка обновлений"}
	var best []byte
	entries, e := os.ReadDir(dir)
	if e != nil {
		return out, nil, e
	}
	invalid := false
	for _, f := range entries {
		if f.IsDir() || f.Type()&os.ModeSymlink != 0 || !strings.HasPrefix(f.Name(), "QuietWorld_") || !strings.HasSuffix(strings.ToLower(f.Name()), ".qwupdate") {
			continue
		}
		v := strings.TrimSuffix(strings.TrimPrefix(f.Name(), "QuietWorld_"), ".qwupdate")
		if !newer(v, out.Version) {
			continue
		}
		info, e := f.Info()
		if e != nil || !info.Mode().IsRegular() || info.Size() > maxUpdate {
			invalid = true
			continue
		}
		fh, e := os.Open(filepath.Join(dir, f.Name()))
		if e != nil {
			invalid = true
			continue
		}
		data, e := io.ReadAll(io.LimitReader(fh, maxUpdate+1))
		fh.Close()
		if e != nil || len(data) > maxUpdate {
			invalid = true
			continue
		}
		m, _, e := validateUpdate(data)
		if e != nil || m.Version != v {
			invalid = true
			continue
		}
		out = checkResult{m.Version, m.Notes, "Папка обновлений", true}
		best = data
	}
	if best == nil && invalid {
		return out, nil, errors.New("Найден повреждённый или неподходящий пакет")
	}
	return out, best, nil
}
func registerUpdateSource(mux *http.ServeMux, origin, saveDir string) {
	dir := filepath.Join(filepath.Dir(saveDir), "Updates")
	_ = os.MkdirAll(dir, 0700)
	config := filepath.Join(saveDir, "update-source.json")
	var mu sync.Mutex
	var cached []byte
	var expires time.Time
	readConfig := func() sourceConfig {
		c := sourceConfig{URL: "https://raw.githubusercontent.com/dubenkodanil-png/QuietWorld/main/updates/latest.json"}
		if b, e := os.ReadFile(config); e == nil {
			_ = json.Unmarshal(b, &c)
		}
		return c
	}
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(v)
	}
	post := func(r *http.Request) bool {
		return r.Method == "POST" && r.Header.Get("Origin") == origin && r.Header.Get("X-QuietWorld-Update") == "source"
	}
	mux.HandleFunc("/update/source", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			changelog, err := assets.ReadFile("changelog.json")
			if err != nil || !json.Valid(changelog) {
				http.Error(w, "История обновлений недоступна", 500)
				return
			}
			reply(w, map[string]any{"url": readConfig().URL, "folder": dir, "version": appVersion, "notes": currentNotes, "history": json.RawMessage(changelog)})
			return
		}
		if !post(r) {
			http.Error(w, "Запрос отклонён", 403)
			return
		}
		var c sourceConfig
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&c) != nil {
			http.Error(w, "Неверные настройки", 400)
			return
		}
		c.URL = strings.TrimSpace(c.URL)
		if c.URL != "" {
			if _, e := httpsURL(c.URL); e != nil {
				http.Error(w, e.Error(), 400)
				return
			}
		}
		b, _ := json.Marshal(c)
		mu.Lock()
		defer mu.Unlock()
		cached = nil
		if e := atomicWrite(config, b); e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		reply(w, map[string]any{"url": c.URL, "folder": dir})
	})
	mux.HandleFunc("/update/check", func(w http.ResponseWriter, r *http.Request) {
		if !post(r) {
			http.Error(w, "Запрос отклонён", 403)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		cached = nil
		c := readConfig()
		var out checkResult
		var data []byte
		var e error
		if c.URL != "" {
			out, data, e = remoteCandidate(c.URL)
		} else {
			out, data, e = localCandidate(dir)
		}
		if e != nil {
			http.Error(w, e.Error(), 502)
			return
		}
		cached = data
		expires = time.Now().Add(10 * time.Minute)
		reply(w, out)
	})
	mux.HandleFunc("/update/candidate", func(w http.ResponseWriter, r *http.Request) {
		if !post(r) {
			http.Error(w, "Запрос отклонён", 403)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if len(cached) == 0 || time.Now().After(expires) {
			http.Error(w, "Сначала проверьте обновления", 404)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(cached)
		cached = nil
	})
}
