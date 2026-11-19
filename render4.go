package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

// ---------- extra view state ----------

type Card struct {
	Title   string
	Body    string
	Color   [3]uint8
	Buttons []CardBtn
	Swatch  *[3]uint8 // colour picker result
	Until   time.Time
}

type CardBtn struct{ ID, Label string }

type CmdView struct {
	Input   string
	Results []CmdResult
	Sel     int
}

type PickerView struct {
	Color [3]uint8
}

type UnreadView struct {
	App string
	N   int
}

type BTView struct {
	Name string
	Pct  int
}

type FocusView struct {
	Today float64
	Week  []float64
}

type PlanRow struct {
	Kind  string // "event", "remind", "bday", "todo"
	ID    int
	When  string
	Text  string
	Extra string
	Done  bool
}

type Extra struct {
	Peaks      [5]float64
	CoverColor *[3]uint8
	Fav        bool
	SleepLeft  string
	Plan       []PlanRow
	PlanScroll int
	Focus      FocusView
	DND        bool
	Clocks     []ClockView
	Card       *Card
	Cmd        *CmdView
	Game       *Game
	Picker     *PickerView
	Mic, Cam   string
	Bright     float64 // -1 unknown
	Unread     []UnreadView
	BT         []BTView
	Mood       int
	SysScroll  int
	Caret      bool
	NoFill     bool
}

func (v View) cardOn() bool { return v.X.Card != nil && v.Now.Before(v.X.Card.Until) }

func unreadTotal(u []UnreadView) int {
	n := 0
	for _, x := range u {
		n += x.N
	}
	return n
}

// ---------- karaoke-style filled text ----------

// textFill draws s and colours the first frac of it (left to right).
func (f *frame) textFill(s string, k fontKind, size, cx, cy, ax, frac float64, on, off color.NRGBA) float64 {
	w := f.r.measure(k, size, s)
	if s == "" {
		return w
	}
	onC, offC := withA(on, f.a), withA(off, f.a)
	onC.A = uint8(min(255, (int(onC.A)+8)/16*16))
	offC.A = uint8(min(255, (int(offC.A)+8)/16*16))
	imgOff, asc := f.r.textImage(s, k, size, offC)
	imgOn, _ := f.r.textImage(s, k, size, onC)
	base := cy + size*0.36
	x := (cx-ax*w)*f.s - f.ox - 2
	y := base*f.s - float64(asc)
	cut := int(2 + frac*w*f.s)
	b := imgOn.Bounds()
	if cut < b.Dx() {
		f.blitDevice(imgOff.SubImage(image.Rect(cut, 0, b.Dx(), b.Dy())), x+float64(cut), y)
	}
	if cut > 0 {
		f.blitDevice(imgOn.SubImage(image.Rect(0, 0, min(cut, b.Dx()), b.Dy())), x, y)
	}
	return w
}

// ---------- icons ----------

func (p *painter) heart(cx, cy, r float64, filled bool, c color.NRGBA) {
	p.dc.NewSubPath()
	p.dc.MoveTo(cx*p.s, (cy+r)*p.s)
	p.dc.CubicTo((cx-r*1.6)*p.s, (cy-r*0.2)*p.s, (cx-r*0.8)*p.s, (cy-r*1.4)*p.s, cx*p.s, (cy-r*0.45)*p.s)
	p.dc.CubicTo((cx+r*0.8)*p.s, (cy-r*1.4)*p.s, (cx+r*1.6)*p.s, (cy-r*0.2)*p.s, cx*p.s, (cy+r)*p.s)
	p.dc.ClosePath()
	p.dc.SetColor(c)
	if filled {
		p.dc.Fill()
	} else {
		p.dc.SetLineWidth(1.5 * p.s)
		p.dc.Stroke()
	}
}

func (p *painter) moon(cx, cy, r float64, c, bg color.NRGBA) {
	p.circle(cx, cy, r, c)
	p.circle(cx+r*0.5, cy-r*0.35, r*0.85, bg)
}

func (p *painter) sun(cx, cy, r float64, c color.NRGBA) {
	p.circle(cx, cy, r*0.55, c)
	p.dc.SetLineWidth(1.5 * p.s)
	p.dc.SetColor(c)
	for i := 0; i < 8; i++ {
		a := float64(i) * math.Pi / 4
		p.dc.DrawLine((cx+math.Cos(a)*r*0.85)*p.s, (cy+math.Sin(a)*r*0.85)*p.s, (cx+math.Cos(a)*r*1.25)*p.s, (cy+math.Sin(a)*r*1.25)*p.s)
		p.dc.Stroke()
	}
}

func (p *painter) magnifier(cx, cy float64, c color.NRGBA) {
	p.ring(cx-1.5, cy-1.5, 5, 1.8, c)
	p.dc.SetLineWidth(2 * p.s)
	p.dc.SetColor(c)
	p.dc.DrawLine((cx+2)*p.s, (cy+2)*p.s, (cx+6)*p.s, (cy+6)*p.s)
	p.dc.Stroke()
}

func (p *painter) checkbox(x, cy float64, done bool, c color.NRGBA) {
	if done {
		p.rr(x, cy-7, 14, 14, 4, withA(cRest, p.a))
		p.dc.SetLineWidth(2 * p.s)
		p.dc.SetColor(withA(cWhite, p.a))
		p.dc.MoveTo((x+3.5)*p.s, cy*p.s)
		p.dc.LineTo((x+6)*p.s, (cy+2.8)*p.s)
		p.dc.LineTo((x+10.5)*p.s, (cy-3)*p.s)
		p.dc.Stroke()
		return
	}
	p.dc.NewSubPath()
	p.dc.DrawRoundedRectangle(x*p.s, (cy-7)*p.s, 14*p.s, 14*p.s, 4*p.s)
	p.dc.SetLineWidth(1.6 * p.s)
	p.dc.SetColor(c)
	p.dc.Stroke()
}

// ---------- command bar ----------

func cmdIconColor(kind string) color.NRGBA {
	switch kind {
	case "calc":
		return rgb(0xff, 0x9f, 0x0a)
	case "money", "unit":
		return rgb(0x30, 0xd1, 0x58)
	case "app":
		return rgb(0x0a, 0x84, 0xff)
	case "web":
		return rgb(0x64, 0xd2, 0xff)
	case "remind":
		return rgb(0xff, 0x9f, 0x0a)
	case "todo":
		return rgb(0xbf, 0x5a, 0xf2)
	case "link":
		return rgb(0xff, 0x37, 0x5f)
	case "trans":
		return rgb(0x5e, 0x5c, 0xe6)
	}
	return rgb(0x8e, 0x8e, 0x93)
}

func cmdIconLetter(kind string) string {
	return map[string]string{"calc": "=", "money": "€", "unit": "~", "app": ">", "web": "G", "remind": "!", "todo": "+", "link": "#", "trans": "A", "cmd": "/"}[kind]
}

func cmdHeight(c *CmdView) float64 { return 58 + float64(len(c.Results))*34 + 6 }

func (f *frame) drawCmd() {
	v := f.v
	c := v.X.Cmd
	px, py, pw := f.px, f.py, f.pw
	cy := py + 29
	f.rr(px+12, py+10, pw-24, 38, 19, withA(cBox, f.a))
	f.magnifier(px+34, cy, withA(cGray, f.a))
	tx := px + 50
	if c.Input == "" {
		f.text(L("Rašyk skaičiavimą, programą ar komandą…"), fRegular, 14, tx, cy, 0, cDim)
		if v.X.Caret {
			f.rr(tx, cy-9, 1.8, 18, 0.9, withA(cText, f.a))
		}
	} else {
		in := c.Input
		maxW := pw - 50 - 30
		for f.r.measure(fSemibold, 14.5, in) > maxW && utf8.RuneCountInString(in) > 1 {
			_, n := utf8.DecodeRuneInString(in)
			in = in[n:]
		}
		w := f.text(in, fSemibold, 14.5, tx, cy, 0, cText)
		if v.X.Caret {
			f.rr(tx+w+1, cy-9, 1.8, 18, 0.9, withA(cText, f.a))
		}
	}
	for i, r := range c.Results {
		y := py + 56 + float64(i)*34
		id := fmt.Sprintf("cmdsel:%d", i)
		if i == c.Sel || v.HoverBtn == id {
			f.rr(px+10, y, pw-20, 32, 10, withA(cBtn, f.a))
		}
		ic := cmdIconColor(r.Icon)
		f.circle(px+30, y+16, 11, withA(ic, f.a))
		f.text(cmdIconLetter(r.Icon), fBold, 11.5, px+30, y+16, 0.5, cWhite)
		sw := 0.0
		if r.Sub != "" {
			sub := f.r.fit(fRegular, 11.5, r.Sub, 150)
			sw = f.text(sub, fRegular, 11.5, px+pw-22, y+16, 1, cGray) + 12
		}
		f.text(f.r.fit(fSemibold, 13, r.Title, pw-60-sw-22), fSemibold, 13, px+48, y+16, 0, cText)
		f.hit(id, px+10, y, px+pw-10, y+32)
	}
}

// ---------- game ----------

func (f *frame) drawGame() {
	v := f.v
	g := v.X.Game
	px, py, pw := f.px, f.py, f.pw
	ground := py + 116
	f.rr(px+16, ground, pw-32, 2, 1, withA(cBtn, f.a))
	for _, x := range g.Obstacles {
		f.rr(px+x, ground-18, 14, 18, 3, withA(rgb(0xff, 0x45, 0x3a), f.a))
	}
	m := ModeWorking
	if g.Over {
		m = ModeWaiting
	}
	f.robot(px+40, ground-9-g.Y, m, v.T, v.headColor(), v.Face, f.a, moodNormal, 0)
	f.text(fmt.Sprintf(L("Taškai %d · Rekordas %d"), int(g.Score), g.Best), fSemibold, 12.5, px+pw-20, py+24, 1, cText)
	f.text(L("Tarpas arba spustelk – šokti · Esc – išeiti"), fRegular, 11.5, px+20, py+24, 0, cDim)
	if g.Over {
		f.text(L("Pralaimėjai! Spustelk ir bandyk dar kartą"), fSemibold, 14, px+pw/2, py+70, 0.5, cText)
	}
	f.hit("game:jump", px, py, px+pw, py+f.ph)
}

// ---------- cards (fact of the day, translation, birthday, colour, …) ----------

func (r *Renderer) cardSize(c *Card) (float64, float64) {
	lines := r.wrap(fRegular, 12.5, c.Body, 420-32-func() float64 {
		if c.Swatch != nil {
			return 56
		}
		return 0
	}(), 4)
	h := 48 + float64(len(lines))*18
	if len(c.Buttons) > 0 {
		h += 36
	}
	if c.Swatch != nil && h < 110 {
		h = 110
	}
	return 420, h
}

func (f *frame) drawCard() {
	v := f.v
	c := v.X.Card
	px, py, pw := f.px, f.py, f.pw
	col := rgb(c.Color[0], c.Color[1], c.Color[2])
	x := px + 18
	if c.Swatch != nil {
		sc := rgb(c.Swatch[0], c.Swatch[1], c.Swatch[2])
		f.rr(px+16, py+16, 44, 44, 12, withA(sc, f.a))
		x = px + 72
	} else {
		f.circle(px+24, py+24, 5, withA(col, f.a))
		x = px + 36
	}
	f.text(f.r.fit(fSemibold, 14, c.Title, pw-x+px-20), fSemibold, 14, x, py+24, 0, cText)
	lw := pw - (x - px) - 18
	for i, l := range f.r.wrap(fRegular, 12.5, c.Body, lw, 4) {
		f.text(l, fRegular, 12.5, x, py+46+float64(i)*18, 0, cGray)
	}
	if len(c.Buttons) > 0 {
		bx := px + pw - 16
		cy := py + f.ph - 24
		for i := len(c.Buttons) - 1; i >= 0; i-- {
			b := c.Buttons[i]
			w := f.r.measure(fSemibold, 12.5, b.Label) + 26
			bx -= w
			f.button(b.ID, b.Label, bx, cy, w, 28, i == 0)
			bx -= 8
		}
	}
	f.hit("card:close", px, py, px+pw, py+f.ph)
	// buttons registered later win over card:close in hitAt (last match)
	if len(c.Buttons) > 0 {
		hits := f.hits
		// move the close hit to the front so buttons stay clickable
		for i, h := range hits {
			if h.Name == "card:close" {
				copy(hits[1:i+1], hits[0:i])
				hits[0] = h
				break
			}
		}
	}
}

// ---------- colour picker (live) ----------

func (f *frame) drawPicker() {
	v := f.v
	px, py, pw := f.px, f.py, f.pw
	c := v.X.Picker.Color
	cy := py + smallH/2
	f.rr(px+14, cy-11, 22, 22, 6, rgb(c[0], c[1], c[2]))
	f.text(fmt.Sprintf("#%02X%02X%02X", c[0], c[1], c[2]), fSemibold, 13.5, px+46, cy, 0, cText)
	f.text(L("spustelk bet kur · Esc – atšaukti"), fRegular, 11.5, px+pw-18, cy, 1, cGray)
}

// ---------- plans tab (calendar + reminders + birthdays + to-dos) ----------

func (f *frame) tabPlan() {
	v := f.v
	px, py, pw := f.px, f.py, f.pw
	cy := py + 58
	x := px + 16
	for _, q := range []struct{ id, label string }{{"plan:todo", L("+ Užduotis")}, {"plan:remind", L("+ Priminimas")}, {"rem:add:15", fmt.Sprintf(L("+%d min"), 15)}} {
		w := f.r.measure(fSemibold, 12.5, q.label) + 22
		f.button(q.id, q.label, x, cy, w, 26, q.id == "plan:todo")
		x += w + 6
	}
	hasDone := false
	for _, r := range v.X.Plan {
		if r.Kind == "todo" && r.Done {
			hasDone = true
		}
	}
	if hasDone {
		c := cGray
		if v.HoverBtn == "todo:clear" {
			c = cText
		}
		lw := f.text(L("Išvalyti atliktas"), fSemibold, 12, px+pw-20, cy, 1, c)
		f.hit("todo:clear", px+pw-20-lw-8, cy-12, px+pw-12, cy+12)
	}
	rows := v.X.Plan
	if len(rows) == 0 {
		f.text(L("Planų nėra"), fSemibold, 13, px+pw/2, py+104, 0.5, cGray)
		f.text(L("Užduotys, priminimai, kalendorius ir gimtadieniai atsiras čia"), fRegular, 11.5, px+pw/2, py+126, 0.5, cDim)
		return
	}
	start := v.X.PlanScroll
	if start > len(rows)-1 {
		start = max(0, len(rows)-1)
	}
	for i := 0; i < 3 && start+i < len(rows); i++ {
		r := rows[start+i]
		y := py + 78 + float64(i)*26
		f.rr(px+16, y, pw-32, 23, 8, withA(cBox, f.a))
		cy := y + 11.5
		tx := px + 28
		switch r.Kind {
		case "todo":
			id := fmt.Sprintf("todo:toggle:%d", r.ID)
			f.checkbox(px+26, cy, r.Done, withA(cGray, f.a))
			f.hit(id, px+16, y, px+46, y+23)
			tx = px + 48
		case "event":
			f.circle(px+30, cy, 4, withA(rgb(0x0a, 0x84, 0xff), f.a))
			tx = px + 40
		case "remind":
			f.tabIcon(tabRemind, px+30, cy, withA(accent(ModeWorking), f.a))
			tx = px + 42
		case "bday":
			f.heart(px+30, cy, 4.5, true, withA(rgb(0xff, 0x37, 0x5f), f.a))
			tx = px + 42
		}
		if r.When != "" {
			tx += f.text(r.When, fSemibold, 12, tx, cy, 0, cGray) + 8
		}
		right := px + pw - 26
		if r.Kind == "remind" || r.Kind == "todo" {
			id := fmt.Sprintf("%s:del:%d", map[string]string{"remind": "rem", "todo": "todo"}[r.Kind], r.ID)
			c := cDim
			if v.HoverBtn == id {
				c = cText
			}
			f.cross(px+pw-30, cy, 3.5, withA(c, f.a))
			f.hit(id, px+pw-42, y, px+pw-16, y+23)
			right = px + pw - 44
		}
		if r.Extra != "" {
			right -= f.text(r.Extra, fRegular, 11.5, right, cy, 1, cGray) + 10
		}
		col := cText
		if r.Done {
			col = cDim
		}
		t := f.r.fit(fRegular, 12.5, r.Text, right-tx)
		tw := f.text(t, fRegular, 12.5, tx, cy, 0, col)
		if r.Done {
			f.rr(tx, cy-0.5, tw, 1.2, 0.6, withA(cDim, f.a))
		}
	}
	if len(rows) > 3 && !hasDone {
		f.text(fmt.Sprintf(L("%d–%d iš %d · slink ratuku"), start+1, min(start+3, len(rows)), len(rows)), fRegular, 10.5, px+pw-20, cy, 1, cDim)
	}
}

// ---------- timer statistics ----------

func (f *frame) focusStats(cy float64) {
	v := f.v
	px, pw := f.px, f.pw
	fs := v.X.Focus
	wk := 0.0
	for _, d := range fs.Week {
		wk += d
	}
	f.text(fmt.Sprintf(L("Šiandien %s · savaitė %s"), fmtHM(fs.Today), fmtHM(wk)), fRegular, 11.5, px+20, cy, 0, cGray)
	if len(fs.Week) == 7 {
		mx := 1.0
		for _, d := range fs.Week {
			mx = math.Max(mx, d)
		}
		x0 := px + pw - 20 - 7*12
		for i, d := range fs.Week {
			h := 2 + 16*d/mx
			c := cBtnHi
			if i == 6 {
				c = cWork
			}
			f.rr(x0+float64(i)*12, cy+8-h, 8, h, 2, withA(c, f.a))
		}
	}
}

// ---------- system rows (scrollable) ----------

type sysRow struct {
	label string // kind, also the (translated) label
	draw  func(cy float64)
	name  string // shown instead of the label (e.g. headphone name)
}

func (f *frame) systemRows() []sysRow {
	v := f.v
	px, pw := f.px, f.pw
	var rows []sysRow
	for _, se := range v.Snap.Sessions {
		se := se
		rows = append(rows, mkRow("Claude", func(cy float64) {
			f.circle(px+132, cy, 4, withA(accent(se.Mode), f.a))
			t := L(se.Text)
			if t == "" {
				t = L("laukia")
			}
			if se.Detail != "" {
				t += " · " + se.Detail
			}
			name := se.Project
			if name == "" {
				name = "Claude Code"
			}
			w := f.text(f.r.fit(fSemibold, 12.5, name, 120), fSemibold, 12.5, px+142, cy, 0, cText)
			f.text(f.r.fit(fRegular, 12, t, pw-142-w-30), fRegular, 12, px+142+w+8, cy, 0, cGray)
		}))
	}
	s := v.Sys
	if s.Has {
		if s.Battery >= 0 {
			bc := cRest
			val := fmt.Sprintf("%d%%", s.Battery)
			if s.Charging {
				val += L(" · kraunasi")
			} else if s.Battery <= 20 {
				bc = cWork
			} else if s.Battery <= 40 {
				bc = accent(ModeWorking)
			}
			rows = append(rows, mkRow("Baterija", func(cy float64) { f.barAt(cy, float64(s.Battery)/100, val, bc, "") }))
		}
		cpuC := rgb(0x64, 0xd2, 0xff)
		if s.CPU > 0.85 {
			cpuC = cWork
		}
		rows = append(rows, mkRow("Procesorius", func(cy float64) {
			f.barAt(cy, s.CPU, fmt.Sprintf("%d%%", int(math.Round(s.CPU*100))), cpuC, "")
		}))
		memC := rgb(0xbf, 0x5a, 0xf2)
		if s.RAM > 0.9 {
			memC = cWork
		}
		rows = append(rows, mkRow("Atmintis", func(cy float64) {
			f.barAt(cy, s.RAM, strings.Replace(fmt.Sprintf("%.1f / %.0f GB", s.RAMUsed, s.RAMTotal), ".", ",", 1), memC, "")
		}))
	}
	n := v.Net
	rows = append(rows, mkRow("Internetas", func(cy float64) {
		switch {
		case !n.Has:
			f.text(L("tikrinama…"), fRegular, 12.5, px+128, cy, 0, cDim)
		case !n.Online:
			f.circle(px+132, cy, 4, withA(cWork, f.a))
			f.text(L("nėra ryšio"), fSemibold, 12.5, px+142, cy, 0, cWork)
		default:
			f.circle(px+132, cy, 4, withA(cRest, f.a))
			f.text("↓ "+fmtSpeed(n.Down)+"    ↑ "+fmtSpeed(n.Up), fSemibold, 12.5, px+142, cy, 0, cText)
		}
	}))
	if v.X.Bright >= 0 {
		b := v.X.Bright
		rows = append(rows, mkRow("Šviesumas", func(cy float64) {
			f.barAt(cy, b, fmt.Sprintf("%d%%", int(math.Round(b*100))), rgb(0xff, 0xd6, 0x0a), "brightbar")
		}))
	}
	rows = append(rows, mkRow("Privatumas", func(cy float64) {
		x := px + 128
		if v.X.Mic == "" && v.X.Cam == "" {
			f.text(f.r.fit(fRegular, 12, L("mikrofonas ir kamera nenaudojami"), pw-128-18), fRegular, 12, x, cy, 0, cGray)
			return
		}
		if v.X.Mic != "" {
			f.circle(x+4, cy, 4, withA(rgb(0xff, 0x9f, 0x0a), f.a))
			x += 14 + f.text(L("Mikrofonas: ")+v.X.Mic, fSemibold, 12, x+12, cy, 0, cText) + 10
		}
		if v.X.Cam != "" {
			f.circle(x+4, cy, 4, withA(cRest, f.a))
			f.text(f.r.fit(fSemibold, 12, L("Kamera: ")+v.X.Cam, px+pw-18-x-12), fSemibold, 12, x+12, cy, 0, cText)
		}
	}))
	for _, b := range v.X.BT {
		b := b
		rows = append(rows, sysRowN("Bluetooth", b.Name, func(cy float64) {
			c := cRest
			if b.Pct <= 20 {
				c = cWork
			}
			f.barAt(cy, float64(b.Pct)/100, fmt.Sprintf("%d%%", b.Pct), c, "")
		}))
	}
	for _, u := range v.X.Unread {
		u := u
		rows = append(rows, mkRow("Žinutės", func(cy float64) {
			f.text(fmt.Sprintf(L("%s: %d neskaityt."), u.App, u.N), fSemibold, 12.5, px+128, cy, 0, cText)
		}))
	}
	return rows
}

func (f *frame) barAt(cy, frac float64, value string, c color.NRGBA, hitName string) {
	px, pw := f.px, f.pw
	// the value is drawn first so the bar can stop before it
	vw := f.text(value, fSemibold, 12.5, px+pw-18, cy, 1, cText)
	x0, x1 := px+128, math.Min(px+pw-76, px+pw-18-vw-12)
	f.rr(x0, cy-3, x1-x0, 6, 3, withA(cBtn, f.a))
	if frac > 0 {
		f.rr(x0, cy-3, math.Max(6, (x1-x0)*clamp01(frac)), 6, 3, withA(c, f.a))
	}
	if hitName != "" {
		f.hit(hitName, x0, cy-8, x1, cy+8)
	}
}

// moodOf decides how the robot looks right now.
func moodOf(v View) int {
	h := v.Now.Hour()
	switch {
	case v.Media.Playing():
		return moodMusic
	case v.Sys.LowBattery():
		return moodTired
	case (h >= 23 || h < 6) && v.Snap.Mode == ModeIdle:
		return moodSleep
	}
	return moodNormal
}

func peakAvg(p [5]float64) float64 {
	s := 0.0
	for _, x := range p {
		s += x
	}
	return s / 5
}

func mkRow(label string, draw func(cy float64)) sysRow { return sysRow{label: label, draw: draw} }
func sysRowN(label, name string, draw func(cy float64)) sysRow {
	return sysRow{label: label, draw: draw, name: name}
}
