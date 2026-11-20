package main

import (
	"encoding/json"
	"errors"
	"os"
)

func plausibleSave(b []byte) bool {
	if _, ok := readMeta(b); !ok {
		return false
	}
	var head struct {
		Tiles    []int             `json:"tiles"`
		Towns    []json.RawMessage `json:"towns"`
		Factions []json.RawMessage `json:"factions"`
		Version  int               `json:"version"`
		Width    int               `json:"width"`
		Height   int               `json:"height"`
	}
	if json.Unmarshal(b, &head) != nil {
		return false
	}
	if head.Version == 1 {
		head.Width = 30
		head.Height = 18
	}
	if head.Width < 1 || head.Width > 384 || head.Height < 1 || head.Height > 240 {
		return false
	}
	return len(head.Tiles) == head.Width*head.Height && head.Towns != nil && len(head.Factions) >= 1 && len(head.Factions) <= 16
}
func readActiveSave(path string) ([]byte, error) {
	b, e := os.ReadFile(path)
	if e == nil && plausibleSave(b) {
		return b, nil
	}
	old, oe := os.ReadFile(path + ".bak")
	if oe == nil && plausibleSave(old) {
		return old, nil
	}
	if os.IsNotExist(e) && os.IsNotExist(oe) {
		return nil, e
	}
	return nil, errors.New("active world and backup unavailable or invalid")
}
func writeActiveSave(path string, b []byte) error {
	if !plausibleSave(b) {
		return errors.New("invalid world")
	}
	if prev, e := os.ReadFile(path); e == nil && plausibleSave(prev) {
		if e = atomicWrite(path+".bak", prev); e != nil {
			return e
		}
	}
	return atomicWrite(path, b)
}
