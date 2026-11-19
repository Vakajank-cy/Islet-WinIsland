package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomedium"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
)

// Logical canvas (scaled by DPI). The pill is drawn centered at the top.
const (
	canvasW = 500
	canvasH = 300
	pillTop = 10
	smallH  = 36
	bigH    = 198
	musicH  = 270
	permH   = 148
)

// View is everything needed to draw one frame.
type View struct {
	Snap     Snapshot
	Hover    bool
	HoverBtn string
	Now      time.Time
	T        float64 // seconds since start, for animations
	W, H     float64 // current (animated) pill size
	HooksOn  bool

	Tab        int
	Timer      TimerView
	Clips      []string
	ClipScroll int
	Note       string
	RobotColor int
	Face       int
	Flash      *Flash
	Media      *Media
	Lyrics     *Lyrics
	Usage      Usage
	Sys        SysInfo
	LyricsOff  bool        // don't show lyric lines in the small island
	Cover      image.Image // album art, already rounded and sized 52*S
	Weather    Weather
	Links      []Link
	Volume     *VolumeOSD
	AI         AIState
	Shot       *ShotView

	Reminders   []Reminder
	Shelf       []string
	Net         NetInfo
	Translate   bool
	LyricOffset float64
	X           Extra
}

// SysInfo is battery / CPU / memory state.
type SysInfo struct {
	Has      bool
	Battery  int // percent, -1 = no battery
	Charging bool
	CPU      float64
	RAM      float64
	RAMUsed  float64 // GB
	RAMTotal float64 // GB
}

func (s SysInfo) LowBattery() bool { return s.Has && s.Battery >= 0 && s.Battery <= 20 && !s.Charging }

// TimerView is the focus timer as the renderer sees it.
type TimerView struct {
	Active, Running bool
	Text, Preset    string
	Phase, Rounds   int
	Progress        float64
}

var tabNames = []string{"Pradžia", "Muzika", "Orai", "Laikmatis", "Planai", "Iškarpinė", "Lentyna", "Užrašas", "Nuorodos", "Sistema"}

const (
	tabHome = iota
	tabMusic
	tabWeather
	tabTimer
	tabRemind
	tabClips
	tabShelf
	tabNote
	tabLinks
	tabSystem
	tabRobot = -1 // robot looks moved to the settings page
)

// Hit is a clickable area in physical pixels.
type Hit struct {
	Name           string
	X0, Y0, X1, Y1 float64
}

func (h Hit) Contains(x, y float64) bool { return x >= h.X0 && x <= h.X1 && y >= h.Y0 && y <= h.Y1 }

type fontKind int

const (
	fRegular fontKind = iota
	fSemibold
	fBold
	fMono
)

type Renderer struct {
	S     float64 // DPI scale
	fonts [4]*truetype.Font
	faces map[string]font.Face
	cache map[string]image.Image
	texts map[textKey]cachedText
}

func NewRenderer(scale float64) *Renderer {
	r := &Renderer{S: scale, faces: map[string]font.Face{}, cache: map[string]image.Image{}, texts: map[textKey]cachedText{}}
	win := os.Getenv("WINDIR")
	if win == "" {
		win = `C:\Windows`
	}
	files := [4]string{"segoeui.ttf", "seguisb.ttf", "segoeuib.ttf", "consola.ttf"}
	fallback := [4][]byte{goregular.TTF, gomedium.TTF, gobold.TTF, gomono.TTF}
	for i := range files {
		if b, err := os.ReadFile(filepath.Join(win, "Fonts", files[i])); err == nil {
			if f, err := truetype.Parse(b); err == nil {
				r.fonts[i] = f
				continue
			}
		}
		r.fonts[i], _ = truetype.Parse(fallback[i])
	}
	return r
}

func (r *Renderer) face(k fontKind, size float64) font.Face {
	key := fmt.Sprintf("%d/%.2f", k, size*r.S)
	if f, ok := r.faces[key]; ok {
		return f
	}
	f := truetype.NewFace(r.fonts[k], &truetype.Options{Size: size * r.S, Hinting: font.HintingNone})
	r.faces[key] = f
	return f
}

// measure returns text width in logical px.
func (r *Renderer) measure(k fontKind, size float64, s string) float64 {
	return float64(font.MeasureString(r.face(k, size), s)) / 64 / r.S
}

func (r *Renderer) fit(k fontKind, size float64, s string, max float64) string {
	if max <= 0 {
		return ""
	}
	if r.measure(k, size, s) <= max {
		return s
	}
	rs := []rune(s)
	for len(rs) > 0 {
		rs = rs[:len(rs)-1]
		t := strings.TrimRight(string(rs), " ") + "…"
		if r.measure(k, size, t) <= max {
			return t
		}
	}
	return ""
}

// ---------- colors ----------

var (
	cPill  = rgb(0x0a, 0x0a, 0x0b)
	cText  = rgb(0xf5, 0xf5, 0xf7)
	cGray  = rgb(0x98, 0x98, 0x9f)
	cDim   = rgb(0x63, 0x63, 0x66)
	cBtn   = rgb(0x2c, 0x2c, 0x2e)
	cBtnHi = rgb(0x3d, 0x3d, 0x40)
	cBox   = rgb(0x1c, 0x1c, 0x1e)
)

func rgb(r, g, b uint8) color.NRGBA { return color.NRGBA{r, g, b, 255} }

func accent(m Mode) color.NRGBA {
	switch m {
	case ModeWorking:
		return rgb(0xff, 0x9f, 0x0a)
	case ModePermission:
		return rgb(0xff, 0xd6, 0x0a)
	case ModeDone:
		return rgb(0x30, 0xd1, 0x58)
	case ModeWaiting:
		return rgb(0x0a, 0x84, 0xff)
	}
	return rgb(0xc7, 0xc7, 0xcc)
}

func withA(c color.NRGBA, a float64) color.NRGBA {
	c.A = uint8(math.Max(0, math.Min(1, a)) * float64(c.A))
	return c
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// ---------- layout ----------

var (
	cWork = rgb(0xff, 0x69, 0x61)
	cRest = rgb(0x30, 0xd1, 0x58)
)

func expanded(v View) bool {
	return v.Snap.Mode == ModePermission || (v.Hover && !v.shotOn() && !v.cardOn() && v.X.Cmd == nil && v.X.Game == nil && v.X.Picker == nil)
}

func (v View) flashOn() bool { return v.Flash != nil && v.Now.Before(v.Flash.Until) }

func phaseColor(phase int) color.NRGBA {
	if phase == 1 {
		return cRest
	}
	return cWork
}

func phaseName(phase int) string {
	if phase == 1 {
		return L("Poilsis")
	}
	return L("Darbas")
}

func (v View) headColor() color.NRGBA {
	if v.RobotColor > 0 && v.RobotColor < len(robotColors) {
		c := robotColors[v.RobotColor]
		return rgb(c.R, c.G, c.B)
	}
	return accent(v.Snap.Mode)
}

// Target returns the pill size the current state wants.
func (r *Renderer) Target(v View) (float64, float64) {
	s := v.Snap
	if s.Mode == ModePermission {
		return 460, permH
	}
	if v.X.Cmd != nil {
		return 460, cmdHeight(v.X.Cmd)
	}
	if v.X.Game != nil && v.X.Game.On {
		return 460, 140
	}
	if v.X.Picker != nil {
		return 330, smallH
	}
	if v.cardOn() {
		return r.cardSize(v.X.Card)
	}
	if v.shotOn() {
		return 380, 96
	}
	if v.volOn() && !v.Hover {
		return 260, smallH
	}
	if v.Hover {
		if v.Tab == tabMusic {
			return 440, musicH
		}
		return 440, bigH
	}
	switch s.Mode {
	case ModeWorking:
		tw := r.measure(fSemibold, 13.5, L(s.Text))
		if s.Detail != "" {
			tw += 8 + math.Min(r.measure(fRegular, 13, s.Detail), 220)
		}
		return math.Max(210, math.Min(400, 48+tw+56)), smallH
	case ModeDone:
		return math.Max(180, 48+r.measure(fSemibold, 13.5, L(s.Text))+50), smallH
	case ModeWaiting:
		tw := r.measure(fSemibold, 13.5, L(s.Text))
		if s.Detail != "" {
			tw += 8 + math.Min(r.measure(fRegular, 13, s.Detail), 200)
		}
		return math.Max(210, math.Min(420, 48+tw+22)), smallH
	}
	if v.flashOn() {
		return math.Max(200, math.Min(420, 48+r.measure(fSemibold, 13.5, v.Flash.Text)+22)), smallH
	}
	if txt, _ := v.musicLine(); txt != "" {
		return math.Max(230, math.Min(440, 48+math.Min(r.measure(fSemibold, 13, txt), 330)+46)), smallH
	}
	if v.AI.Generating {
		return math.Max(220, 48+r.measure(fSemibold, 13.5, v.AI.Name+L(" rašo atsakymą"))+46), smallH
	}
	if v.Timer.Active {
		return 214, smallH
	}
	w := 156.0
	if v.Weather.Has {
		w += 50
	}
	if v.Usage.Has {
		w += 22
	}
	if v.Sys.LowBattery() {
		w += 50
	}
	return w, smallH
}

// musicLine is what the collapsed island shows while music plays:
// the current lyric line if we have synced lyrics, else "title · artist".
func (v View) musicLine() (string, bool) {
	m := v.Media
	if !m.Playing() {
		return "", false
	}
	if l := v.Lyrics; !v.LyricsOff && l != nil && l.State == lyrSynced {
		if i := l.LineAt(m.Position(v.Now) + lyricLead + v.LyricOffset); i >= 0 && strings.TrimSpace(l.Lines[i].Text) != "" {
			return l.Lines[i].Text, true
		}
	}
	if m.Artist != "" {
		return m.Title + " · " + m.Artist, false
	}
	return m.Title, false
}

var cMusic = rgb(0xff, 0x37, 0x5f)

// lyricLead shows each line a moment early, so it is on screen when singing starts.
const lyricLead = 0.2

// ---------- drawing primitives ----------

type painter struct {
	r  *Renderer
	dc *gg.Context
	s  float64
	a  float64 // content alpha
	ox float64 // device x offset of the drawing surface (for gradients)
}

func (p *painter) rr(x, y, w, h, rad float64, c color.Color) {
	p.dc.DrawRoundedRectangle(x*p.s, y*p.s, w*p.s, h*p.s, rad*p.s)
	p.dc.SetColor(c)
	p.dc.Fill()
}

func (p *painter) circle(x, y, rad float64, c color.Color) {
	p.dc.DrawCircle(x*p.s, y*p.s, rad*p.s)
	p.dc.SetColor(c)
	p.dc.Fill()
}

func (p *painter) ring(x, y, rad, width float64, c color.Color) {
	p.dc.NewSubPath()
	p.dc.DrawCircle(x*p.s, y*p.s, rad*p.s)
	p.dc.SetLineWidth(width * p.s)
	p.dc.SetColor(c)
	p.dc.Stroke()
}

func (p *painter) arc(x, y, rad, width, a0, a1 float64, c color.Color) {
	p.dc.NewSubPath()
	p.dc.DrawArc(x*p.s, y*p.s, rad*p.s, a0, a1)
	p.dc.SetLineWidth(width * p.s)
	p.dc.SetLineCap(gg.LineCapRound)
	p.dc.SetColor(c)
	p.dc.Stroke()
	p.dc.SetLineCap(gg.LineCapButt)
}

// text draws s with its visual middle at cy; ax: 0 left, 0.5 center, 1 right.
// Rendered text is cached as small images: drawing glyphs is the slowest
// part of a frame, and most texts don't change between frames.
func (p *painter) text(s string, k fontKind, size, x, cy, ax float64, c color.NRGBA) float64 {
	w := p.r.measure(k, size, s)
	col := withA(c, p.a)
	if col.A < 8 || s == "" {
		return w
	}
	col.A = uint8(min(255, (int(col.A)+8)/16*16)) // quantize alpha so fades reuse the cache
	img, asc := p.r.textImage(s, k, size, col)
	base := cy + size*0.36
	p.blitDevice(img, (x-ax*w)*p.s-p.ox-2, base*p.s-float64(asc))
	return w
}

type textKey struct {
	s    string
	k    fontKind
	size float64
	c    color.NRGBA
}

func (r *Renderer) textImage(s string, k fontKind, size float64, c color.NRGBA) (*image.RGBA, int) {
	key := textKey{s, k, size, c}
	if t, ok := r.texts[key]; ok {
		return t.img, t.asc
	}
	f := r.face(k, size)
	m := f.Metrics()
	asc := m.Ascent.Ceil() + 1
	h := asc + m.Descent.Ceil() + 2
	w := int(math.Ceil(r.measure(k, size, s)*r.S)) + 4
	dc := gg.NewContext(w, h)
	dc.SetFontFace(f)
	dc.SetColor(c)
	dc.DrawString(s, 2, float64(asc))
	img := dc.Image().(*image.RGBA)
	if len(r.texts) > 800 {
		r.texts = map[textKey]cachedText{}
	}
	r.texts[key] = cachedText{img, asc}
	return img, asc
}

type cachedText struct {
	img *image.RGBA
	asc int
}

// blitDevice composites img at device-pixel position (x, y).
func (p *painter) blitDevice(img image.Image, x, y float64) {
	dst, ok := p.dc.Image().(*image.RGBA)
	if !ok {
		return
	}
	pt := image.Pt(int(math.Round(x)), int(math.Round(y)))
	r := image.Rectangle{pt, pt.Add(img.Bounds().Size())}
	draw.Draw(dst, r, img, img.Bounds().Min, draw.Over)
}

func (p *painter) poly(c color.Color, pts ...float64) {
	p.dc.NewSubPath()
	for i := 0; i+1 < len(pts); i += 2 {
		p.dc.LineTo(pts[i]*p.s, pts[i+1]*p.s)
	}
	p.dc.ClosePath()
	p.dc.SetColor(c)
	p.dc.Fill()
}

// robot draws the mascot; (x, cy) = left edge and vertical middle of the head.
func (p *painter) robot(x, cy float64, m Mode, t float64, head color.NRGBA, face int, alpha float64, mood int, peak float64) {
	dark := withA(cEye, alpha)
	head = withA(head, alpha)
	y := cy - 8.5
	if mood == moodMusic && m == ModeIdle {
		y -= peak * 2.6 // nods to the beat
	}
	if mood == moodTired && m == ModeIdle {
		y += 0.8
	}
	switch m {
	case ModePermission:
		y += math.Sin(t*9) * 0.7
	case ModeWorking:
		y += math.Sin(t*3) * 0.4
	}
	// cat ears
	if face == 3 {
		p.poly(head, x+0.5, y+5, x+2.5, y-4, x+9, y+1)
		p.poly(head, x+21.5, y+5, x+19.5, y-4, x+13, y+1)
	}
	// antenna (always shows the status color)
	p.dc.SetLineWidth(1.4 * p.s)
	p.dc.SetColor(withA(cDim, alpha))
	p.dc.DrawLine((x+11)*p.s, (y-0.5)*p.s, (x+11)*p.s, (y-3.5)*p.s)
	p.dc.Stroke()
	antA := 1.0
	if m == ModeWorking || m == ModePermission {
		antA = 0.55 + 0.45*math.Sin(t*5)
	}
	p.circle(x+11, y-4.3, 1.9, withA(accent(m), antA*alpha))
	// head
	p.rr(x, y, 22, 17, 5.5, head)

	ex1, ex2, ey := x+6.2, x+12.2, y+5.6
	ew, eh := 3.6, 5.6
	c1, c2 := ex1+ew/2, ex2+ew/2
	if m == ModeDone {
		for _, cx := range []float64{c1, c2} {
			p.arc(cx, ey+4.2, 2.1, 1.7, math.Pi*1.1, math.Pi*1.9, dark)
		}
		return
	}
	dx, dy := 0.0, 0.0
	switch m {
	case ModeWorking:
		dx = math.Sin(t*2.2) * 1.6
	case ModeWaiting:
		dy = -1
	}
	blink := 1.0
	if ph := math.Mod(t, 4.3); ph < 0.14 {
		blink = math.Max(0.16, 1-math.Sin(ph/0.14*math.Pi))
	}
	if m == ModeIdle {
		switch mood {
		case moodSleep:
			// closed eyes and a few floating "z"
			for _, cx := range []float64{c1, c2} {
				p.rr(cx-1.9, ey+3.2, 3.8, 1.3, 0.6, dark)
			}
			for i := 0; i < 2; i++ {
				ph := math.Mod(t*0.5+float64(i)*0.5, 1)
				za := alpha * math.Sin(ph*math.Pi)
				zx, zy := x+22+ph*6+float64(i)*4, y-1-ph*8
				sz := 2.2 + float64(i)*0.8
				zc := withA(cGray, za)
				p.dc.SetLineWidth(1.1 * p.s)
				p.dc.SetColor(zc)
				p.dc.MoveTo(zx*p.s, zy*p.s)
				p.dc.LineTo((zx+sz)*p.s, zy*p.s)
				p.dc.LineTo(zx*p.s, (zy+sz)*p.s)
				p.dc.LineTo((zx+sz)*p.s, (zy+sz)*p.s)
				p.dc.Stroke()
			}
			return
		case moodTired:
			blink = 0.4
		case moodMusic:
			// happy eyes while music plays
			for _, cx := range []float64{c1, c2} {
				p.arc(cx, ey+4.2, 2.1, 1.7, math.Pi*1.1, math.Pi*1.9, dark)
			}
			return
		}
	}
	mid := ey + 2.8 + dy
	switch face {
	case 1: // round eyes
		for _, cx := range []float64{c1, c2} {
			p.dc.DrawEllipse((cx+dx)*p.s, mid*p.s, 2.4*p.s, 2.4*blink*p.s)
			p.dc.SetColor(dark)
			p.dc.Fill()
		}
	case 2: // glasses
		for _, cx := range []float64{c1, c2} {
			p.ring(cx, mid, 3.4, 1.1, dark)
			p.dc.DrawEllipse((cx+dx*0.5)*p.s, mid*p.s, 1.5*p.s, 1.5*blink*p.s)
			p.dc.SetColor(dark)
			p.dc.Fill()
		}
		p.dc.SetLineWidth(1.1 * p.s)
		p.dc.DrawLine((c1+3.4)*p.s, mid*p.s, (c2-3.4)*p.s, mid*p.s)
		p.dc.Stroke()
	default:
		h := math.Max(0.9, eh*blink)
		p.rr(ex1+dx, mid-h/2, ew, h, math.Min(1.7, h/2), dark)
		p.rr(ex2+dx, mid-h/2, ew, h, math.Min(1.7, h/2), dark)
		if face == 3 { // cat nose
			p.poly(dark, x+10, y+12.6, x+12, y+12.6, x+11, y+13.9)
		}
	}
}

func (p *painter) glow(px, py, w, h, rad float64, c color.NRGBA, base float64) {
	for i := 1; i <= 9; i++ {
		f := float64(i)
		a := base * math.Pow(1-f/10, 2)
		p.dc.NewSubPath()
		p.dc.DrawRoundedRectangle((px-f)*p.s, (py-f)*p.s, (w+2*f)*p.s, (h+2*f)*p.s, (rad+f)*p.s)
		p.dc.SetLineWidth(1.3 * p.s)
		p.dc.SetColor(withA(c, a))
		p.dc.Stroke()
	}
}

// wrap splits text into lines that fit width (logical px).
func (r *Renderer) wrap(k fontKind, size float64, text string, width float64, maxLines int) []string {
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		cur := ""
		for _, w := range words {
			try := w
			if cur != "" {
				try = cur + " " + w
			}
			if r.measure(k, size, try) <= width || cur == "" {
				cur = try
			} else {
				lines = append(lines, cur)
				cur = w
			}
		}
		lines = append(lines, cur)
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = r.fit(k, size, strings.TrimRight(lines[maxLines-1], " .…")+" …", width)
	}
	for i := range lines {
		lines[i] = r.fit(k, size, lines[i], width)
	}
	return lines
}

// ---------- frame ----------

type frame struct {
	*painter
	v    View
	px   float64
	py   float64
	pw   float64
	ph   float64
	hits []Hit
}

func (f *frame) hit(name string, x0, y0, x1, y1 float64) {
	f.hits = append(f.hits, Hit{name, x0 * f.s, y0 * f.s, x1 * f.s, y1 * f.s})
}

// button draws a rounded button and registers its hit area.
func (f *frame) button(name, label string, x, cy, w, h float64, primary bool) {
	bg, fg := cBtn, cText
	if primary {
		bg, fg = cText, cPill
		if f.v.HoverBtn == name {
			bg = rgb(0xff, 0xff, 0xff)
		}
	} else if f.v.HoverBtn == name {
		bg = cBtnHi
	}
	f.rr(x, cy-h/2, w, h, h/2, withA(bg, f.a))
	f.text(label, fSemibold, 13, x+w/2, cy, 0.5, fg)
	f.hit(name, x, cy-h/2, x+w, cy+h/2)
}

// iconButton draws a round button; icon is drawn by the callback.
func (f *frame) iconButton(name string, cx, cy, rad float64, primary bool, icon func(c color.NRGBA)) {
	bg, fg := cBtn, cText
	if primary {
		bg, fg = cText, cPill
		if f.v.HoverBtn == name {
			bg = rgb(0xff, 0xff, 0xff)
		}
	} else if f.v.HoverBtn == name {
		bg = cBtnHi
	}
	f.circle(cx, cy, rad, withA(bg, f.a))
	icon(withA(fg, f.a))
	f.hit(name, cx-rad, cy-rad, cx+rad, cy+rad)
}

// Draw renders one frame. Only the area around the pill is drawn (fast);
// the returned image's Bounds().Min is its position in the window.
func (r *Renderer) Draw(v View) (*image.RGBA, []Hit) {
	s := r.S
	pxL := (canvasW - v.W) / 2
	x0 := int(math.Max(0, math.Floor((pxL-12)*s)))
	x1 := int(math.Min(math.Ceil(canvasW*s), math.Ceil((pxL+v.W+12)*s)))
	y1 := int(math.Min(math.Ceil(canvasH*s), math.Ceil((pillTop+v.H+12)*s)))
	dc := gg.NewContext(x1-x0, y1)
	dc.Translate(float64(-x0), 0)
	f := &frame{painter: &painter{r: r, dc: dc, s: s, a: 1, ox: float64(x0)}, v: v}
	f.pw, f.ph = v.W, v.H
	f.px = (canvasW - f.pw) / 2
	f.py = float64(pillTop)
	px, py, pw, ph := f.px, f.py, f.pw, f.ph
	rad := math.Min(ph/2, 26)
	m := v.Snap.Mode

	// glow
	since := v.Now.Sub(v.Snap.ModeSince).Seconds()
	switch {
	case m == ModePermission:
		f.glow(px, py, pw, ph, rad, accent(m), 0.45+0.3*math.Sin(v.T*4))
	case m == ModeDone:
		f.glow(px, py, pw, ph, rad, accent(m), 0.18+0.55*math.Exp(-since/1.5))
	case m == ModeWaiting:
		f.glow(px, py, pw, ph, rad, accent(m), 0.3+0.15*math.Sin(v.T*2.5))
	case v.flashOn():
		fc := v.Flash.Color
		f.glow(px, py, pw, ph, rad, rgb(fc[0], fc[1], fc[2]), 0.3+0.2*math.Sin(v.T*4))
	case v.cardOn():
		cc := v.X.Card.Color
		f.glow(px, py, pw, ph, rad, rgb(cc[0], cc[1], cc[2]), 0.25)
	case v.X.CoverColor != nil && v.Media.Playing() && !v.Hover:
		cc := v.X.CoverColor
		f.glow(px, py, pw, ph, rad, rgb(cc[0], cc[1], cc[2]), 0.1+0.55*peakAvg(v.X.Peaks))
	}

	f.solidRect(px, py, pw, ph, rad, cPill)
	defer func() {
		// privacy dots: orange = microphone, green = camera (like on a phone)
		dx := px + pw - 12
		if v.X.Cam != "" {
			f.circle(dx, py+9, 3.2, rgb(0x30, 0xd1, 0x58))
			dx -= 9
		}
		if v.X.Mic != "" {
			f.circle(dx, py+9, 3.2, rgb(0xff, 0x9f, 0x0a))
		}
	}()
	if lightTheme {
		dc.NewSubPath()
		dc.DrawRoundedRectangle((px+0.5)*s, (py+0.5)*s, (pw-1)*s, (ph-1)*s, (rad-0.5)*s)
		dc.SetLineWidth(1 * s)
		dc.SetColor(cBtnHi)
		dc.Stroke()
	}
	f.hit("pill", px, py, px+pw, py+ph)

	tw, th := r.Target(v)
	f.a = clamp01(1 - (math.Abs(tw-pw)+math.Abs(th-ph))/40)

	// No clip mask here: gg's masks make every text draw cost a full-surface
	// composite. Content is simply not drawn until the pill has (almost) its size.
	k := clamp01((ph - smallH) / 40)
	special := m != ModePermission && (v.shotOn() || v.X.Cmd != nil || v.X.Game != nil || v.X.Picker != nil || v.cardOn())
	if !special {
		f.robot(px+14+k*2, py+18+k*6, m, v.T, v.headColor(), v.Face, 1, v.X.Mood, peakAvg(v.X.Peaks))
		if n := unreadTotal(v.X.Unread); n > 0 && k < 0.5 {
			bx, by := px+14+22+1, py+18-9
			f.circle(bx, by, 6.5, rgb(0xff, 0x3b, 0x30))
			f.text(fmt.Sprint(min(n, 9)), fBold, 9.5, bx, by, 0.5, cWhite)
		}
	}

	if f.a > 0.3 {
		f.a = (f.a - 0.3) / 0.7
		switch {
		case m != ModePermission && v.X.Cmd != nil:
			f.drawCmd()
		case m != ModePermission && v.X.Game != nil && v.X.Game.On:
			f.drawGame()
		case m != ModePermission && v.X.Picker != nil:
			f.drawPicker()
		case m != ModePermission && v.cardOn():
			f.drawCard()
		case v.shotOn() && m != ModePermission:
			f.drawShot()
		case !expanded(v):
			f.drawCollapsed()
		case m == ModePermission && v.Snap.Perm != nil:
			f.drawPermission()
		default:
			f.drawExpanded()
		}
	}

	img, ok := dc.Image().(*image.RGBA)
	if !ok {
		img = image.NewRGBA(dc.Image().Bounds())
	}
	img.Rect = img.Rect.Add(image.Pt(x0, 0))
	return img, f.hits
}

func (f *frame) drawCollapsed() {
	v := f.v
	px, py, pw := f.px, f.py, f.pw
	cy := py + smallH/2
	tx := px + 48
	acc := accent(v.Snap.Mode)
	if v.volOn() {
		f.drawVolume()
		return
	}
	switch v.Snap.Mode {
	case ModeWorking:
		w := f.text(L(v.Snap.Text), fSemibold, 13.5, tx, cy, 0, cText)
		if v.Snap.Detail != "" {
			d := f.r.fit(fRegular, 13, v.Snap.Detail, pw-48-w-8-52)
			f.text(d, fRegular, 13, tx+w+8, cy, 0, cGray)
		}
		for i := 0; i < 3; i++ {
			a := 0.25 + 0.75*clamp01(math.Sin(v.T*6-float64(i)*0.9))
			f.circle(px+pw-38+float64(i)*8.5, cy, 2.6, withA(acc, a*f.a))
		}
		return
	case ModeDone:
		f.text(L(v.Snap.Text), fSemibold, 13.5, tx, cy, 0, cText)
		cx := px + pw - 26
		f.circle(cx, cy, 9, withA(acc, f.a))
		f.dc.SetLineWidth(2 * f.s)
		f.dc.SetColor(withA(cPill, f.a))
		f.dc.MoveTo((cx-4)*f.s, cy*f.s)
		f.dc.LineTo((cx-1)*f.s, (cy+3)*f.s)
		f.dc.LineTo((cx+4.2)*f.s, (cy-3)*f.s)
		f.dc.Stroke()
		return
	case ModeWaiting:
		w := f.text(L(v.Snap.Text), fSemibold, 13.5, tx, cy, 0, cText)
		if v.Snap.Detail != "" {
			d := f.r.fit(fRegular, 13, v.Snap.Detail, pw-48-w-8-18)
			f.text(d, fRegular, 13, tx+w+8, cy, 0, cGray)
		}
		return
	}
	if v.flashOn() {
		f.text(f.r.fit(fSemibold, 13.5, v.Flash.Text, pw-48-18), fSemibold, 13.5, tx, cy, 0, cText)
		return
	}
	if txt, lyric := v.musicLine(); txt != "" {
		k := fSemibold
		if !lyric {
			k = fRegular
		}
		t := f.r.fit(k, 13, txt, pw-48-46)
		if lyric && t == txt && !v.X.NoFill {
			l := v.Lyrics
			pos := v.Media.Position(v.Now) + lyricLead + v.LyricOffset
			f.textFill(t, k, 13, tx, cy, 0, lineProgress(l, l.LineAt(pos), pos), cText, cGray)
		} else {
			f.text(t, k, 13, tx, cy, 0, cText)
		}
		bc := cMusic
		if cc := v.X.CoverColor; cc != nil {
			bc = rgb(cc[0], cc[1], cc[2])
		}
		for i := 0; i < 3; i++ {
			pk := v.X.Peaks[i+1]
			h := 3 + 11*pk
			f.rr(px+pw-34+float64(i)*5.5, cy-h/2, 3, h, 1.5, withA(bc, f.a))
		}
		return
	}
	if v.AI.Generating {
		w := f.text(v.AI.Name+L(" rašo atsakymą"), fSemibold, 13.5, tx, cy, 0, cText)
		_ = w
		for i := 0; i < 3; i++ {
			a := 0.25 + 0.75*clamp01(math.Sin(v.T*6-float64(i)*0.9))
			f.circle(px+pw-38+float64(i)*8.5, cy, 2.6, withA(rgb(0x10, 0xa3, 0x7f), a*f.a))
		}
		return
	}
	if v.Timer.Active {
		pc := phaseColor(v.Timer.Phase)
		f.text(phaseName(v.Timer.Phase), fRegular, 13, tx, cy, 0, cGray)
		tw := f.text(v.Timer.Text, fSemibold, 14.5, px+pw-18, cy, 1, pc)
		rx := px + pw - 18 - tw - 16
		f.ring(rx, cy, 7, 2.4, withA(cBtn, f.a))
		if v.Timer.Progress > 0 {
			f.arc(rx, cy, 7, 2.4, -math.Pi/2, -math.Pi/2+2*math.Pi*v.Timer.Progress, withA(pc, f.a))
		}
		if !v.Timer.Running {
			f.text(L("pauzė"), fRegular, 11.5, rx-14, cy, 1, cDim)
		}
		return
	}
	tw := f.text(v.Now.Format("15:04"), fSemibold, 14.5, px+pw-18, cy, 1, cText)
	x := px + pw - 18 - tw - 10
	if v.X.DND {
		f.moon(x-6, cy, 5.5, withA(rgb(0xbf, 0x5a, 0xf2), f.a), cPill)
		x -= 20
	}
	if v.Weather.Has {
		ww := f.text(fmtTemp(v.Weather.Temp), fSemibold, 13.5, x, cy, 1, cGray)
		f.weatherIcon(v.Weather.Code, v.Weather.IsDay, x-ww-11, cy, 16, f.a)
		x -= ww + 26
	}
	if v.Usage.Has {
		f.ring(x-7, cy, 6.5, 2.2, withA(cBtn, f.a))
		f.arc(x-7, cy, 6.5, 2.2, -math.Pi/2, -math.Pi/2+2*math.Pi*v.Usage.Progress(v.Now), withA(accent(ModeWorking), f.a))
		x -= 22
	}
	if v.Sys.LowBattery() {
		f.text(fmt.Sprintf("%d%%", v.Sys.Battery), fSemibold, 13, x, cy, 1, cWork)
	}
}

func (f *frame) drawPermission() {
	v := f.v
	px, py, pw := f.px, f.py, f.pw
	tx := px + 48
	pr := v.Snap.Perm
	acc := accent(ModePermission)
	cy := py + 24
	w := f.text(L("Claude prašo leidimo"), fSemibold, 14.5, tx, cy, 0, cText)
	if v.Snap.PermCount > 1 {
		bx := tx + w + 8
		lbl := fmt.Sprintf("+%d", v.Snap.PermCount-1)
		lw := f.r.measure(fSemibold, 11.5, lbl)
		f.rr(bx, cy-9, lw+12, 18, 9, withA(cBtn, f.a))
		f.text(lbl, fSemibold, 11.5, bx+6, cy, 0, cGray)
	}
	if pr.Project != "" {
		f.text(f.r.fit(fRegular, 12.5, pr.Project, 130), fRegular, 12.5, px+pw-18, cy, 1, cGray)
	}
	bx, by, bw, bh := px+16, py+46, pw-32, 34.0
	f.rr(bx, by, bw, bh, 10, withA(cBox, f.a))
	tool := pr.Tool
	if tool == "" {
		tool = L("Veiksmas")
	}
	tool = f.r.fit(fSemibold, 12.5, tool, 120)
	tlw := f.text(tool, fSemibold, 12.5, bx+12, by+bh/2, 0, acc)
	f.text(f.r.fit(fMono, 12.5, pr.Detail, bw-12-tlw-12-12), fMono, 12.5, bx+12+tlw+10, by+bh/2, 0, cText)

	cyb := py + 116
	x1 := px + pw - 16
	f.button("allow", L("Leisti"), x1-104, cyb, 104, 34, true)
	f.button("deny", L("Uždrausti"), x1-104-8-104, cyb, 104, 34, false)

	left := permWait.Seconds() - v.Now.Sub(pr.Created).Seconds()
	lbl := fmt.Sprintf(L("Atsakyti terminale · %d s"), int(math.Max(0, math.Ceil(left))))
	tcol := cGray
	if v.HoverBtn == "terminal" {
		tcol = cText
	}
	lw := f.text(lbl, fRegular, 12.5, px+22, cyb, 0, tcol)
	f.hit("terminal", px+14, cyb-14, px+22+lw+8, cyb+14)
}

func (f *frame) drawExpanded() {
	v := f.v
	px, py, pw, ph := f.px, f.py, f.pw, f.ph
	tx := px + 48
	m := v.Snap.Mode

	// status row: Claude on the left, date + time on the right
	cy := py + 24
	status, detail, col := L(v.Snap.Text), v.Snap.Detail, cText
	if m == ModeIdle {
		col = cGray
		if v.HooksOn {
			status, detail = L("Claude Code laukia darbo"), ""
		} else {
			status, detail = L("Claude ryšys išjungtas"), L("(dešinys pelės mygtukas)")
		}
	}
	if v.flashOn() && (m == ModeIdle || m == ModeDone) {
		fc := v.Flash.Color
		status, detail, col = v.Flash.Text, "", rgb(fc[0], fc[1], fc[2])
	}
	if v.volOn() {
		f.statusVolume(cy)
		status, detail = "", ""
	} else if v.AI.Generating && m == ModeIdle && !v.flashOn() {
		status, detail, col = v.AI.Name+L(" rašo atsakymą…"), "", cText
	}
	tw := f.text(v.Now.Format("15:04"), fSemibold, 13.5, px+pw-18, cy, 1, cText)
	dw := f.text(compactDate(v),
		fRegular, 12.5, px+pw-18-tw-8, cy, 1, cGray)
	dw += f.statusIcons(px+pw-18-tw-dw-22, cy) + 8
	maxW := pw - 48 - 18 - tw - dw - 22
	w := f.text(f.r.fit(fSemibold, 13.5, status, maxW), fSemibold, 13.5, tx, cy, 0, col)
	if detail != "" {
		f.text(f.r.fit(fRegular, 13, detail, maxW-w-8), fRegular, 13, tx+w+8, cy, 0, cGray)
	}

	switch v.Tab {
	case tabMusic:
		f.tabMusic()
	case tabWeather:
		f.tabWeather()
	case tabLinks:
		f.tabLinks()
	case tabTimer:
		f.tabTimer()
	case tabClips:
		f.tabClips()
	case tabNote:
		f.tabNote()
	case tabSystem:
		f.tabSystem()
	case tabRemind:
		f.tabPlan()
	case tabShelf:
		f.tabShelf()
	default:
		f.tabHome()
	}
	f.tabBar(py + ph - 22)
}

// tabBar: active tab shows icon + name, the others only an icon.
func (f *frame) tabBar(cy float64) {
	v := f.v
	const iconW, gap = 30.0, 3.0
	label := L(tabNames[v.Tab])
	activeW := 14 + 14 + 6 + f.r.measure(fSemibold, 12, label) + 14
	total := activeW + float64(len(tabNames)-1)*iconW + float64(len(tabNames)-1)*gap
	x := f.px + (f.pw-total)/2
	for i := range tabNames {
		id := fmt.Sprintf("tab:%d", i)
		w := iconW
		tc := tabColor(i)
		if i == v.Tab {
			w = activeW
			f.rr(x, cy-14, w, 28, 14, withA(tc, 0.2*f.a))
			f.tabIcon(i, x+14+7, cy, withA(tc, f.a))
			f.text(label, fSemibold, 12, x+14+14+6, cy, 0, tc)
		} else {
			ia := 0.7
			if v.HoverBtn == id {
				f.rr(x, cy-14, w, 28, 14, withA(cBox, f.a))
				ia = 1
			}
			f.tabIcon(i, x+w/2, cy, withA(tc, ia*f.a))
		}
		f.hit(id, x, cy-15, x+w, cy+15)
		x += w + gap
	}
}

// tabIcon draws a 14px line icon centered at (cx, cy).
func (f *frame) tabIcon(i int, cx, cy float64, c color.NRGBA) {
	s := f.s
	dc := f.dc
	dc.SetColor(c)
	dc.SetLineWidth(1.5 * s)
	dc.SetLineJoin(gg.LineJoinRound)
	dc.SetLineCap(gg.LineCapRound)
	defer dc.SetLineCap(gg.LineCapButt)
	L := func(x0, y0, x1, y1 float64) {
		dc.DrawLine((cx+x0)*s, (cy+y0)*s, (cx+x1)*s, (cy+y1)*s)
		dc.Stroke()
	}
	R := func(x, y, w, h, r float64) {
		dc.NewSubPath()
		dc.DrawRoundedRectangle((cx+x)*s, (cy+y)*s, w*s, h*s, r*s)
		dc.Stroke()
	}
	switch i {
	case tabHome:
		dc.NewSubPath()
		dc.MoveTo((cx-6.5)*s, (cy-0.5)*s)
		dc.LineTo(cx*s, (cy-6.5)*s)
		dc.LineTo((cx+6.5)*s, (cy-0.5)*s)
		dc.Stroke()
		R(-4.5, -2, 9, 8, 1.5)
	case tabMusic:
		dc.NewSubPath()
		dc.DrawCircle((cx-3)*s, (cy+4)*s, 2.4*s)
		dc.Fill()
		dc.SetColor(c)
		L(-0.8, 4, -0.8, -6)
		L(-0.8, -6, 5, -4)
		L(5, -4, 5, -1.5)
	case tabTimer:
		dc.NewSubPath()
		dc.DrawCircle(cx*s, (cy+1)*s, 6*s)
		dc.Stroke()
		L(0, 1, 0, -2.5)
		L(-2, -6.8, 2, -6.8)
	case tabClips:
		R(-5, -5, 10, 12, 2)
		dc.NewSubPath()
		dc.DrawRoundedRectangle((cx-2.5)*s, (cy-7)*s, 5*s, 3.4*s, 1*s)
		dc.Fill()
	case tabNote:
		R(-5, -6.5, 10, 13, 2)
		L(-2.5, -2.5, 2.5, -2.5)
		L(-2.5, 0.5, 2.5, 0.5)
		L(-2.5, 3.5, 0.5, 3.5)
	case tabSystem:
		R(-6.5, -4, 11.5, 8, 2)
		L(6.5, -1.5, 6.5, 1.5)
		dc.NewSubPath()
		dc.DrawRoundedRectangle((cx-4.5)*s, (cy-2)*s, 5*s, 4*s, 0.8*s)
		dc.Fill()
	case tabWeather:
		dc.NewSubPath()
		dc.DrawCircle((cx-2.5)*s, (cy-2.5)*s, 3.6*s)
		dc.Stroke()
		R(-5, 0, 11.5, 5.5, 2.75)
	case tabLinks:
		f.gridIcon(cx+0.5, cy+0.5, c)
	case tabRemind: // bell
		dc.NewSubPath()
		dc.MoveTo((cx-5.5)*s, (cy+3.5)*s)
		dc.LineTo((cx-4.5)*s, (cy-1)*s)
		dc.DrawArc(cx*s, (cy-1)*s, 4.5*s, math.Pi, 2*math.Pi)
		dc.LineTo((cx+5.5)*s, (cy+3.5)*s)
		dc.ClosePath()
		dc.Stroke()
		L(-1.5, 6, 1.5, 6)
	case tabShelf: // tray
		dc.NewSubPath()
		dc.MoveTo((cx-6.5)*s, (cy+0.5)*s)
		dc.LineTo((cx-4.5)*s, (cy-5)*s)
		dc.LineTo((cx+4.5)*s, (cy-5)*s)
		dc.LineTo((cx+6.5)*s, (cy+0.5)*s)
		dc.Stroke()
		R(-6.5, 0.5, 13, 5.5, 1.5)
		L(-2.5, 0.5, -1.5, 2.5)
		L(-1.5, 2.5, 1.5, 2.5)
		L(1.5, 2.5, 2.5, 0.5)
	case tabRobot:
		R(-6, -3.5, 12, 9, 3)
		L(0, -3.5, 0, -6)
		dc.NewSubPath()
		dc.DrawCircle((cx-2.5)*s, (cy+1)*s, 1.2*s)
		dc.DrawCircle((cx+2.5)*s, (cy+1)*s, 1.2*s)
		dc.Fill()
	}
}

// card draws a gradient card like a phone widget and registers a hit.
// Gradients are slow in gg, so each card image is rendered once and cached.
func (f *frame) card(name string, x, y, w, h float64, c1, c2 color.NRGBA) {
	hover := f.v.HoverBtn == name
	f.gradientRect(x, y, w, h, 16, c1, c2, hover)
	f.hit(name, x, y, x+w, y+h)
}

func (f *frame) gradientRect(x, y, w, h, rad float64, c1, c2 color.NRGBA, hover bool) {
	key := fmt.Sprintf("g%v%v%.0f%.0f%.0f%v%.2f", c1, c2, w, h, rad, hover, f.s)
	img, ok := f.r.cache[key]
	if !ok {
		W, H := int(math.Ceil(w*f.s)), int(math.Ceil(h*f.s))
		dc := gg.NewContext(W, H)
		g := gg.NewLinearGradient(0, 0, float64(W), float64(H))
		g.AddColorStop(0, c1)
		g.AddColorStop(1, c2)
		dc.DrawRoundedRectangle(0, 0, float64(W), float64(H), rad*f.s)
		dc.SetFillStyle(g)
		dc.Fill()
		if hover {
			dc.DrawRoundedRectangle(0, 0, float64(W), float64(H), rad*f.s)
			dc.SetColor(color.NRGBA{255, 255, 255, 22})
			dc.Fill()
		}
		img = dc.Image()
		if len(f.r.cache) > 64 {
			f.r.cache = map[string]image.Image{}
		}
		f.r.cache[key] = img
	}
	f.blitImage(img, x, y)
}

// solidRect draws a cached filled rounded rectangle (the big pill background).
func (f *frame) solidRect(x, y, w, h, rad float64, c color.NRGBA) {
	W, H := int(math.Round(w*f.s)), int(math.Round(h*f.s))
	key := fmt.Sprintf("s%v%d%d%.1f", c, W, H, rad)
	img, ok := f.r.cache[key]
	if !ok {
		dc := gg.NewContext(W, H)
		dc.DrawRoundedRectangle(0, 0, float64(W), float64(H), rad*f.s)
		dc.SetColor(c)
		dc.Fill()
		img = dc.Image()
		if len(f.r.cache) > 64 {
			f.r.cache = map[string]image.Image{}
		}
		f.r.cache[key] = img
	}
	f.blitImage(img, x, y)
}

// blitImage copies a cached image at logical (x, y); much faster than gg's
// DrawImage, which resamples through a bilinear filter.
func (p *painter) blitImage(img image.Image, x, y float64) {
	dst, ok := p.dc.Image().(*image.RGBA)
	if !ok {
		return
	}
	pt := image.Pt(int(math.Round(x*p.s-p.ox)), int(math.Round(y*p.s)))
	r := image.Rectangle{pt, pt.Add(img.Bounds().Size())}
	draw.Draw(dst, r, img, img.Bounds().Min, draw.Over)
}

func (f *frame) playIcon(cx, cy float64, playing bool, c color.NRGBA) {
	if playing {
		f.rr(cx-4.6, cy-5.5, 3.2, 11, 1, c)
		f.rr(cx+1.4, cy-5.5, 3.2, 11, 1, c)
	} else {
		f.poly(c, cx-3.5, cy-6, cx+6, cy, cx-3.5, cy+6)
	}
}

var cWhite70 = color.NRGBA{255, 255, 255, 185}

func (f *frame) tabHome() {
	v := f.v
	px, py, pw := f.px, f.py, f.pw
	y, h := py+46, 102.0
	gap := 8.0
	cw := (pw - 32 - 2*gap) / 3
	x1, x2, x3 := px+16, px+16+cw+gap, px+16+2*(cw+gap)

	// Claude card
	f.card(fmt.Sprintf("tab:%d", tabSystem), x1, y, cw, h, rgb(0x5a, 0x2a, 0x10), rgb(0xb8, 0x5a, 0x1e))
	f.sparkle(x1+18, y+16, 6, withA(rgb(0xff, 0xd6, 0x0a), f.a))
	f.text(f.r.fit(fSemibold, 11.5, "Claude", cw-62), fSemibold, 11.5, x1+30, y+16, 0, cWhite70)
	rx, ry := x1+cw-20, y+20
	f.ring(rx, ry, 9, 2.6, color.NRGBA{255, 255, 255, uint8(60 * f.a)})
	if v.Usage.Has {
		f.arc(rx, ry, 9, 2.6, -math.Pi/2, -math.Pi/2+2*math.Pi*v.Usage.Progress(v.Now), withA(cWhite, f.a))
		f.text(v.Usage.LeftText(v.Now), fBold, 19, x1+12, y+62, 0, cWhite)
		f.text(L("iki atsinaujinimo"), fRegular, 11.5, x1+12, y+84, 0, cWhite70)
	} else {
		f.text(L("5 val."), fBold, 19, x1+12, y+62, 0, cWhite)
		f.text(f.r.fit(fRegular, 11.5, L("visas limitas laisvas"), cw-24), fRegular, 11.5, x1+12, y+84, 0, cWhite70)
	}

	// Music card
	f.card(fmt.Sprintf("tab:%d", tabMusic), x2, y, cw, h, rgb(0x6e, 0x10, 0x24), rgb(0xd0, 0x2a, 0x4c))
	f.note(x2+17, y+16, withA(rgb(0xff, 0xb3, 0xc6), f.a))
	f.text(f.r.fit(fSemibold, 11.5, L("Muzika"), cw-66), fSemibold, 11.5, x2+30, y+16, 0, cWhite70)
	m := v.Media
	if m != nil && m.Title != "" {
		f.text(f.r.fit(fBold, 14.5, m.Title, cw-24), fBold, 14.5, x2+12, y+62, 0, cWhite)
		f.text(f.r.fit(fRegular, 11.5, m.Artist, cw-24), fRegular, 11.5, x2+12, y+84, 0, cWhite70)
	} else {
		f.text(L("Niekas negroja"), fSemibold, 13, x2+12, y+62, 0, cWhite)
		f.text(L("Paleisk Spotify ar YouTube"), fRegular, 11, x2+12, y+84, 0, cWhite70)
	}
	bx, by := x2+cw-20, y+20
	f.iconButton("play", bx, by, 12, true, func(c color.NRGBA) {
		f.dc.Push()
		f.playIcon(bx-0.5, by, m.Playing(), withA(rgb(0xb0, 0x20, 0x40), f.a))
		f.dc.Pop()
	})

	// Focus card
	f.card(fmt.Sprintf("tab:%d", tabTimer), x3, y, cw, h, rgb(0x2e, 0x1a, 0x6e), rgb(0x8a, 0x3c, 0xd8))
	f.stopwatch(x3+18, y+16, withA(rgb(0xd8, 0xc0, 0xff), f.a))
	f.text(f.r.fit(fSemibold, 11.5, L("Laikmatis"), cw-66), fSemibold, 11.5, x3+30, y+16, 0, cWhite70)
	f.text(v.Timer.Text, fBold, 22, x3+12, y+62, 0, cWhite)
	st := L("Paruošta")
	if v.Timer.Running {
		st = phaseName(v.Timer.Phase) + L(" · vyksta")
	} else if v.Timer.Active {
		st = L("Pauzė")
	}
	f.text(st, fRegular, 11.5, x3+12, y+84, 0, cWhite70)
	tx, ty := x3+cw-20, y+20
	f.iconButton("timer:toggle", tx, ty, 12, true, func(c color.NRGBA) {
		f.playIcon(tx-0.5, ty, v.Timer.Running, withA(rgb(0x6a, 0x30, 0xb8), f.a))
	})
}

func (f *frame) mediaButtons(cy, right float64, playing bool) {
	nx, plx, pvx := right-17, right-17-40, right-17-80
	f.iconButton("next", nx, cy, 15, false, func(c color.NRGBA) {
		f.poly(c, nx-5.5, cy-4.5, nx+0.8, cy, nx-5.5, cy+4.5)
		f.rr(nx+1.3, cy-4.5, 1.9, 9, 0.9, c)
	})
	f.iconButton("play", plx, cy, 19, true, func(c color.NRGBA) {
		f.playIcon(plx, cy, playing, c)
	})
	f.iconButton("prev", pvx, cy, 15, false, func(c color.NRGBA) {
		f.poly(c, pvx+5.5, cy-4.5, pvx-0.8, cy, pvx+5.5, cy+4.5)
		f.rr(pvx-3.2, cy-4.5, 1.9, 9, 0.9, c)
	})
}

func trackHue(s string) (color.NRGBA, color.NRGBA) {
	var h uint32 = 2166136261
	for _, b := range []byte(s) {
		h = (h ^ uint32(b)) * 16777619
	}
	pal := [][2]color.NRGBA{
		{rgb(0xff, 0x37, 0x5f), rgb(0x8e, 0x1a, 0x52)},
		{rgb(0x5e, 0x5c, 0xe6), rgb(0x1e, 0x3a, 0x8a)},
		{rgb(0x30, 0xd1, 0x58), rgb(0x0f, 0x5e, 0x4a)},
		{rgb(0xff, 0x9f, 0x0a), rgb(0x9a, 0x2c, 0x12)},
		{rgb(0x64, 0xd2, 0xff), rgb(0x1d, 0x4e, 0x89)},
		{rgb(0xbf, 0x5a, 0xf2), rgb(0x4a, 0x1d, 0x7a)},
	}
	p := pal[h%uint32(len(pal))]
	return p[0], p[1]
}

func (f *frame) bar(label string, cy, frac float64, value string, c color.NRGBA) {
	px, pw := f.px, f.pw
	f.text(label, fRegular, 12.5, px+20, cy, 0, cGray)
	x0, x1 := px+108, px+pw-120
	f.rr(x0, cy-3, x1-x0, 6, 3, withA(cBtn, f.a))
	if frac > 0 {
		f.rr(x0, cy-3, math.Max(6, (x1-x0)*clamp01(frac)), 6, 3, withA(c, f.a))
	}
	f.text(value, fSemibold, 12.5, px+pw-20, cy, 1, cText)
}

func (f *frame) tabTimer() {
	v := f.v
	t := v.Timer
	px, py, pw := f.px, f.py, f.pw
	pc := phaseColor(t.Phase)
	f.text(t.Text, fBold, 36, px+20, py+78, 0, cText)
	lbl := phaseName(t.Phase)
	if t.Rounds > 0 {
		lbl += fmt.Sprintf(" · atlikta %d", t.Rounds)
	}
	if t.Active && !t.Running {
		lbl += L(" · pauzė")
	}
	f.text(lbl, fSemibold, 12.5, px+21, py+106, 0, pc)
	bw := 200.0
	f.rr(px+20, py+120, bw, 4, 2, withA(cBtn, f.a))
	if t.Progress > 0 {
		f.rr(px+20, py+120, math.Max(4, bw*t.Progress), 4, 2, withA(pc, f.a))
	}

	mcy := py + 90
	tx, rx := px+pw-42, px+pw-92
	f.iconButton("timer:toggle", tx, mcy, 22, true, func(c color.NRGBA) {
		if t.Running {
			f.rr(tx-5.5, mcy-6.5, 3.6, 13, 1.2, c)
			f.rr(tx+1.9, mcy-6.5, 3.6, 13, 1.2, c)
		} else {
			f.poly(c, tx-4, mcy-7, tx+7, mcy, tx-4, mcy+7)
		}
	})
	f.iconButton("timer:reset", rx, mcy, 17, false, func(c color.NRGBA) {
		f.arc(rx, mcy, 6, 1.8, -math.Pi*0.35, math.Pi*1.35, c)
		ax, ay := rx+6*math.Cos(-math.Pi*0.35), mcy+6*math.Sin(-math.Pi*0.35)
		f.poly(c, ax-1, ay-4.2, ax+3.6, ay+0.6, ax-2.6, ay+2)
	})
	f.button("timer:preset", t.Preset, px+pw-178, mcy, 62, 30, false)
	f.text(L("min"), fRegular, 11, px+pw-147, mcy+24, 0.5, cDim)
	f.focusStats(py + 150)
}

func (f *frame) tabClips() {
	v := f.v
	px, py, pw := f.px, f.py, f.pw
	if len(v.Clips) == 0 {
		f.text(L("Nukopijuok tekstą (Ctrl+C) – jis atsiras čia"), fRegular, 13, px+pw/2, py+80, 0.5, cGray)
		f.text(L("Saugoma tik atmintyje, ne diske"), fRegular, 11.5, px+pw/2, py+102, 0.5, cDim)
		return
	}
	y0 := py + 44
	for i := 0; i < 3; i++ {
		idx := v.ClipScroll + i
		if idx >= len(v.Clips) {
			break
		}
		id := fmt.Sprintf("clip:%d", idx)
		y := y0 + float64(i)*27
		bg := cBox
		if v.HoverBtn == id {
			bg = cBtnHi
		}
		f.rr(px+16, y, pw-32, 24, 8, withA(bg, f.a))
		f.text(fmt.Sprintf("%d", idx+1), fSemibold, 11.5, px+28, y+12, 0.5, cDim)
		f.text(f.r.fit(fRegular, 12.5, oneLine(v.Clips[idx]), pw-32-36-14), fRegular, 12.5, px+40, y+12, 0, cText)
		f.hit(id, px+16, y, px+pw-16, y+24)
	}
	cy := py + 134
	info := L("Paspausk eilutę – nukopijuos vėl")
	if len(v.Clips) > 3 {
		info = fmt.Sprintf(L("%d–%d iš %d · slink ratuku"), v.ClipScroll+1, min(v.ClipScroll+3, len(v.Clips)), len(v.Clips))
	}
	f.text(info, fRegular, 12, px+20, cy, 0, cDim)
	c := cGray
	if v.HoverBtn == "clip:clear" {
		c = cText
	}
	lw := f.text(L("Išvalyti"), fSemibold, 12.5, px+pw-20, cy, 1, c)
	f.hit("clip:clear", px+pw-20-lw-8, cy-12, px+pw-12, cy+12)
}

func (f *frame) tabNote() {
	v := f.v
	px, py, pw := f.px, f.py, f.pw
	bx, by, bw, bh := px+16, py+44, pw-32, 72.0
	f.rr(bx, by, bw, bh, 10, withA(cBox, f.a))
	if strings.TrimSpace(v.Note) == "" {
		f.text(L("Užrašas tuščias. Spausk „Atidaryti“ ir rašyk."), fRegular, 12.5, bx+12, by+bh/2, 0, cDim)
	} else {
		lines := f.r.wrap(fRegular, 12.5, v.Note, bw-24, 3)
		for i, l := range lines {
			f.text(l, fRegular, 12.5, bx+12, by+16+float64(i)*20, 0, cText)
		}
	}
	cy := py + 136
	f.button("note:open", L("Atidaryti"), px+pw-16-104, cy, 104, 28, true)
	f.button("note:paste", L("+ Iš iškarpinės"), px+pw-16-104-8-128, cy, 128, 28, false)
	f.text("uzrasas.txt", fRegular, 11.5, px+20, cy, 0, cDim)
}

func (f *frame) tabRobot() {
	v := f.v
	px, py := f.px, f.py
	cy := py + 66
	f.text(L("Spalva"), fSemibold, 12.5, px+20, cy, 0, cGray)
	for i, c := range robotColors {
		cx := px + 112 + float64(i)*34
		id := fmt.Sprintf("color:%d", i)
		if i == 0 {
			cols := []color.NRGBA{accent(ModeWorking), accent(ModePermission), accent(ModeDone), accent(ModeWaiting)}
			for q, qc := range cols {
				a0 := -math.Pi/2 + float64(q)*math.Pi/2
				f.dc.NewSubPath()
				f.dc.MoveTo(cx*f.s, cy*f.s)
				f.dc.DrawArc(cx*f.s, cy*f.s, 11*f.s, a0, a0+math.Pi/2)
				f.dc.ClosePath()
				f.dc.SetColor(withA(qc, f.a))
				f.dc.Fill()
			}
		} else {
			f.circle(cx, cy, 11, withA(rgb(c.R, c.G, c.B), f.a))
		}
		if i == v.RobotColor {
			f.ring(cx, cy, 14.5, 2, withA(cText, f.a))
		} else if v.HoverBtn == id {
			f.ring(cx, cy, 14.5, 1.5, withA(cDim, f.a))
		}
		f.hit(id, cx-15, cy-15, cx+15, cy+15)
	}
	cy = py + 118
	f.text(L("Veidukas"), fSemibold, 12.5, px+20, cy, 0, cGray)
	for i := range robotFaces {
		x := px + 100 + float64(i)*74
		id := fmt.Sprintf("face:%d", i)
		bg := cBox
		if v.HoverBtn == id {
			bg = cBtnHi
		}
		f.rr(x, cy-21, 66, 42, 12, withA(bg, f.a))
		if i == v.Face {
			f.dc.NewSubPath()
			f.dc.DrawRoundedRectangle(x*f.s, (cy-21)*f.s, 66*f.s, 42*f.s, 12*f.s)
			f.dc.SetLineWidth(2 * f.s)
			f.dc.SetColor(withA(cText, f.a))
			f.dc.Stroke()
		}
		f.robot(x+22, cy+3, ModeIdle, f.v.T, f.v.headColor(), i, f.a, moodNormal, 0)
		f.hit(id, x, cy-21, x+66, cy+21)
	}
}

// Anim moves the pill size with a light spring, so it "pops" like an iPhone.
type Anim struct{ W, H, vw, vh float64 }

// Step advances by dt seconds. Returns true while still moving.
func (a *Anim) Step(tw, th, dt float64) bool {
	if dt > 0.05 {
		dt = 0.05
	}
	const k, c = 330.0, 27.0 // a bit under critical damping (36) -> small overshoot
	a.vw += (k*(tw-a.W) - c*a.vw) * dt
	a.vh += (k*(th-a.H) - c*a.vh) * dt
	a.W += a.vw * dt
	a.H += a.vh * dt
	moving := false
	if math.Abs(tw-a.W) < 0.3 && math.Abs(a.vw) < 3 {
		a.W, a.vw = tw, 0
	} else {
		moving = true
	}
	if math.Abs(th-a.H) < 0.3 && math.Abs(a.vh) < 3 {
		a.H, a.vh = th, 0
	} else {
		moving = true
	}
	return moving
}

// Pop gives the island a little bounce (new notification).
func (a *Anim) Pop() { a.vw += 150; a.vh += 70 }

// RenderIcon draws the app icon (robot on a dark rounded square).
func RenderIcon(size int) image.Image {
	dc := gg.NewContext(size, size)
	s := float64(size) / 32
	dc.DrawRoundedRectangle(0, 0, float64(size), float64(size), 8*s)
	dc.SetColor(cPill)
	dc.Fill()
	dc.SetColor(rgb(0xff, 0x9f, 0x0a))
	dc.DrawCircle(16*s, 6.5*s, 1.9*s)
	dc.Fill()
	dc.SetLineWidth(1.4 * s)
	dc.SetColor(cDim)
	dc.DrawLine(16*s, 8*s, 16*s, 10*s)
	dc.Stroke()
	dc.DrawRoundedRectangle(5*s, 10*s, 22*s, 17*s, 5.5*s)
	dc.SetColor(rgb(0xff, 0x9f, 0x0a))
	dc.Fill()
	dc.SetColor(cPill)
	dc.DrawRoundedRectangle(11.2*s, 15.6*s, 3.6*s, 5.6*s, 1.7*s)
	dc.Fill()
	dc.DrawRoundedRectangle(17.2*s, 15.6*s, 3.6*s, 5.6*s, 1.7*s)
	dc.Fill()
	return dc.Image()
}
