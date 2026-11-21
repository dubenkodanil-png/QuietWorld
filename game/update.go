package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const appVersion = "7.0.1"
const maxUpdate = 64 << 20

var pendingUpdate atomic.Bool

type updateManifest struct {
	Product string   `json:"product"`
	Version string   `json:"version"`
	SHA256  string   `json:"sha256"`
	Notes   []string `json:"notes,omitempty"`
}

func versionParts(v string) ([3]int, bool) {
	var out [3]int
	p := strings.Split(v, ".")
	if len(p) != 3 {
		return out, false
	}
	for i, s := range p {
		n, e := strconv.Atoi(s)
		if e != nil || n < 0 || n > 99999 || strconv.Itoa(n) != s {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
func newer(a, b string) bool {
	av, ok := versionParts(a)
	bv, ok2 := versionParts(b)
	if !ok || !ok2 {
		return false
	}
	for i := range av {
		if av[i] != bv[i] {
			return av[i] > bv[i]
		}
	}
	return false
}
func zipBytes(f *zip.File, limit int64) ([]byte, error) {
	if f.UncompressedSize64 > uint64(limit) {
		return nil, errors.New("Файл слишком большой")
	}
	r, e := f.Open()
	if e != nil {
		return nil, e
	}
	defer r.Close()
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, errors.New("Файл повреждён или слишком большой")
	}
	return b, nil
}
func validateUpdate(data []byte) (updateManifest, []byte, error) {
	var m updateManifest
	z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil {
		return m, nil, errors.New("Неверный формат .qwupdate")
	}
	if len(z.File) != 2 {
		return m, nil, errors.New("В пакете должны быть только manifest.json и QuietWorld.exe")
	}
	var manifest, payload *zip.File
	for _, f := range z.File {
		switch f.Name {
		case "manifest.json":
			manifest = f
		case "QuietWorld.exe":
			payload = f
		default:
			return m, nil, errors.New("Неизвестный файл в пакете")
		}
	}
	if manifest == nil || payload == nil {
		return m, nil, errors.New("Неполный пакет")
	}
	b, e := zipBytes(manifest, 4096)
	if e != nil || json.Unmarshal(b, &m) != nil || m.Product != "QuietWorld" || !newer(m.Version, appVersion) {
		return m, nil, fmt.Errorf("Пакет не подходит: требуется версия новее %s", appVersion)
	}
	exe, e := zipBytes(payload, maxUpdate)
	if e != nil {
		return m, nil, e
	}
	sum := sha256.Sum256(exe)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), m.SHA256) {
		return m, nil, errors.New("Контрольная сумма не совпадает")
	}
	p, e := pe.NewFile(bytes.NewReader(exe))
	if e != nil {
		return m, nil, errors.New("Неверный исполняемый файл Windows")
	}
	defer p.Close()
	if p.Machine != pe.IMAGE_FILE_MACHINE_AMD64 || p.FileHeader.Characteristics&pe.IMAGE_FILE_DLL != 0 {
		return m, nil, errors.New("Требуется приложение Windows x64")
	}
	return m, exe, nil
}
func registerUpdate(mux *http.ServeMux, url, save string) {
	registerUpdateSource(mux, url, filepath.Dir(save))
	mux.HandleFunc("/update/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(map[string]string{"version": appVersion})
	})
	mux.HandleFunc("/update/apply", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Origin") != url || r.Header.Get("X-QuietWorld-Update") != "local-package" {
			http.Error(w, "Запрос отклонён", 403)
			return
		}
		if !pendingUpdate.CompareAndSwap(false, true) {
			http.Error(w, "Обновление уже выполняется", 409)
			return
		}
		ok := false
		defer func() {
			if !ok {
				pendingUpdate.Store(false)
			}
		}()
		b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUpdate))
		if e != nil {
			http.Error(w, "Не удалось прочитать пакет", 400)
			return
		}
		m, exe, e := validateUpdate(b)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		// A successful UI save must exist before touching the installed application.
		lock.Lock()
		snapshot, e := os.ReadFile(save)
		if e == nil {
			e = os.WriteFile(save+".preupdate", snapshot, 0600)
		}
		lock.Unlock()
		if e != nil || !json.Valid(snapshot) {
			http.Error(w, "Не удалось создать резервную копию мира", 500)
			return
		}
		if e = stageUpdate(exe, m.Version); e != nil {
			http.Error(w, "Не удалось подготовить установку: "+e.Error(), 500)
			return
		}
		ok = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(m)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		go func() { time.Sleep(900 * time.Millisecond); requestUpdateExit() }()
	})
}
func stageUpdate(exe []byte, version string) error {
	self, e := os.Executable()
	if e != nil {
		return e
	}
	dir := filepath.Dir(self)
	// Keep each stage adjacent to the installed exe, on the same volume.
	stage, e := os.MkdirTemp(dir, ".qw-update-")
	if e != nil {
		return e
	}
	success := false
	defer func() {
		if !success {
			os.RemoveAll(stage)
		}
	}()
	next := filepath.Join(stage, "next.exe")
	helper := filepath.Join(stage, "updater.exe")
	if e = os.WriteFile(next, exe, 0700); e != nil {
		return e
	}
	old, e := os.ReadFile(self)
	if e != nil {
		return e
	}
	if e = os.WriteFile(helper, old, 0700); e != nil {
		return e
	}
	if e = startUpdater(helper, self, next, version); e != nil {
		return e
	}
	success = true
	return nil
}
