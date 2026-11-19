package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---------- Pomodoro focus timer ----------

var timerPresets = [][2]int{{25, 5}, {50, 10}, {15, 3}}

type Pomodoro struct {
	Preset  int
	Phase   int // 0 = darbas, 1 = poilsis
	Running bool
	EndsAt  time.Time
	Left    time.Duration
	Rounds  int // finished work rounds
}

func NewPomodoro(preset int) *Pomodoro {
	if preset < 0 || preset >= len(timerPresets) {
		preset = 0
	}
	p := &Pomodoro{Preset: preset}
	p.Left = p.phaseLen()
	return p
}

func (p *Pomodoro) phaseLen() time.Duration {
	return time.Duration(timerPresets[p.Preset][p.Phase]) * time.Minute
}

func (p *Pomodoro) Remaining(now time.Time) time.Duration {
	if p.Running {
		if d := p.EndsAt.Sub(now); d > 0 {
			return d
		}
		return 0
	}
	return p.Left
}

// Active means the timer was started (running or paused mid-way).
func (p *Pomodoro) Active() bool { return p.Running || p.Left != p.phaseLen() || p.Phase != 0 }

func (p *Pomodoro) Toggle(now time.Time) {
	if p.Running {
		p.Left = p.Remaining(now)
		p.Running = false
	} else {
		p.EndsAt = now.Add(p.Left)
		p.Running = true
	}
}

func (p *Pomodoro) Reset() {
	p.Running, p.Phase, p.Rounds = false, 0, 0
	p.Left = p.phaseLen()
}

func (p *Pomodoro) NextPreset() {
	p.Preset = (p.Preset + 1) % len(timerPresets)
	p.Reset()
}

// Tick switches phase when time is up. Returns a message if it did.
func (p *Pomodoro) Tick(now time.Time) (string, bool) {
	if !p.Running || now.Before(p.EndsAt) {
		return "", false
	}
	if p.Phase == 0 {
		p.Rounds++
		p.Phase = 1
		p.EndsAt = now.Add(p.phaseLen())
		return fmt.Sprintf(L("Laikas pailsėti! %d min poilsio"), timerPresets[p.Preset][1]), true
	}
	p.Phase = 0
	p.EndsAt = now.Add(p.phaseLen())
	return L("Poilsis baigtas, grįžtam prie darbo"), true
}

func (p *Pomodoro) Progress(now time.Time) float64 {
	total := p.phaseLen().Seconds()
	return clamp01(1 - p.Remaining(now).Seconds()/total)
}

func fmtDur(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

func (p *Pomodoro) PresetLabel() string {
	return fmt.Sprintf("%d/%d", timerPresets[p.Preset][0], timerPresets[p.Preset][1])
}

// ---------- clipboard history (kept in memory only, never saved) ----------

const clipMax = 10

type ClipHistory struct{ Items []string }

func (c *ClipHistory) Add(s string) bool {
	if strings.TrimSpace(s) == "" || len(s) > 200000 {
		return false
	}
	for i, it := range c.Items {
		if it == s {
			if i == 0 {
				return false
			}
			c.Items = append(c.Items[:i], c.Items[i+1:]...)
			break
		}
	}
	c.Items = append([]string{s}, c.Items...)
	if len(c.Items) > clipMax {
		c.Items = c.Items[:clipMax]
	}
	return true
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300])
	}
	return s
}

// ---------- quick note (a text file the user can open in Notepad) ----------

func notePath() string {
	return filepath.Join(filepath.Dir(configPath()), "uzrasas.txt")
}

func ensureNote() string {
	p := notePath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		os.WriteFile(p, []byte{}, 0o644)
	}
	return p
}

func readNote() (string, time.Time) {
	p := notePath()
	st, err := os.Stat(p)
	if err != nil {
		return "", time.Time{}
	}
	b, _ := os.ReadFile(p)
	s := strings.TrimPrefix(string(b), "\uFEFF")
	return strings.ReplaceAll(s, "\r\n", "\n"), st.ModTime()
}

func appendNote(text string, now time.Time) error {
	p := ensureNote()
	cur, _ := readNote()
	sep := ""
	if cur != "" && !strings.HasSuffix(cur, "\n") {
		sep = "\r\n"
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	text = strings.ReplaceAll(strings.TrimSpace(text), "\n", "\r\n")
	_, err = f.WriteString(sep + "[" + now.Format("15:04") + "] " + text + "\r\n")
	return err
}

// ---------- robot looks ----------

var robotColors = []struct {
	Name    string
	R, G, B uint8
}{
	{"Pagal būseną", 0, 0, 0},
	{"Balta", 0xf2, 0xf2, 0xf7},
	{"Rožinė", 0xff, 0x7e, 0xb6},
	{"Violetinė", 0xbf, 0x5a, 0xf2},
	{"Mėtinė", 0x66, 0xd4, 0xcf},
	{"Raudona", 0xff, 0x45, 0x3a},
	{"Žydra", 0x64, 0xd2, 0xff},
}

var robotFaces = []string{"Įprastas", "Apvalios akys", "Akiniai", "Katinukas"}

// Flash is a short message shown on the island (e.g. timer finished).
type Flash struct {
	Text   string
	Color  [3]uint8
	Until  time.Time
	Action uintptr // window to bring to front when clicked (0 = none)
	Open   string  // or a file / folder / URL to open
}
