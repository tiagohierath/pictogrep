package main

// What a phone gets for free, and what it does not.
//
// The desktop is unaffected by everything here. It is not distributed by a
// store, it has no billing to answer to, and the plugins it already shipped
// with stay switched on for the people who already have them: taking a
// working feature away from an existing install to sell it back is not a
// thing this does.
//
// On a phone the split is deliberately narrow. Import from web and the
// calendar are the two that make an empty library useful on the day it is
// installed, so they are free; everything else is what premium is for.

import (
	"encoding/json"
	"net/http"
	"os"
)

// freeOnPhone is the set that needs no unlock. Anything not named here is
// premium on a phone, including plugins added later, which is the safer
// direction for a list nobody will remember to update.
var freeOnPhone = map[string]bool{
	"web":      true,
	"calendar": true,
}

type premiumSettings struct {
	Unlocked bool `json:"unlocked"`
}

func (a *application) premiumUnlocked() bool {
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		return false
	}
	var document struct {
		Premium premiumSettings `json:"premium"`
	}
	if json.Unmarshal(data, &document) != nil {
		return false
	}
	return document.Premium.Unlocked
}

func (a *application) savePremium(unlocked bool) error {
	document := map[string]any{}
	if data, err := os.ReadFile(a.configPath); err == nil {
		_ = json.Unmarshal(data, &document)
	}
	document["premium"] = premiumSettings{Unlocked: unlocked}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomically(a.configPath, append(data, '\n'), 0o644)
}

// premiumLocks answers whether this plugin is behind the unlock right now.
// Called by pluginEnabled, so a locked plugin is off everywhere at once: the
// panel it draws, the routes it serves, and the background job it runs.
func (a *application) premiumLocks(name string) bool {
	if !runsOnPhone || freeOnPhone[name] {
		return false
	}
	return !a.premiumUnlocked()
}

// POST /api/app/premium: records the unlock.
//
// This is where a completed Play Billing purchase lands, and until that is
// wired it is also the only way in. Play requires digital goods sold inside
// an app to go through its billing, so the button in the interface is a
// placeholder for that flow rather than the flow itself.
func (s *server) setPremium(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Unlocked bool `json:"unlocked"`
	}
	if err := decodeJSON(r, &request, 1<<16); err != nil {
		sendError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.app.savePremium(request.Unlocked); err != nil {
		sendError(w, http.StatusInternalServerError, err)
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "unlocked": request.Unlocked})
}
