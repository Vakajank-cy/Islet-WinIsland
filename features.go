package main

import (
	"encoding/json"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // world clocks work without Windows time zone names
	"unicode"
)

func dataPath(name string) string { return filepath.Join(filepath.Dir(configPath()), name) }

func loadJSON(path string, v any) {
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, v)
	}
}

func saveJSON(path string, v any) {
	os.MkdirAll(filepath.Dir(path), 0o755)
	b, _ := json.MarshalIndent(v, "", "  ")
	os.WriteFile(path, b, 0o644)
}

// ---------- to-do list ----------

type Todo struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type Todos struct {
	mu    sync.Mutex
	Items []Todo
	path  string
}

func LoadTodos(path string) *Todos {
	t := &Todos{path: path}
	loadJSON(path, &t.Items)
	return t
}

func (t *Todos) save() {
	if t.path != "" {
		saveJSON(t.path, t.Items)
	}
}

func (t *Todos) Add(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	id := 1
	for _, it := range t.Items {
		if it.ID >= id {
			id = it.ID + 1
		}
	}
	t.Items = append(t.Items, Todo{ID: id, Text: text})
	t.save()
}

func (t *Todos) Toggle(id int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.Items {
		if t.Items[i].ID == id {
			t.Items[i].Done = !t.Items[i].Done
		}
	}
	t.save()
}

func (t *Todos) Delete(id int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, it := range t.Items {
		if it.ID == id {
			t.Items = append(t.Items[:i], t.Items[i+1:]...)
			break
		}
	}
	t.save()
}

func (t *Todos) ClearDone() {
	t.mu.Lock()
	defer t.mu.Unlock()
	var keep []Todo
	for _, it := range t.Items {
		if !it.Done {
			keep = append(keep, it)
		}
	}
	t.Items = keep
	t.save()
}

func (t *Todos) List() []Todo {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]Todo(nil), t.Items...)
	sort.SliceStable(out, func(i, j int) bool { return !out[i].Done && out[j].Done })
	return out
}

// ---------- favourite songs ----------

type Fav struct {
	Artist string    `json:"artist"`
	Title  string    `json:"title"`
	Added  time.Time `json:"added"`
}

type Favorites struct {
	mu    sync.Mutex
	Items []Fav
	path  string
}

func LoadFavorites(path string) *Favorites {
	f := &Favorites{path: path}
	loadJSON(path, &f.Items)
	return f
}

func favKey(artist, title string) string { return strings.ToLower(artist + "\x00" + title) }

func (f *Favorites) Has(artist, title string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := favKey(artist, title)
	for _, it := range f.Items {
		if favKey(it.Artist, it.Title) == k {
			return true
		}
	}
	return false
}

// Toggle adds or removes a song; returns true if it is now a favourite.
func (f *Favorites) Toggle(artist, title string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := favKey(artist, title)
	for i, it := range f.Items {
		if favKey(it.Artist, it.Title) == k {
			f.Items = append(f.Items[:i], f.Items[i+1:]...)
			saveJSON(f.path, f.Items)
			return false
		}
	}
	f.Items = append([]Fav{{artist, title, time.Now()}}, f.Items...)
	if f.path != "" {
		saveJSON(f.path, f.Items)
	}
	return true
}

func (f *Favorites) DeleteAt(i int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= 0 && i < len(f.Items) {
		f.Items = append(f.Items[:i], f.Items[i+1:]...)
		if f.path != "" {
			saveJSON(f.path, f.Items)
		}
	}
}

func (f *Favorites) List() []Fav {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Fav(nil), f.Items...)
}

// ---------- focus statistics (seconds of "work" per day) ----------

type FocusStats struct {
	Days  map[string]float64 `json:"days"`
	path  string
	dirty time.Time
}

func LoadFocus(path string) *FocusStats {
	f := &FocusStats{Days: map[string]float64{}, path: path}
	loadJSON(path, f)
	if f.Days == nil {
		f.Days = map[string]float64{}
	}
	return f
}

func (f *FocusStats) Add(now time.Time, secs float64) {
	f.Days[now.Format("2006-01-02")] += secs
	if now.Sub(f.dirty) > time.Minute {
		f.dirty = now
		if f.path != "" {
			saveJSON(f.path, f)
		}
	}
}

func (f *FocusStats) Day(t time.Time) float64 { return f.Days[t.Format("2006-01-02")] }

// Week returns the last 7 days, oldest first.
func (f *FocusStats) Week(now time.Time) []float64 {
	out := make([]float64, 7)
	for i := 0; i < 7; i++ {
		out[i] = f.Day(now.AddDate(0, 0, i-6))
	}
	return out
}

func fmtHM(secs float64) string {
	m := int(secs / 60)
	if m < 60 {
		return fmt.Sprintf(L("%d min"), m)
	}
	return fmt.Sprintf(L("%d h %02d min"), m/60, m%60)
}

// ---------- birthdays ----------

type Birthday struct {
	Name  string `json:"name"`
	Month int    `json:"month"`
	Day   int    `json:"day"`
}

// ParseBirthdays reads lines like "Tomas | 03-15" or "Tomas 15.03".
func ParseBirthdays(s string) []Birthday {
	var out []Birthday
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, date, ok := strings.Cut(line, "|")
		if !ok {
			i := strings.LastIndex(line, " ")
			if i < 0 {
				continue
			}
			name, date = line[:i], line[i+1:]
		}
		name, date = strings.TrimSpace(name), strings.TrimSpace(date)
		var a, b int
		var sep string
		switch {
		case strings.Contains(date, "-"):
			sep = "-"
		case strings.Contains(date, "."):
			sep = "."
		case strings.Contains(date, "/"):
			sep = "/"
		default:
			continue
		}
		parts := strings.Split(date, sep)
		if len(parts) < 2 {
			continue
		}
		// accept YYYY-MM-DD, MM-DD and DD.MM
		if len(parts) == 3 && len(parts[0]) == 4 {
			parts = parts[1:]
		}
		a, _ = strconv.Atoi(parts[0])
		b, _ = strconv.Atoi(parts[1])
		m, d := a, b
		if sep == "." || sep == "/" {
			m, d = b, a
		}
		if m < 1 || m > 12 || d < 1 || d > 31 || name == "" {
			continue
		}
		out = append(out, Birthday{name, m, d})
	}
	return out
}

func FormatBirthdays(b []Birthday) string {
	var s []string
	for _, x := range b {
		s = append(s, fmt.Sprintf("%s | %02d-%02d", x.Name, x.Month, x.Day))
	}
	return strings.Join(s, "\n")
}

// nextDate returns the next occurrence (today counts).
func (b Birthday) nextDate(now time.Time) time.Time {
	y := now.Year()
	d := time.Date(y, time.Month(b.Month), b.Day, 0, 0, 0, 0, now.Location())
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if d.Before(today) {
		d = d.AddDate(1, 0, 0)
	}
	return d
}

// ---------- world clocks ----------

var worldCities = []struct{ Name, Zone string }{
	{"Londonas", "Europe/London"}, {"Niujorkas", "America/New_York"}, {"Tokijas", "Asia/Tokyo"},
	{"Los Andželas", "America/Los_Angeles"}, {"Čikaga", "America/Chicago"}, {"Paryžius", "Europe/Paris"},
	{"Berlynas", "Europe/Berlin"}, {"Varšuva", "Europe/Warsaw"}, {"Ryga", "Europe/Riga"}, {"Kyjivas", "Europe/Kyiv"},
	{"Dubajus", "Asia/Dubai"}, {"Delis", "Asia/Kolkata"}, {"Pekinas", "Asia/Shanghai"}, {"Sidnėjus", "Australia/Sydney"},
	{"San Paulas", "America/Sao_Paulo"}, {"Reikjavikas", "Atlantic/Reykjavik"},
}

type ClockView struct{ Name, Time, Diff string }

func worldClocks(names []string, now time.Time) []ClockView {
	var out []ClockView
	for _, n := range names {
		for _, c := range worldCities {
			if c.Name != n {
				continue
			}
			loc, err := time.LoadLocation(c.Zone)
			if err != nil {
				continue
			}
			t := now.In(loc)
			_, off1 := now.Zone()
			_, off2 := t.Zone()
			d := (off2 - off1) / 3600
			diff := fmt.Sprintf(L("%+d h"), d)
			if d == 0 {
				diff = "±0 h"
			}
			out = append(out, ClockView{L(c.Name), t.Format("15:04"), diff})
		}
	}
	return out
}

// ---------- mood of the robot ----------

const (
	moodNormal = iota
	moodSleep
	moodMusic
	moodTired
)

// ---------- album colour ----------

// coverColor finds a vivid average colour of the album art.
func coverColor(img image.Image) ([3]uint8, bool) {
	if img == nil {
		return [3]uint8{}, false
	}
	b := img.Bounds()
	var sr, sg, sb, sw float64
	step := max(1, b.Dx()/24)
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			r, g, bl, a := img.At(x, y).RGBA()
			if a < 0x8000 {
				continue
			}
			fr, fg, fb := float64(r)/65535, float64(g)/65535, float64(bl)/65535
			mx := math.Max(fr, math.Max(fg, fb))
			mn := math.Min(fr, math.Min(fg, fb))
			sat := 0.0
			if mx > 0 {
				sat = (mx - mn) / mx
			}
			w := sat*sat*mx + 0.02 // prefer saturated, bright pixels
			sr += fr * w
			sg += fg * w
			sb += fb * w
			sw += w
		}
	}
	if sw == 0 {
		return [3]uint8{}, false
	}
	r, g, bl := sr/sw, sg/sw, sb/sw
	// brighten so the glow is visible
	mx := math.Max(r, math.Max(g, bl))
	if mx > 0 {
		k := 0.95 / mx
		r, g, bl = r*k, g*k, bl*k
	}
	return [3]uint8{uint8(r * 255), uint8(g * 255), uint8(bl * 255)}, true
}

// ---------- mini game ----------

type Game struct {
	On, Over   bool
	Y, VY      float64 // robot height above ground
	Obstacles  []float64
	Speed      float64
	Score      float64
	Best       int
	spawnTimer float64
	seed       uint32
}

func (g *Game) Start() {
	best := g.Best
	*g = Game{On: true, Speed: 170, Best: best, seed: uint32(time.Now().UnixNano()), spawnTimer: 1.2}
}

func (g *Game) rnd() float64 {
	g.seed = g.seed*1664525 + 1013904223
	return float64(g.seed>>8) / float64(1<<24)
}

func (g *Game) Jump() {
	if !g.On {
		return
	}
	if g.Over {
		g.Start()
		return
	}
	if g.Y <= 0.5 {
		g.VY = 330
	}
}

// Step advances the game; width is the play field width in logical px.
func (g *Game) Step(dt, width float64) {
	if !g.On || g.Over {
		return
	}
	if dt > 0.05 {
		dt = 0.05
	}
	g.VY -= 1000 * dt
	g.Y = math.Max(0, g.Y+g.VY*dt)
	if g.Y == 0 && g.VY < 0 {
		g.VY = 0
	}
	g.Speed += 6 * dt
	g.Score += dt * 10
	g.spawnTimer -= dt
	if g.spawnTimer <= 0 {
		g.Obstacles = append(g.Obstacles, width+20)
		g.spawnTimer = 0.9 + g.rnd()*1.1
	}
	keep := g.Obstacles[:0]
	for _, x := range g.Obstacles {
		x -= g.Speed * dt
		if x > -30 {
			keep = append(keep, x)
		}
		// robot spans x 40..62, obstacle 14 wide, 18 tall
		if x < 60 && x+14 > 42 && g.Y < 18 {
			g.Over = true
			if int(g.Score) > g.Best {
				g.Best = int(g.Score)
			}
		}
	}
	g.Obstacles = keep
}

// ---------- karaoke fill ----------

// lineProgress: how far through line i the song is (0..1).
func lineProgress(l *Lyrics, i int, pos float64) float64 {
	if l == nil || i < 0 || i >= len(l.Lines) {
		return 0
	}
	start := l.Lines[i].T
	end := start + 4
	if i+1 < len(l.Lines) {
		end = l.Lines[i+1].T
	}
	// singing usually ends a bit before the next line
	dur := math.Max(0.5, (end-start)*0.85)
	return clamp01((pos - start) / dur)
}

// ---------- misc ----------

func isMostlyForeign(s string) bool {
	// heuristic for "offer a translation": mostly letters, and no Lithuanian letters
	s = strings.TrimSpace(s)
	if len([]rune(s)) < 12 || len(s) > 2000 || strings.Contains(s, "://") {
		return false
	}
	letters, lt := 0, 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
		}
		if strings.ContainsRune("ąčęėįšųūžĄČĘĖĮŠŲŪŽ", r) {
			lt++
		}
	}
	return letters > len([]rune(s))/2 && lt == 0 && strings.Count(s, " ") >= 2
}
