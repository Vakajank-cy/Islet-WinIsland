package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Usage is the current Claude 5-hour window, computed from Claude Code's
// local session files (~/.claude/projects/**/*.jsonl). Nothing is sent anywhere.
type Usage struct {
	Has      bool
	Start    time.Time
	End      time.Time
	Tokens   int64 // input + output tokens in this window
	Requests int
}

const usageWindow = 5 * time.Hour

func claudeProjectDirs() []string {
	var dirs []string
	if env := os.Getenv("CLAUDE_CONFIG_DIR"); env != "" {
		for _, d := range strings.Split(env, ",") {
			dirs = append(dirs, filepath.Join(strings.TrimSpace(d), "projects"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".claude", "projects"),
			filepath.Join(home, ".config", "claude", "projects"))
	}
	return dirs
}

type usageEntry struct {
	t      time.Time
	tokens int64
}

type jsonlLine struct {
	Timestamp string `json:"timestamp"`
	RequestID string `json:"requestId"`
	Message   struct {
		ID    string `json:"id"`
		Usage *struct {
			Input  int64 `json:"input_tokens"`
			Output int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// ScanUsage reads recent session files and returns the active 5h window.
func ScanUsage(dirs []string, now time.Time) Usage {
	since := now.Add(-2 * usageWindow)
	seen := map[string]bool{}
	var entries []usageEntry
	for _, root := range dirs {
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			if info, err := d.Info(); err != nil || info.ModTime().Before(since) {
				return nil
			}
			f, err := os.Open(path)
			if err != nil {
				return nil
			}
			defer f.Close()
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 64*1024), 32*1024*1024)
			for sc.Scan() {
				line := sc.Bytes()
				if !strings.Contains(string(line), `"usage"`) {
					continue
				}
				var l jsonlLine
				if json.Unmarshal(line, &l) != nil || l.Message.Usage == nil {
					continue
				}
				t, err := time.Parse(time.RFC3339Nano, l.Timestamp)
				if err != nil || t.Before(since) || t.After(now.Add(time.Minute)) {
					continue
				}
				if key := l.Message.ID + "|" + l.RequestID; key != "|" {
					if seen[key] {
						continue
					}
					seen[key] = true
				}
				entries = append(entries, usageEntry{t, l.Message.Usage.Input + l.Message.Usage.Output})
			}
			return nil
		})
	}
	return activeWindow(entries, now)
}

// activeWindow groups entries into 5h windows (each starts at the hour of
// its first request) and returns the one that is still open.
func activeWindow(entries []usageEntry, now time.Time) Usage {
	sort.Slice(entries, func(i, j int) bool { return entries[i].t.Before(entries[j].t) })
	var u Usage
	var last time.Time
	for _, e := range entries {
		if !u.Has || !e.t.Before(u.End) || e.t.Sub(last) >= usageWindow {
			start := e.t.Truncate(time.Hour)
			u = Usage{Has: true, Start: start, End: start.Add(usageWindow)}
		}
		u.Tokens += e.tokens
		u.Requests++
		last = e.t
	}
	if !u.Has || !now.Before(u.End) {
		return Usage{}
	}
	return u
}

func (u Usage) Progress(now time.Time) float64 {
	if !u.Has {
		return 0
	}
	return clamp01(now.Sub(u.Start).Seconds() / usageWindow.Seconds())
}

func (u Usage) LeftText(now time.Time) string {
	d := u.End.Sub(now)
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf(L("%d h %02d min"), h, m)
	}
	return fmt.Sprintf(L("%d min"), m)
}

// fmtTokens: 1234567 -> "1,2 mln.", 45300 -> "45,3 tūkst."
func fmtTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return strings.Replace(fmt.Sprintf(L("%.1f mln."), float64(n)/1e6), ".", ",", 1)
	case n >= 1_000:
		return strings.Replace(fmt.Sprintf(L("%.1f tūkst."), float64(n)/1e3), ".", ",", 1)
	}
	return fmt.Sprint(n)
}
