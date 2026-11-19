package main

import (
	"strconv"
	"strings"
)

// Global shortcuts. Windows gives a key combination to only one program, so
// if another app (for example Claude's own quick window on Ctrl+Alt+Space)
// already holds one, the island takes the next free one from the list and
// the settings page shows which one it got. The user can record their own.

type Hotkey struct{ Mods, VK uint32 }

const (
	hkAlt   = 0x1
	hkCtrl  = 0x2
	hkShift = 0x4
	hkWin   = 0x8
)

type hotkeyAction struct {
	ID       int    // RegisterHotKey id (the window gets it in WM_HOTKEY)
	Name     string // key in config / settings page
	Defaults []string
}

var hotkeyActions = []hotkeyAction{
	{1, "hide", []string{"Ctrl+Alt+D", "Ctrl+Alt+H", "Ctrl+Shift+Alt+D"}},
	{2, "shot", []string{"Ctrl+Alt+S", "Ctrl+Shift+Alt+S", "Ctrl+Alt+X"}},
	// Ctrl+Alt+Space is Claude's quick-entry window, so it is not the default
	{3, "cmd", []string{"Ctrl+Alt+K", "Ctrl+Alt+J", "Ctrl+Shift+Space", "Ctrl+Alt+Q"}},
	{4, "color", []string{"Ctrl+Alt+C", "Ctrl+Alt+P", "Ctrl+Shift+Alt+C"}},
}

// ParseHotkey reads "Ctrl+Alt+K", "ctrl + shift + space", "Win+Alt+F5"…
// A shortcut needs at least one of Ctrl, Alt or Win.
func ParseHotkey(s string) (Hotkey, bool) {
	var h Hotkey
	parts := strings.Split(s, "+")
	for i, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		last := i == len(parts)-1
		switch {
		case p == "ctrl" || p == "control" || p == "ctl":
			h.Mods |= hkCtrl
		case p == "alt":
			h.Mods |= hkAlt
		case p == "shift":
			h.Mods |= hkShift
		case p == "win" || p == "windows" || p == "meta":
			h.Mods |= hkWin
		case !last || h.VK != 0:
			return Hotkey{}, false
		case p == "space":
			h.VK = 0x20
		case len(p) == 1 && (p[0] >= 'a' && p[0] <= 'z' || p[0] >= '0' && p[0] <= '9'):
			h.VK = uint32(strings.ToUpper(p)[0])
		case len(p) >= 2 && p[0] == 'f':
			n, err := strconv.Atoi(p[1:])
			if err != nil || n < 1 || n > 12 {
				return Hotkey{}, false
			}
			h.VK = 0x70 + uint32(n-1)
		default:
			return Hotkey{}, false
		}
	}
	if h.VK == 0 || h.Mods&(hkCtrl|hkAlt|hkWin) == 0 {
		return Hotkey{}, false
	}
	return h, true
}

func (h Hotkey) String() string {
	var b []string
	if h.Mods&hkCtrl != 0 {
		b = append(b, "Ctrl")
	}
	if h.Mods&hkShift != 0 {
		b = append(b, "Shift")
	}
	if h.Mods&hkAlt != 0 {
		b = append(b, "Alt")
	}
	if h.Mods&hkWin != 0 {
		b = append(b, "Win")
	}
	switch {
	case h.VK == 0x20:
		b = append(b, "Space")
	case h.VK >= 0x70 && h.VK <= 0x7B:
		b = append(b, "F"+strconv.Itoa(int(h.VK-0x70+1)))
	default:
		b = append(b, string(rune(h.VK)))
	}
	return strings.Join(b, "+")
}

// hotkeyCandidates: the user's own choice first, then the defaults.
func hotkeyCandidates(act hotkeyAction, chosen string) []Hotkey {
	var out []Hotkey
	seen := map[Hotkey]bool{}
	for _, s := range append([]string{chosen}, act.Defaults...) {
		if h, ok := ParseHotkey(s); ok && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

// withKey: "Komandų juosta" + "Ctrl+Alt+K" → "Komandų juosta (Ctrl+Alt+K)".
func withKey(label, key string) string {
	if key == "" {
		return label
	}
	return label + " (" + key + ")"
}
