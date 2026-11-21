package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed index.html adventures.js
var assets embed.FS
var lock sync.Mutex

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--apply-update" {
		applyUpdate(os.Args[2:])
		return
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserConfigDir()
	}
	if base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "QuietWorld", "Saves")
	if err := os.MkdirAll(dir, 0700); err != nil {
		log.Fatal(err)
	}
	save := filepath.Join(dir, "world.json")
	const addr = "127.0.0.1:17843"
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		showError("Не удалось открыть локальный порт. Закройте предыдущую версию игры и повторите запуск.")
		return
	}
	url := "http://" + ln.Addr().String()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/adventures.js" {
			b, _ := assets.ReadFile("adventures.js")
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Write(b)
			return
		}
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, _ := assets.ReadFile("index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "quietworld-v5")
	})
	mux.HandleFunc("/save", func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()
		defer lock.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		switch r.Method {
		case "GET":
			w.Header().Set("Content-Type", "application/json")
			b, e := readActiveSave(save)
			if e != nil {
				if !os.IsNotExist(e) {
					http.Error(w, "save read error", 500)
					return
				}
				io.WriteString(w, "null")
				return
			}
			w.Write(b)
		case "POST":
			if origin := r.Header.Get("Origin"); origin != "" && origin != url {
				http.Error(w, "forbidden", 403)
				return
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				http.Error(w, "unsupported", 415)
				return
			}
			b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
			if e != nil || !json.Valid(b) {
				http.Error(w, "invalid save", 400)
				return
			}
			var meta struct {
				Version int   `json:"version"`
				Tick    int   `json:"tick"`
				SavedAt int64 `json:"savedAt"`
			}
			if json.Unmarshal(b, &meta) != nil || (meta.Version != 1 && meta.Version != 2 && meta.Version != 3 && meta.Version != 4 && meta.Version != 5 && meta.Version != 6 && meta.Version != 7 && meta.Version != 8 && meta.Version != 9 && meta.Version != 10 && meta.Version != 11 && meta.Version != 12) || meta.Tick < 0 {
				http.Error(w, "invalid world", 400)
				return
			}
			if prev, e := readActiveSave(save); e == nil && meta.SavedAt > 0 {
				var old struct {
					SavedAt int64 `json:"savedAt"`
				}
				if json.Unmarshal(prev, &old) == nil && old.SavedAt > meta.SavedAt {
					w.WriteHeader(204)
					return
				}
			}
			e = writeActiveSave(save, b)
			if e != nil {
				http.Error(w, "save write error", 500)
				return
			}
			w.WriteHeader(204)
		default:
			http.Error(w, "method", 405)
		}
	})
	registerSettings(mux, url, dir)
	registerUpdate(mux, url, save)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != ln.Addr().String() {
			http.Error(w, "invalid host", 403)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		mux.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if e := srv.Serve(ln); e != nil && e != http.ErrServerClosed {
			log.Println(e)
		}
	}()
	defer srv.Close()
	if os.Getenv("QUIETWORLD_NO_BROWSER") == "1" {
		fmt.Println(url)
		select {}
	}
	if e := desktop(url, filepath.Join(base, "QuietWorld", "DesktopProfile")); e != nil {
		showError(e.Error())
	}
}
