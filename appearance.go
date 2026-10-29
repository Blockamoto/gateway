package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

type appearancePreference struct {
	Theme   string `json:"theme"`
	Default string `json:"default,omitempty"`
}

func (a *app) appearancePath() string { return filepath.Join(a.dataDir, "appearance.json") }

func (a *app) readAppearance() (appearancePreference, error) {
	p := appearancePreference{Default: "light"}
	b, err := os.ReadFile(a.appearancePath())
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(b, &p); err != nil {
		return p, err
	}
	if p.Theme != "light" && p.Theme != "dark" {
		return p, fmt.Errorf("invalid saved appearance")
	}
	p.Default = "light"
	return p, nil
}

// Appearance belongs to the profile, so a new local management port or an
// application update does not discard an explicit palette choice.
func (a *app) handleAppearance(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, 403, fmt.Errorf("appearance is restricted to the local management interface"))
		return
	}
	if r.Method == http.MethodGet {
		p, err := a.readAppearance()
		if err != nil {
			jsonError(w, 500, err)
			return
		}
		satlineReply(w, p)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "GET or POST required", 405)
		return
	}
	var p appearancePreference
	if !satlineBody(w, r, &p) {
		return
	}
	if p.Theme != "light" && p.Theme != "dark" {
		jsonError(w, 400, fmt.Errorf("appearance must be light or dark"))
		return
	}
	p.Default = "light"
	b, err := json.Marshal(p)
	if err == nil {
		err = atomicWriteBytes(a.appearancePath(), b)
	}
	if err != nil {
		jsonError(w, 500, err)
		return
	}
	satlineReply(w, p)
}
