package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Link is a quick-launch button: a website, app URI or program path.
type Link struct {
	Name   string
	Target string
}

const defaultLinks = `# Greitieji mygtukai Islet salelėje (iki 8).
# Formatas:  Pavadinimas | adresas arba programa
# Pvz.:      Mokykla | https://www.tamo.lt
YouTube | https://www.youtube.com
ChatGPT | https://chatgpt.com
Claude | https://claude.ai
Spotify | spotify:
GitHub | https://github.com
Gmail | https://mail.google.com
Vertėjas | https://translate.google.com
Discord | https://discord.com/app
`

func linksPath() string { return filepath.Join(filepath.Dir(configPath()), "nuorodos.txt") }

func ensureLinks() string {
	p := linksPath()
	if _, err := os.Stat(p); os.IsNotExist(err) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(strings.ReplaceAll(defaultLinks, "\n", "\r\n")), 0o644)
	}
	return p
}

func ParseLinks(s string) []Link {
	var out []Link
	for _, line := range strings.Split(strings.TrimPrefix(s, "\uFEFF"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, target, ok := strings.Cut(line, "|")
		name, target = strings.TrimSpace(name), strings.TrimSpace(target)
		if !ok || name == "" || target == "" {
			continue
		}
		out = append(out, Link{name, target})
		if len(out) == 8 {
			break
		}
	}
	return out
}

func readLinks() ([]Link, time.Time) {
	p := ensureLinks()
	st, err := os.Stat(p)
	if err != nil {
		return ParseLinks(defaultLinks), time.Time{}
	}
	b, _ := os.ReadFile(p)
	return ParseLinks(string(b)), st.ModTime()
}

// linkColor picks a brand-ish color for well-known names, else a stable one.
func linkColor(name string) [3]uint8 {
	switch strings.ToLower(name) {
	case "youtube":
		return [3]uint8{0xff, 0x00, 0x33}
	case "chatgpt":
		return [3]uint8{0x10, 0xa3, 0x7f}
	case "claude":
		return [3]uint8{0xd9, 0x77, 0x57}
	case "spotify":
		return [3]uint8{0x1d, 0xb9, 0x54}
	case "github":
		return [3]uint8{0x6e, 0x76, 0x81}
	case "gmail":
		return [3]uint8{0xea, 0x43, 0x35}
	case "discord":
		return [3]uint8{0x58, 0x65, 0xf2}
	}
	pal := [][3]uint8{{0x0a, 0x84, 0xff}, {0xbf, 0x5a, 0xf2}, {0xff, 0x9f, 0x0a}, {0x30, 0xd1, 0x58}, {0xff, 0x37, 0x5f}, {0x64, 0xd2, 0xff}}
	var h uint32 = 2166136261
	for _, b := range []byte(strings.ToLower(name)) {
		h = (h ^ uint32(b)) * 16777619
	}
	return pal[h%uint32(len(pal))]
}
