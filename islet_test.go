package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func snapshot(t *testing.T, r *Renderer, name string, v View) {
	tw, th := r.Target(v)
	v.W, v.H = tw, th
	img, _ := r.Draw(v)
	// put on a light "desktop" so transparency is visible
	bg := image.NewRGBA(image.Rect(0, 0, int(canvasW*r.S), int(canvasH*r.S)))
	draw.Draw(bg, bg.Bounds(), &image.Uniform{color.RGBA{0xdd, 0xe3, 0xea, 255}}, image.Point{}, draw.Src)
	draw.Draw(bg, img.Bounds(), img, img.Bounds().Min, draw.Over)
	os.MkdirAll("preview", 0o755)
	f, _ := os.Create(filepath.Join("preview", name+".png"))
	png.Encode(f, bg)
	f.Close()
}

func TestRenderStates(t *testing.T) {
	r := NewRenderer(1.5)
	now := time.Date(2026, 11, 19, 19, 24, 0, 0, time.Local)
	base := View{Now: now, T: 1.0, HooksOn: true, Timer: TimerView{Text: "25:00", Preset: "25/5"}}
	snap := func(m Mode, text, detail string) Snapshot {
		return Snapshot{Mode: m, Text: text, Detail: detail, Project: "portfolio", ModeSince: now.Add(-500 * time.Millisecond)}
	}
	media := &Media{App: "Spotify.exe", Title: "Vakaro Traukinys", Artist: "Testo Grupė", Status: 4, Pos: 41, End: 214, Updated: now.UnixMilli()}
	lyr := &Lyrics{State: lyrSynced, Lines: ParseLRC("[00:30.00]Pirma sugalvota eilutė\n[00:38.50]Čia groja bandomoji daina\n[00:44.00]Trečia išgalvota eilutė\n[00:50.00]Ketvirta eilutė")}
	usage := Usage{Has: true, Start: now.Add(-2 * time.Hour), End: now.Add(3*time.Hour - 47*time.Minute), Tokens: 1234567, Requests: 34}
	sys := SysInfo{Has: true, Battery: 54, Charging: true, CPU: 0.23, RAM: 0.61, RAMUsed: 9.6, RAMTotal: 16}
	base.Usage, base.Sys = usage, sys
	v := base
	v.Snap = snap(ModeIdle, "", "")
	snapshot(t, r, "01-idle", v)
	v.Hover = true
	v.Media, v.Lyrics = media, lyr
	v.Timer = TimerView{Active: true, Running: true, Text: "18:42", Preset: "25/5", Phase: 0, Rounds: 2, Progress: 0.25}
	v.HoverBtn = "tab:1"
	snapshot(t, r, "02-tab-home", v)
	v.Tab = tabMusic
	v.HoverBtn = "play"
	snapshot(t, r, "02b-tab-music", v)
	v.Tab = tabSystem
	v.HoverBtn = ""
	snapshot(t, r, "02c-tab-system", v)
	v.Tab = tabTimer
	v.HoverBtn = "timer:toggle"
	snapshot(t, r, "03-tab-timer", v)
	v.Tab = tabClips
	v.HoverBtn = "clip:1"
	v.Clips = []string{"git push origin main", "https://github.com/vakaris/dynamic-island", "Labas, kaip sekasi?\nAntra eilute", "npm run dev"}
	snapshot(t, r, "04-tab-clips", v)
	v.Tab = tabNote
	v.HoverBtn = ""
	v.Note = "Nupirkti pieno\nPabaigti Dynamic Island laikmatį ir patikrinti pranešimus Windows sistemoje\nParašyti draugui"
	snapshot(t, r, "05-tab-note", v)
	v.Tab = tabRemind
	v.HoverBtn = ""
	v.Reminders = []Reminder{{1, "Treniruotė", now.Add(40 * time.Minute)}, {2, "Paskambinti močiutei", now.Add(3 * time.Hour)}, {3, "Išnešti šiukšles", now.Add(20 * time.Hour)}}
	snapshot(t, r, "06-tab-remind", v)
	v.Tab = tabShelf
	v.Shelf = []string{`C:\Users\v\Desktop\ataskaita.docx`, `C:\Users\v\Downloads\nuotrauka.jpg`, `C:\Users\v\Music\daina.mp3`, `C:\Users\v\Documents\projektas`}
	v.HoverBtn = "shelf:open:1"
	snapshot(t, r, "06-tab-shelf", v)
	v.Tab = tabSystem
	v.Net = NetInfo{Has: true, Online: true, Down: 2.4 * (1 << 20), Up: 120 * 1024}
	snapshot(t, r, "06-tab-system2", v)
	v = base
	v.Snap = snap(ModeIdle, "", "")
	v.Media, v.Lyrics = media, lyr
	snapshot(t, r, "06b-music-collapsed", v)
	v.Tab = tabMusic
	v.Hover = true
	v.Lyrics = &Lyrics{State: lyrNotFound}
	snapshot(t, r, "06c-music-nolyrics", v)
	v = base
	v.Snap = snap(ModeIdle, "", "")
	v.Sys = SysInfo{Has: true, Battery: 14}
	snapshot(t, r, "06d-idle-lowbat", v)
	wx := Weather{Has: true, City: "Vilnius", Temp: 2.6, Code: 2, IsDay: true}
	for i, c := range []int{2, 63, 3, 73, 0, 95} {
		wx.Days = append(wx.Days, WeatherDay{now.AddDate(0, 0, i), c, float64(4 - i), float64(-2 - i)})
	}
	v = base
	v.Snap = snap(ModeIdle, "", "")
	v.Weather = wx
	snapshot(t, r, "06e-idle-weather", v)
	v.Hover, v.Tab = true, tabWeather
	snapshot(t, r, "06f-tab-weather", v)
	v.Tab = tabLinks
	v.Links = ParseLinks(defaultLinks)
	v.HoverBtn = "link:2"
	snapshot(t, r, "06g-tab-links", v)
	v = base
	v.Snap = snap(ModeIdle, "", "")
	v.Volume = &VolumeOSD{Level: 0.45, Until: now.Add(time.Second)}
	snapshot(t, r, "06h-volume", v)
	v.Volume = nil
	v.Shot = &ShotView{Until: now.Add(time.Second)}
	snapshot(t, r, "06i-shot", v)
	v.Shot = nil
	v.AI = AIState{Name: "ChatGPT", Generating: true}
	snapshot(t, r, "06j-ai", v)
	v = base
	v.Snap = snap(ModeIdle, "", "")
	v.Media, v.Lyrics = media, lyr
	v.Hover, v.Tab, v.HoverBtn = true, tabMusic, "seekbar"
	snapshot(t, r, "06k-music-seek", v)
	est := *media
	est.End, est.Estimated = 0, true
	v.Media = &est
	l2 := *lyr
	l2.Trans = []string{"First made-up line", "This is a test song playing", "Third invented line", "Fourth line"}
	v.Lyrics, v.Translate, v.HoverBtn = &l2, true, ""
	snapshot(t, r, "06l-music-trans", v)
	v.X.Fav = true
	v.X.SleepLeft = "28 min"
	v.X.Peaks = [5]float64{0.3, 0.7, 0.5, 0.9, 0.4}
	v.X.CoverColor = &[3]uint8{0xff, 0x6b, 0x3d}
	snapshot(t, r, "07a-music-fav", v)
	v.Hover = false
	snapshot(t, r, "07b-music-collapsed-glow", v)
	v.Hover = true
	v.X = Extra{}
	v.Media = nil
	v.Tab = tabRemind
	v.X.Plan = []PlanRow{{Kind: "event", When: "14:00", Text: "Matematikos pamoka", Extra: "šiandien"}, {Kind: "remind", ID: 1, When: "19:30", Text: "Treniruotė", Extra: "po 40 min"},
		{Kind: "todo", ID: 2, Text: "Nupirkti pieno", Done: true}, {Kind: "todo", ID: 3, Text: "Padaryti namų darbus"}}
	snapshot(t, r, "07c-tab-plan", v)
	v.Tab = tabTimer
	v.X.Focus = FocusView{Today: 4500, Week: []float64{1200, 3000, 0, 5400, 2400, 1800, 4500}}
	snapshot(t, r, "07d-tab-timer-stats", v)
	v.Tab = tabWeather
	v.Weather = wx
	v.X.Clocks = worldClocks([]string{"Londonas", "Niujorkas", "Tokijas"}, now)
	snapshot(t, r, "07e-tab-weather-clocks", v)
	v.Tab = tabSystem
	v.Snap.Sessions = []Session{{Project: "dynamic-island", Text: "Redaguoja", Detail: "main.go", Mode: ModeWorking}, {Project: "svetaine", Text: "Baigta", Mode: ModeDone}}
	v.X.Mic, v.X.Bright = "Discord", 0.6
	v.X.BT = []BTView{{"AirPods Pro", 72}}
	v.X.Unread = []UnreadView{{"Discord", 3}}
	snapshot(t, r, "07f-tab-system-rows", v)
	v.Hover = false
	v.X.DND = true
	snapshot(t, r, "07g-collapsed-indicators", v)
	v.X = Extra{Cmd: &CmdView{Input: "10 eur usd", Results: EvalCommand("2+2*5", nil)}}
	v.X.Caret = true
	snapshot(t, r, "07h-cmd", v)
	g := &Game{}
	g.Start()
	g.Obstacles = []float64{200, 330}
	g.Score = 42
	v.X = Extra{Game: g}
	snapshot(t, r, "07i-game", v)
	v.X = Extra{Card: &Card{Title: "Dienos faktas", Body: todaysFact(now), Color: [3]uint8{0x0a, 0x84, 0xff}, Buttons: []CardBtn{{"card:close", "Gerai"}}, Until: now.Add(time.Minute)}}
	snapshot(t, r, "07j-card", v)
	v.X = Extra{Card: &Card{Title: "#3A7BD5", Body: "RGB 58, 123, 213 · nukopijuota", Color: [3]uint8{0x3a, 0x7b, 0xd5}, Swatch: &[3]uint8{0x3a, 0x7b, 0xd5}, Buttons: []CardBtn{{"pick:hex", "HEX"}, {"pick:rgb", "RGB"}}, Until: now.Add(time.Minute)}}
	snapshot(t, r, "07k-card-color", v)
	v.X = Extra{Picker: &PickerView{Color: [3]uint8{0xff, 0x45, 0x3a}}}
	snapshot(t, r, "07l-picker", v)
	v.X = Extra{Mood: moodSleep}
	v.Media = nil
	snapshot(t, r, "07m-sleep", v)
	v.X = Extra{}
	v.Volume = &VolumeOSD{Level: 0.65, Bright: true, Until: now.Add(time.Second)}
	snapshot(t, r, "07o-bright", v)
	v.Volume = nil
	setLang("en")
	v.X = Extra{}
	v.Hover, v.Tab = true, tabHome
	snapshot(t, r, "07n-english", v)
	setLang("lt")
	setTheme(true)
	v.Tab = tabHome
	snapshot(t, r, "06m-light-home", v)
	v.Hover = false
	snapshot(t, r, "06n-light-collapsed", v)
	setTheme(false)
	v = base
	v.Snap = snap(ModeIdle, "", "")
	v.Timer = TimerView{Active: true, Running: true, Text: "18:42", Phase: 0, Progress: 0.3}
	v.RobotColor, v.Face = 2, 3
	snapshot(t, r, "07-timer-collapsed", v)
	v = base
	v.Snap = snap(ModeIdle, "", "")
	v.Flash = &Flash{Text: "Laikas pailsėti! 5 min poilsio", Color: [3]uint8{0x30, 0xd1, 0x58}, Until: now.Add(time.Minute)}
	snapshot(t, r, "08-flash", v)
	v = base
	v.Snap = snap(ModeWorking, "Redaguoja", "index.html")
	v.Face = 2
	snapshot(t, r, "09-working", v)
	v = base
	v.Snap = snap(ModeDone, "Baigta", "")
	snapshot(t, r, "10-done", v)
	v = base
	v.Snap = snap(ModePermission, "", "")
	v.Snap.Perm = &PermReq{Tool: "Bash", Detail: "git push origin main", Project: "portfolio", Created: now.Add(-16 * time.Second)}
	v.Snap.PermCount = 2
	v.HoverBtn = "allow"
	snapshot(t, r, "11-permission", v)
	v = base
	v.Snap = snap(ModeIdle, "", "")
	v.W, v.H = 300, 80
	v.Hover = true
	if img, _ := r.Draw(v); img.Bounds().Dx() == 0 {
		t.Fatal("empty frame")
	}
	ic := RenderIcon(256)
	f, _ := os.Create("preview/icon.png")
	png.Encode(f, ic)
	f.Close()
}

func TestPomodoro(t *testing.T) {
	now := time.Now()
	p := NewPomodoro(2) // 15/3
	p.Toggle(now)
	if msg, ok := p.Tick(now.Add(15*time.Minute + time.Second)); !ok || p.Phase != 1 || !strings.Contains(msg, "3 min") {
		t.Fatalf("phase switch failed %v %d %q", ok, p.Phase, msg)
	}
	p.Toggle(now.Add(16 * time.Minute)) // pause
	if p.Running {
		t.Fatal("should pause")
	}
	p.NextPreset()
	if p.PresetLabel() != "25/5" || p.Active() {
		t.Fatal("preset/reset wrong")
	}
	var c ClipHistory
	for i := 0; i < 15; i++ {
		c.Add(fmt.Sprint(i))
	}
	c.Add("3")
	if len(c.Items) != clipMax || c.Items[0] != "3" {
		t.Fatalf("clip history wrong %v", c.Items)
	}
}

func TestLyricsAndUsage(t *testing.T) {
	ls := ParseLRC("[ar:x]\n[00:12.50]Labas\n[01:02.00][01:30.00]Kartojasi\n[00:05]Pradžia")
	if len(ls) != 4 || ls[0].T != 5 || ls[1].Text != "Labas" || ls[3].T != 90 {
		t.Fatalf("bad lrc %+v", ls)
	}
	l := &Lyrics{Lines: ls}
	if l.LineAt(3) != -1 || l.LineAt(12.6) != 1 || l.LineAt(100) != 3 {
		t.Fatal("LineAt wrong")
	}
	cases := [][4]string{
		{"Some Channel", "Artist Name - Song Title (Official Video)", "Artist Name", "Song Title"},
		{"Artist - Topic", "Song Title", "Artist", "Song Title"},
		{"Artist, Other", "Song feat. Someone", "Artist", "Song"},
		{"Artist", "Alive (Remastered 2011)", "Artist", "Alive"},
	}
	for _, c := range cases {
		if a, ti := cleanTrack(c[0], c[1]); a != c[2] || ti != c[3] {
			t.Errorf("cleanTrack(%q,%q) = %q,%q", c[0], c[1], a, ti)
		}
	}

	dir := t.TempDir()
	now := time.Date(2026, 11, 19, 19, 30, 0, 0, time.UTC)
	os.MkdirAll(filepath.Join(dir, "proj"), 0o755)
	line := func(ts time.Time, id string, in, out int) string {
		return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"requestId":"r%s","message":{"id":"m%s","usage":{"input_tokens":%d,"output_tokens":%d}}}`,
			ts.Format(time.RFC3339Nano), id, id, in, out)
	}
	data := strings.Join([]string{
		line(now.Add(-9*time.Hour), "old", 999, 999),
		line(now.Add(-2*time.Hour-10*time.Minute), "1", 100, 50),
		line(now.Add(-1*time.Hour), "2", 10, 5),
		line(now.Add(-1*time.Hour), "2", 10, 5), // duplicate
		`{"type":"user","message":{"content":"hi"}}`,
	}, "\n")
	os.WriteFile(filepath.Join(dir, "proj", "s.jsonl"), []byte(data), 0o644)
	u := ScanUsage([]string{dir}, now)
	if !u.Has || u.Tokens != 165 || u.Requests != 2 || u.Start.Hour() != 17 || u.End.Hour() != 22 {
		t.Fatalf("bad usage %+v", u)
	}
	if fmtTokens(1234567) != "1,2 mln." || fmtTokens(45300) != "45,3 tūkst." {
		t.Fatal("fmtTokens")
	}
	if ScanUsage([]string{dir}, now.Add(4*time.Hour)).Has {
		t.Fatal("window should be closed")
	}
}

func TestLinksWeather(t *testing.T) {
	ls := ParseLinks("# x\nA | https://a.lt\nbad line\nB|spotify:\n")
	if len(ls) != 2 || ls[1].Target != "spotify:" {
		t.Fatalf("links %+v", ls)
	}
	if weatherKind(63) != wxRain || weatherKind(75) != wxSnow || weatherKind(96) != wxStorm || weatherKind(0) != wxClear {
		t.Fatal("weatherKind")
	}
	if fmtTemp(-0.4) != "0°" || fmtTemp(-3.6) != "-4°" {
		t.Fatal("fmtTemp")
	}
}

type fakeBackend struct{ s SettingsDTO }

func (f *fakeBackend) Settings() SettingsDTO                 { return f.s }
func (f *fakeBackend) ApplySettings(s SettingsDTO) error     { f.s = s; return nil }
func (f *fakeBackend) Reminders() []Reminder                 { return nil }
func (f *fakeBackend) AddReminder(text string, at time.Time) {}
func (f *fakeBackend) DelReminder(id int)                    {}
func (f *fakeBackend) Now() NowDTO                           { return NowDTO{Title: "x"} }
func (f *fakeBackend) Cover() []byte                         { return nil }
func (f *fakeBackend) AdjustOffset(d float64)                {}
func (f *fakeBackend) Favs() []Fav                           { return nil }
func (f *fakeBackend) DelFav(i int)                          {}
func (f *fakeBackend) Todos() []Todo                         { return nil }
func (f *fakeBackend) AddTodo(text string)                   {}
func (f *fakeBackend) ToggleTodo(id int)                     {}
func (f *fakeBackend) DelTodo(id int)                        {}

func TestWebPages(t *testing.T) {
	webBackend = &fakeBackend{}
	defer func() { webBackend = nil }()
	h := newServer(NewState())
	get := func(path string, token bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "http://"+listenAddr+path, nil)
		req.Host = listenAddr
		if token {
			req.Header.Set("X-Island-Token", webToken)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := get("/nustatymai", false); w.Code != 200 || !strings.Contains(w.Body.String(), webToken) {
		t.Fatalf("settings page %d", w.Code)
	}
	if w := get("/api/settings", false); w.Code != 403 {
		t.Fatal("api must need token")
	}
	if w := get("/api/now", true); w.Code != 200 || !strings.Contains(w.Body.String(), `"title":"x"`) {
		t.Fatalf("api now %d %s", w.Code, w.Body.String())
	}
	req := httptest.NewRequest("GET", "http://evil.example/api/now", nil)
	req.Header.Set("X-Island-Token", webToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("foreign host must be rejected")
	}
}

func TestTrackerTranslateRem(t *testing.T) {
	now := time.Now()
	var c clockTracker
	m := &Media{Title: "A", Status: 4}
	c.Update(m, now)
	if p := c.Pos(now.Add(10 * time.Second)); p < 9.9 || p > 10.1 {
		t.Fatalf("pos %v", p)
	}
	m.Status = 5
	c.Update(m, now.Add(10*time.Second))
	if p := c.Pos(now.Add(20 * time.Second)); p < 9.9 || p > 10.1 {
		t.Fatalf("paused pos %v", p)
	}
	c.Set(50, now)
	m2 := &Media{Title: "B", Status: 4}
	c.Update(m2, now)
	if c.Pos(now) != 0 {
		t.Fatal("new song should restart")
	}
	txt, src, err := parseGtx([]byte(`[[["Labas\n","Hello\n",null],["pasauli","world",null]],null,"en"]`))
	if err != nil || txt != "Labas\npasauli" || src != "en" {
		t.Fatalf("gtx %q %q %v", txt, src, err)
	}
	r := LoadReminders("")
	r.Add("a", now.Add(time.Minute))
	r.Add("b", now.Add(-time.Second))
	if d := r.Due(now); len(d) != 1 || d[0].Text != "b" || len(r.List()) != 1 {
		t.Fatal("reminders due")
	}
	if untilText(now.Add(12*time.Minute+10*time.Second), now) != "po 12 min" {
		t.Fatal(untilText(now.Add(12*time.Minute), now))
	}
	rec := lrclibRecord{TrackName: "Miražas", ArtistName: "Aleksandras Makejevas", SyncedLyrics: new(string), Duration: 200}
	if matchScore(rec, "Aleksandras Makejevas", "Miražas", 0) <= matchScore(lrclibRecord{TrackName: "Kita", ArtistName: "Kitas", SyncedLyrics: new(string)}, "Aleksandras Makejevas", "Miražas", 0) {
		t.Fatal("score")
	}
}

// Every text passed to L("…") must have English and Russian translations.
func TestAllTextsTranslated(t *testing.T) {
	re := regexp.MustCompile(`(?:^|[^A-Za-z0-9_])L\("((?:[^"\\]|\\.)*)"\)`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			k, err := strconv.Unquote(`"` + m[1] + `"`)
			if err != nil || k == "" {
				continue
			}
			if tr, ok := dict[k]; !ok || tr[0] == "" && !strings.HasPrefix(k, "Liko") || tr[1] == "" {
				t.Errorf("%s: missing translation for %q", f, k)
			}
		}
	}
}

func TestStateFlow(t *testing.T) {
	st := NewState()
	st.HandleEvent(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Edit", "cwd": `C:\Users\v\proj`,
		"tool_input": map[string]any{"file_path": `C:\Users\v\proj\main.go`}})
	s := st.Snapshot()
	if s.Mode != ModeWorking || s.Text != "Redaguoja" || s.Detail != "main.go" || s.Project != "proj" {
		t.Fatalf("bad working state %+v", s)
	}
	st.HandleEvent(map[string]any{"hook_event_name": "Stop"})
	if st.Snapshot().Mode != ModeDone {
		t.Fatal("expected done")
	}
	if len(st.TakeSounds()) != 1 {
		t.Fatal("expected a sound")
	}
	st.Tick(time.Now().Add(20 * time.Second))
	if st.Snapshot().Mode != ModeIdle {
		t.Fatal("expected idle after done timeout")
	}
}

func TestServerPermission(t *testing.T) {
	st := NewState()
	srv := httptest.NewServer(newServer(st))
	defer srv.Close()

	body := `{"hook_event_name":"PermissionRequest","session_id":"s1","cwd":"/x/proj","tool_name":"Bash","tool_input":{"command":"git push"}}`
	done := make(chan string)
	go func() {
		resp, err := http.Post(srv.URL+"/hook", "application/json", strings.NewReader(body))
		if err != nil {
			done <- "err"
			return
		}
		var buf bytes.Buffer
		buf.ReadFrom(resp.Body)
		done <- buf.String()
	}()
	// wait for request to show up
	for i := 0; i < 100 && st.Snapshot().Perm == nil; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if p := st.Snapshot().Perm; p == nil || p.Detail != "git push" {
		t.Fatalf("permission not queued: %+v", p)
	}
	st.Answer("deny")
	out := <-done
	var got map[string]map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad json %q", out)
	}
	if got["hookSpecificOutput"]["hookEventName"] != "PermissionRequest" ||
		got["hookSpecificOutput"]["decision"].(map[string]any)["behavior"] != "deny" {
		t.Fatalf("bad decision %s", out)
	}
	if st.Snapshot().Perm != nil {
		t.Fatal("request not removed")
	}

	// normal event returns empty body quickly
	resp, _ := http.Post(srv.URL+"/hook", "application/json", strings.NewReader(`{"hook_event_name":"Stop"}`))
	var b bytes.Buffer
	b.ReadFrom(resp.Body)
	if resp.StatusCode != 200 || b.Len() != 0 {
		t.Fatalf("unexpected %d %q", resp.StatusCode, b.String())
	}
}

func TestHooksInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	orig := `{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo hi"}]}]}}`
	path := filepath.Join(home, ".claude", "settings.json")
	os.WriteFile(path, []byte(orig), 0o644)

	if HooksInstalled() {
		t.Fatal("should not be installed yet")
	}
	if err := SetHooks(true); err != nil {
		t.Fatal(err)
	}
	SetHooks(true) // idempotent
	if !HooksInstalled() {
		t.Fatal("should be installed")
	}
	b, _ := os.ReadFile(path)
	if strings.Count(string(b), hookURL) != len(hookEvents) || !strings.Contains(string(b), "echo hi") || !strings.Contains(string(b), `"opus"`) {
		t.Fatalf("bad settings:\n%s", b)
	}
	if err := SetHooks(false); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), hookURL) || !strings.Contains(string(b), "echo hi") {
		t.Fatalf("bad after removal:\n%s", b)
	}
	if _, err := os.Stat(path + ".pries-island.bak"); err != nil {
		t.Fatal("no backup")
	}
}

func TestLangSweep(t *testing.T) {
	r := NewRenderer(1.5)
	now := time.Date(2026, 11, 19, 19, 24, 0, 0, time.Local)
	defer setLang("lt")
	wx := Weather{Has: true, City: "Vilnius", Temp: -1.4, Code: 3, IsDay: false}
	for i, c := range []int{2, 63, 3, 73, 0, 95} {
		wx.Days = append(wx.Days, WeatherDay{now.AddDate(0, 0, i), c, float64(4 - i), float64(-2 - i)})
	}
	for _, lang := range []string{"en", "ru", "lt"} {
		setLang(lang)
		v := View{Now: now, T: 1, HooksOn: true, Hover: true, Timer: TimerView{Text: "25:00", Preset: "25/5"}}
		v.Sys = SysInfo{Has: true, Battery: 100, Charging: true, CPU: 0.12, RAM: 0.69, RAMUsed: 5.3, RAMTotal: 7.7}
		v.Net = NetInfo{Has: true, Online: true, Down: 1500, Up: 900}
		v.Weather = wx
		v.Media = &Media{App: "chrome.exe", Title: "Northern Lights - Paper Boats", Artist: "Northern Lights", Status: 4, Pos: 41, End: 214, Updated: now.UnixMilli()}
		v.Lyrics = &Lyrics{State: lyrSynced, Lines: ParseLRC("[00:30.00]Folding paper boats at midnight\n[00:38.50]Sending them across the river\n[00:44.00]Every one a little promise\n[00:50.00]Floating somewhere into morning")}
		v.Links = ParseLinks(defaultLinks)
		v.X.Bright = 0.6
		v.X.Mic = "Discord"
		v.X.BT = []BTView{{"Redmi Buds 6 Active", 80}, {"Marshall Major IV", 40}, {"MX Master 3S", 15}}
		v.X.Unread = []UnreadView{{"Discord", 3}}
		v.X.Clocks = worldClocks([]string{"Londonas", "Niujorkas", "Tokijas"}, now)
		v.X.Focus = FocusView{Today: 4500, Week: []float64{1200, 3000, 0, 5400, 2400, 1800, 4500}}
		v.X.Plan = []PlanRow{{Kind: "event", When: "14:00", Text: "Math lesson", Extra: "today"}, {Kind: "todo", ID: 2, Text: "Buy milk"}}
		v.Snap = Snapshot{Mode: ModeIdle, ModeSince: now.Add(-time.Second)}
		v.Clips = []string{"git push origin main", "https://github.com/x"}
		v.Note = "Note"
		for tab := 0; tab < len(tabNames); tab++ {
			v.Tab = tab
			snapshot(t, r, fmt.Sprintf("lang-%s-%02d", lang, tab), v)
			if tab == tabSystem {
				for s := 1; s <= 4; s++ {
					v.X.SysScroll = s
					snapshot(t, r, fmt.Sprintf("lang-%s-%02d-s%d", lang, tab, s), v)
				}
				v.X.SysScroll = 0
			}
		}
	}
}

func TestIntroCoverFixes(t *testing.T) {
	// countdown during the intro
	l := &Lyrics{State: lyrSynced, Lines: ParseLRC("[01:01.89]Pirma eilutė\n[01:05.48]Antra eilutė\n[01:20.00]\n[01:40.00]Po pertraukos")}
	if g0, g1, ok := lyricGap(l, l.LineAt(15), 15); !ok || g0 != 0 || math.Abs(g1-61.89) > 0.01 {
		t.Fatalf("intro gap: %v %v %v", g0, g1, ok)
	}
	if _, _, ok := lyricGap(l, l.LineAt(62), 62); ok {
		t.Fatal("no gap while singing")
	}
	if _, g1, ok := lyricGap(l, l.LineAt(85), 85); !ok || g1 != 100 {
		t.Fatal("instrumental gap", g1, ok)
	}
	if _, _, ok := lyricGap(l, l.LineAt(60.5), 60.5); ok {
		t.Fatal("too close to the line for a countdown")
	}

	// black bars of a YouTube thumbnail are cut away
	img := image.NewRGBA(image.Rect(0, 0, 480, 360))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.Black}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(135, 45, 345, 315), &image.Uniform{color.RGBA{200, 150, 40, 255}}, image.Point{}, draw.Src)
	if r := trimBars(img); r != image.Rect(135, 45, 345, 315) {
		t.Fatal("trim", r)
	}
	dark := image.NewRGBA(image.Rect(0, 0, 100, 100))
	draw.Draw(dark, dark.Bounds(), &image.Uniform{color.Black}, image.Point{}, draw.Src)
	if r := trimBars(dark); r != dark.Bounds() {
		t.Fatal("all-dark picture kept", r)
	}

	// online cover: Deezer answer is matched by artist and title
	var pngBuf bytes.Buffer
	png.Encode(&pngBuf, img)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/search"):
			fmt.Fprintf(w, `{"data":[{"title":"Kita daina","artist":{"name":"Северный Ветер"},"album":{"cover_xl":"%[1]s/bad.png"}},
				{"title":"Рассвет","artist":{"name":"Северный ветер"},"album":{"cover_xl":"%[1]s/good.png"}}]}`, "http://"+r.Host)
		case r.URL.Path == "/good.png":
			w.Write(pngBuf.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	wc := NewWebCovers(t.TempDir())
	wc.deezer, wc.itunes = srv.URL, srv.URL
	got := make(chan bool, 1)
	wc.OnReady = func() { got <- true }
	m := &Media{Title: "Северный Ветер - Рассвет (official audio)", Artist: "Some Channel"}
	if wc.Get(m) != "" {
		t.Fatal("should wait for the player first")
	}
	wc.seen[m.Key()] = time.Now().Add(-time.Minute)
	wc.Get(m)
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("no cover")
	}
	ref := wc.Get(m)
	if ref == "" || LoadCover(ref, 52, 12) == nil || len(CoverJPEG(ref, 300)) == 0 {
		t.Fatal("cover not usable", ref)
	}
	if !coverMatch("Северный ветер", "Рассвет", "Северный Ветер", "Рассвет") || coverMatch("X", "Kita", "Северный Ветер", "Рассвет") {
		t.Fatal("coverMatch")
	}

	// preview: island during a one-minute intro
	r := NewRenderer(1.5)
	now := time.Date(2026, 11, 19, 21, 54, 0, 0, time.Local)
	setLang("en")
	defer setLang("lt")
	v := View{Now: now, T: 1, HooksOn: true, Hover: true, Tab: tabMusic}
	v.Snap = Snapshot{Mode: ModeIdle, ModeSince: now.Add(-time.Second)}
	v.Media = &Media{App: "chrome.exe", Title: "Рассвет", Artist: "Северный Ветер", Status: 4, Pos: 15, End: 268, Updated: now.UnixMilli()}
	v.Lyrics = l
	v.Cover = LoadCover(ref, 78, 12*1.5)
	snapshot(t, r, "08a-music-intro", v)
}

func TestHotkeys(t *testing.T) {
	for in, want := range map[string]string{"Ctrl+Alt+K": "Ctrl+Alt+K", "ctrl + shift + space": "Ctrl+Shift+Space", "Alt+Win+F5": "Alt+Win+F5", "Ctrl+Alt+7": "Ctrl+Alt+7", "Shift+Alt+Ctrl+d": "Ctrl+Shift+Alt+D"} {
		h, ok := ParseHotkey(in)
		if !ok || h.String() != want {
			t.Fatalf("%q -> %q %v", in, h.String(), ok)
		}
	}
	for _, bad := range []string{"", "K", "Shift+K", "Ctrl+Alt", "Ctrl+Alt+K+J", "Ctrl+Alt+F13", "Ctrl+Alt+Enter"} {
		if _, ok := ParseHotkey(bad); ok {
			t.Fatalf("%q should be refused", bad)
		}
	}
	for _, act := range hotkeyActions {
		if c := hotkeyCandidates(act, ""); len(c) < 2 {
			t.Fatal("need fallbacks for", act.Name)
		}
		for _, d := range act.Defaults {
			if d == "Ctrl+Alt+Space" {
				t.Fatal("Ctrl+Alt+Space belongs to Claude's quick window")
			}
		}
	}
	c := hotkeyCandidates(hotkeyActions[2], "ctrl+alt+k")
	if c[0].String() != "Ctrl+Alt+K" || len(c) != len(hotkeyActions[2].Defaults) {
		t.Fatal("duplicates must be dropped", c)
	}
	if withKey("Command bar", "Ctrl+Alt+K") != "Command bar (Ctrl+Alt+K)" || withKey("X", "") != "X" {
		t.Fatal("withKey")
	}
}

// TestReadmeShots renders the README screenshots (ISLET_SHOTS=1 go test -run ReadmeShots).
func TestReadmeShots(t *testing.T) {
	if os.Getenv("ISLET_SHOTS") == "" {
		t.Skip("set ISLET_SHOTS=1 to render docs/screenshots")
	}
	setLang("en")
	defer setLang("lt")
	r := NewRenderer(2)
	now := time.Date(2026, 11, 19, 19, 24, 0, 0, time.Local)
	shot := func(name string, v View) {
		tw, th := r.Target(v)
		v.W, v.H = tw, th
		img, _ := r.Draw(v)
		rgba := image.NewRGBA(img.Bounds())
		draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)
		// crop to the pill (plus a little air for the shadow)
		b := image.Rectangle{}
		for y := rgba.Bounds().Min.Y; y < rgba.Bounds().Max.Y; y++ {
			for x := rgba.Bounds().Min.X; x < rgba.Bounds().Max.X; x++ {
				if rgba.RGBAAt(x, y).A > 8 {
					b = b.Union(image.Rect(x, y, x+1, y+1))
				}
			}
		}
		b = b.Inset(-6).Intersect(rgba.Bounds())
		os.MkdirAll(filepath.Join("docs", "screenshots"), 0o755)
		f, _ := os.Create(filepath.Join("docs", "screenshots", name+".png"))
		png.Encode(f, rgba.SubImage(b))
		f.Close()
	}
	wx := Weather{Has: true, City: "Vilnius", Temp: 3.4, Code: 2, IsDay: true}
	for i, c := range []int{2, 63, 3, 73, 0, 95} {
		wx.Days = append(wx.Days, WeatherDay{now.AddDate(0, 0, i), c, float64(6 - i), float64(-1 - i)})
	}
	media := &Media{App: "Spotify.exe", Title: "Barcos de Papel", Artist: "Luna Norte", Status: 4, Pos: 41, End: 214, Updated: now.UnixMilli()}
	lyr := &Lyrics{State: lyrSynced, Lines: ParseLRC("[00:30.00]Barcos de papel a medianoche\n[00:38.50]Cruzando el río sin miedo\n[00:44.00]Cada uno es una promesa\n[00:50.00]Flotando hacia la mañana")}
	base := View{Now: now, T: 1, HooksOn: true, Timer: TimerView{Text: "25:00", Preset: "25/5"}}
	base.Sys = SysInfo{Has: true, Battery: 82, Charging: true, CPU: 0.18, RAM: 0.54, RAMUsed: 8.6, RAMTotal: 16}
	base.Net = NetInfo{Has: true, Online: true, Down: 3.2 * (1 << 20), Up: 240 * 1024}
	base.Weather = wx
	base.Usage = Usage{Has: true, Start: now.Add(-2 * time.Hour), End: now.Add(3*time.Hour - 12*time.Minute), Tokens: 1234567, Requests: 34}
	base.Snap = Snapshot{Mode: ModeIdle, ModeSince: now.Add(-time.Second)}

	v := base
	shot("collapsed-idle", v)
	v.Snap = Snapshot{Mode: ModeWorking, Text: "Redaguoja", Detail: "main.go", Project: "islet", ModeSince: now.Add(-3 * time.Second)}
	shot("collapsed-working", v)
	v = base
	v.Media, v.Lyrics = media, lyr
	shot("collapsed-lyrics", v)

	v = base
	v.Snap = Snapshot{Mode: ModePermission, ModeSince: now.Add(-time.Second), PermCount: 1,
		Perm: &PermReq{Tool: "Bash", Detail: "npm run build", Project: "my-website", Created: now.Add(-6 * time.Second)}}
	v.HoverBtn = "allow"
	shot("permission", v)

	v = base
	v.Hover = true
	v.Media, v.Lyrics = media, lyr
	v.Timer = TimerView{Active: true, Running: true, Text: "18:42", Preset: "25/5", Progress: 0.25}
	shot("tab-home", v)
	v.Tab = tabMusic
	l2 := *lyr
	l2.Trans, l2.TransLang = []string{"Paper boats at midnight", "Crossing the river without fear", "Each one is a promise", "Floating towards the morning"}, "en"
	v.Lyrics, v.Translate = &l2, true
	v.X.Peaks = [5]float64{0.3, 0.7, 0.5, 0.9, 0.4}
	v.X.CoverColor = &[3]uint8{0x5e, 0x8b, 0xff}
	shot("tab-music", v)
	v.Translate, v.X = false, Extra{}
	v.Tab = tabWeather
	v.X.Clocks = worldClocks([]string{"Londonas", "Niujorkas", "Tokijas"}, now)
	shot("tab-weather", v)
	v.X = Extra{Bright: 0.7, Mic: "Discord"}
	v.Tab = tabSystem
	v.Snap.Sessions = []Session{{Project: "islet", Text: "Redaguoja", Detail: "render.go", Mode: ModeWorking}, {Project: "my-website", Text: "Baigta", Mode: ModeDone}}
	shot("tab-system", v)
	v.Snap.Sessions = nil
	v.X = Extra{}
	v.Tab = tabRemind
	v.X.Plan = []PlanRow{{Kind: "event", When: "14:00", Text: "Team call", Extra: "today"}, {Kind: "remind", ID: 1, When: "19:30", Text: "Gym", Extra: "in 40 min"},
		{Kind: "todo", ID: 2, Text: "Buy milk", Done: true}, {Kind: "todo", ID: 3, Text: "Finish the README"}}
	shot("tab-plans", v)
	v.X = Extra{Cmd: &CmdView{Input: "12*7+5", Results: EvalCommand("12*7+5", nil)}, Caret: true}
	v.Hover = false
	shot("command-bar", v)
}
