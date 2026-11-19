package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Events the island listens to, with hook timeouts in seconds.
var hookEvents = []struct {
	Name    string
	Timeout int
}{
	{"UserPromptSubmit", 5},
	{"PreToolUse", 5},
	{"PostToolUse", 5},
	{"PermissionRequest", int(permWait.Seconds()) + 15},
	{"Notification", 5},
	{"Stop", 5},
	{"StopFailure", 5},
}

func claudeSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func readSettings(path string) (map[string]json.RawMessage, error) {
	top := map[string]json.RawMessage{}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return top, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return top, nil
	}
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, errors.New("settings.json nėra tinkamas JSON: " + err.Error())
	}
	return top, nil
}

func writeSettings(path string, top map[string]json.RawMessage) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// One-time backup of the user's original file.
	if b, err := os.ReadFile(path); err == nil {
		bak := path + ".pries-island.bak"
		if _, err := os.Stat(bak); errors.Is(err, os.ErrNotExist) {
			os.WriteFile(bak, b, 0o644)
		}
	}
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type hookGroup struct {
	Matcher string           `json:"matcher,omitempty"`
	Hooks   []map[string]any `json:"hooks"`
}

func loadHooks(top map[string]json.RawMessage) (map[string][]json.RawMessage, error) {
	hooks := map[string][]json.RawMessage{}
	if raw, ok := top["hooks"]; ok && len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, errors.New("settings.json \"hooks\" dalis neatpažinta")
		}
	}
	return hooks, nil
}

// isOurGroup reports whether a matcher group contains only our hook.
func stripOurs(raw json.RawMessage) (json.RawMessage, bool, bool) {
	var g map[string]any
	if json.Unmarshal(raw, &g) != nil {
		return raw, false, true
	}
	list, _ := g["hooks"].([]any)
	var keep []any
	found := false
	for _, h := range list {
		if m, ok := h.(map[string]any); ok && str(m["url"]) == hookURL {
			found = true
			continue
		}
		keep = append(keep, h)
	}
	if !found {
		return raw, false, true
	}
	if len(keep) == 0 {
		return nil, true, false
	}
	g["hooks"] = keep
	b, _ := json.Marshal(g)
	return b, true, true
}

// HooksInstalled reports whether our hooks are present in settings.json.
func HooksInstalled() bool {
	path, err := claudeSettingsPath()
	if err != nil {
		return false
	}
	top, err := readSettings(path)
	if err != nil {
		return false
	}
	hooks, err := loadHooks(top)
	if err != nil {
		return false
	}
	for _, groups := range hooks {
		for _, g := range groups {
			if _, found, _ := stripOurs(g); found {
				return true
			}
		}
	}
	return false
}

// SetHooks adds (on=true) or removes our hooks, keeping everything else.
func SetHooks(on bool) error {
	path, err := claudeSettingsPath()
	if err != nil {
		return err
	}
	top, err := readSettings(path)
	if err != nil {
		return err
	}
	hooks, err := loadHooks(top)
	if err != nil {
		return err
	}
	// Remove ours everywhere first (idempotent).
	for ev, groups := range hooks {
		var kept []json.RawMessage
		for _, g := range groups {
			if ng, _, keep := stripOurs(g); keep {
				kept = append(kept, ng)
			}
		}
		if len(kept) == 0 {
			delete(hooks, ev)
		} else {
			hooks[ev] = kept
		}
	}
	if on {
		for _, e := range hookEvents {
			g, _ := json.Marshal(hookGroup{Hooks: []map[string]any{{
				"type": "http", "url": hookURL, "timeout": e.Timeout,
			}}})
			hooks[e.Name] = append(hooks[e.Name], g)
		}
	}
	if len(hooks) == 0 {
		delete(top, "hooks")
	} else {
		b, err := json.Marshal(hooks)
		if err != nil {
			return err
		}
		top["hooks"] = b
	}
	return writeSettings(path, top)
}
