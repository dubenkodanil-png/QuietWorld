package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBackup(t *testing.T) {
	p := filepath.Join(t.TempDir(), "world.json")
	mk := func(tick int) []byte {
		b, _ := json.Marshal(map[string]any{"version": 14, "tick": tick, "width": 24, "height": 16, "tiles": make([]int, 384), "towns": []int{}, "factions": []int{1}})
		return b
	}
	a, b := mk(1), mk(2)
	if e := writeActiveSave(p, a); e != nil {
		t.Fatal(e)
	}
	if e := writeActiveSave(p, b); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	r, e := readActiveSave(p)
	if e != nil || !bytes.Equal(r, a) {
		t.Fatal(e)
	}
	if writeActiveSave(p, []byte(`{"version":8}`)) == nil {
		t.Fatal("accepted invalid")
	}
	if e := writeActiveSave(p, b); e != nil {
		t.Fatal(e)
	}
	r, e = os.ReadFile(p + ".bak")
	if e != nil || !bytes.Equal(r, a) {
		t.Fatal(e)
	}
}
