package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Preferences struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Mode   string `json:"mode"`
	Auto   int    `json:"auto"`
	OnExit bool   `json:"onExit"`
}

func defaultPreferences() Preferences { return Preferences{1280, 720, "window", 60, true} }
func (p Preferences) valid() bool {
	dimensions := map[[2]int]bool{{960, 540}: true, {1280, 720}: true, {1366, 768}: true, {1600, 900}: true, {1920, 1080}: true, {2560, 1440}: true, {3840, 2160}: true}
	return dimensions[[2]int{p.Width, p.Height}] && (p.Mode == "window" || p.Mode == "max") && (p.Auto == 0 || p.Auto == 15 || p.Auto == 30 || p.Auto == 60 || p.Auto == 120 || p.Auto == 300 || p.Auto == 600)
}
func atomicWrite(path string, b []byte) error {
	tmp := path + ".tmp"
	if e := os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	if e := os.Rename(tmp, path); e != nil {
		os.Remove(tmp)
		return e
	}
	return nil
}

type slotMeta struct {
	Slot    int    `json:"slot"`
	Name    string `json:"name"`
	Tick    int    `json:"tick"`
	Seed    uint32 `json:"seed"`
	SavedAt int64  `json:"savedAt"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Version int    `json:"version"`
}
type slotRecord struct {
	Name  string          `json:"name"`
	World json.RawMessage `json:"world"`
}

func readMeta(b []byte) (slotMeta, bool) {
	var m slotMeta
	if json.Unmarshal(b, &m) != nil || m.Version < 1 || m.Version > 12 || m.Tick < 0 || m.SavedAt < 0 {
		return m, false
	}
	return m, true
}
func registerSettings(mux *http.ServeMux, url, dir string) {
	prefsPath := filepath.Join(dir, "preferences.json")
	slotPath := func(n int) string { return filepath.Join(dir, "slot-"+strconv.Itoa(n)+".json") }
	respond := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(v)
	}
	allowed := func(r *http.Request) bool {
		return r.Header.Get("Origin") == url && strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
	}
	mux.HandleFunc("/preferences", func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()
		defer lock.Unlock()
		p := defaultPreferences()
		switch r.Method {
		case "GET":
			if b, e := os.ReadFile(prefsPath); e == nil {
				var saved Preferences
				if json.Unmarshal(b, &saved) == nil && saved.valid() {
					p = saved
				}
			}
			resizeDesktop(p)
			respond(w, p)
		case "POST":
			if !allowed(r) {
				http.Error(w, "forbidden", 403)
				return
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&p) != nil || !p.valid() {
				http.Error(w, "invalid settings", 400)
				return
			}
			b, _ := json.Marshal(p)
			if atomicWrite(prefsPath, b) != nil {
				http.Error(w, "settings write failed", 500)
				return
			}
			resizeDesktop(p)
			respond(w, p)
		default:
			http.Error(w, "method", 405)
		}
	})
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "method", 405)
			return
		}
		lock.Lock()
		defer lock.Unlock()
		out := []slotMeta{}
		if b, e := os.ReadFile(filepath.Join(dir, "world.json")); e == nil {
			if m, ok := readMeta(b); ok {
				m.Slot = 0
				m.Name = "Последний активный мир"
				out = append(out, m)
			}
		}
		for n := 1; n <= 10; n++ {
			if b, e := os.ReadFile(slotPath(n)); e == nil {
				var rec slotRecord
				if json.Unmarshal(b, &rec) == nil {
					if m, ok := readMeta(rec.World); ok {
						m.Slot = n
						m.Name = rec.Name
						out = append(out, m)
					}
				}
			}
		}
		respond(w, out)
	})
	mux.HandleFunc("/slots/", func(w http.ResponseWriter, r *http.Request) {
		n, e := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/slots/"))
		if e != nil || n < 0 || n > 10 {
			http.NotFound(w, r)
			return
		}
		lock.Lock()
		defer lock.Unlock()
		path := slotPath(n)
		switch r.Method {
		case "GET":
			if n == 0 {
				b, e := os.ReadFile(filepath.Join(dir, "world.json"))
				if e != nil {
					http.NotFound(w, r)
					return
				}
				respond(w, slotRecord{"Последний активный мир", b})
				return
			}
			b, e := os.ReadFile(path)
			if e != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.Write(b)
		case "POST":
			if n == 0 || !allowed(r) {
				http.Error(w, "forbidden", 403)
				return
			}
			b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 33<<20))
			var rec slotRecord
			if e != nil || json.Unmarshal(b, &rec) != nil || len([]rune(rec.Name)) > 80 || len(rec.World) > 32<<20 {
				http.Error(w, "invalid save", 400)
				return
			}
			if _, ok := readMeta(rec.World); !ok {
				http.Error(w, "invalid world", 400)
				return
			}
			if atomicWrite(path, b) != nil {
				http.Error(w, "save write failed", 500)
				return
			}
			w.WriteHeader(204)
		case "DELETE":
			if n == 0 || !allowed(r) {
				http.Error(w, "forbidden", 403)
				return
			}
			if e := os.Remove(path); e != nil && !os.IsNotExist(e) {
				http.Error(w, "delete failed", 500)
				return
			}
			w.WriteHeader(204)
		default:
			http.Error(w, "method", 405)
		}
	})
}
