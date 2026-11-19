package main

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"
)

// ---------- theme ----------

var (
	lightTheme bool
	cEye       = rgb(0x0a, 0x0a, 0x0b) // robot eyes are always dark
	cWhite     = rgb(0xff, 0xff, 0xff)
)

func setTheme(light bool) {
	lightTheme = light
	if light {
		cPill = rgb(0xf7, 0xf7, 0xf9)
		cText = rgb(0x1c, 0x1c, 0x1e)
		cGray = rgb(0x6e, 0x6e, 0x73)
		cDim = rgb(0xa1, 0xa1, 0xa6)
		cBtn = rgb(0xe5, 0xe5, 0xea)
		cBtnHi = rgb(0xd1, 0xd1, 0xd6)
		cBox = rgb(0xee, 0xee, 0xf2)
	} else {
		cPill = rgb(0x0a, 0x0a, 0x0b)
		cText = rgb(0xf5, 0xf5, 0xf7)
		cGray = rgb(0x98, 0x98, 0x9f)
		cDim = rgb(0x63, 0x63, 0x66)
		cBtn = rgb(0x2c, 0x2c, 0x2e)
		cBtnHi = rgb(0x3d, 0x3d, 0x40)
		cBox = rgb(0x1c, 0x1c, 0x1e)
	}
}

// NetInfo is internet state and speed.
type NetInfo struct {
	Has      bool
	Online   bool
	Down, Up float64 // bytes per second
}

func fmtSpeed(b float64) string {
	switch {
	case b >= 1<<20:
		return strings.Replace(fmt.Sprintf("%.1f MB/s", b/(1<<20)), ".", ",", 1)
	case b >= 1<<10:
		return fmt.Sprintf("%.0f KB/s", b/(1<<10))
	}
	return fmt.Sprintf("%.0f B/s", b)
}

// ---------- music ----------

func (f *frame) chip(name, label string, x, cy float64, on bool) float64 {
	w := f.r.measure(fSemibold, 11.5, label) + 18
	bg, fg := cBox, cGray
	if on {
		bg, fg = cText, cPill
	} else if f.v.HoverBtn == name {
		bg, fg = cBtnHi, cText
	}
	f.rr(x, cy-11, w, 22, 11, withA(bg, f.a))
	f.text(label, fSemibold, 11.5, x+w/2, cy, 0.5, fg)
	f.hit(name, x, cy-11, x+w, cy+11)
	return w
}

func (f *frame) tabMusic() {
	v := f.v
	px, py, pw := f.px, f.py, f.pw
	m := v.Media
	if m == nil || m.Title == "" {
		f.text(L("Šiuo metu niekas negroja"), fSemibold, 14, px+pw/2, py+100, 0.5, cText)
		f.text(L("Paleisk muziką Spotify, YouTube ar kitur – čia atsiras daina ir jos žodžiai"),
			fRegular, 12, px+pw/2, py+124, 0.5, cGray)
		f.mediaButtons(py+170, px+pw/2+60, false)
		return
	}
	now := v.Now
	c1, c2 := trackHue(m.Artist + m.Title)
	ax, ay := px+16, py+44
	if v.Cover != nil && f.a > 0.95 {
		f.blitImage(v.Cover, ax, ay)
	} else {
		f.gradientRect(ax, ay, 52, 52, 12, c1, c2, false)
		for i := 0; i < 4; i++ {
			hgt := 4 + 18*v.X.Peaks[i]
			f.rr(ax+12+float64(i)*8, ay+40-hgt, 4.5, hgt, 2, color.NRGBA{255, 255, 255, uint8(220 * f.a)})
		}
	}
	tx := ax + 64
	right := px + pw - 16
	maxW := right - 130 - tx
	f.text(f.r.fit(fBold, 15, m.Title, maxW), fBold, 15, tx, py+58, 0, cText)
	sub := m.Artist
	if a := m.AppName(); a != "" {
		if sub != "" {
			sub += " · "
		}
		sub += a
	}
	f.text(f.r.fit(fRegular, 12.5, sub, maxW), fRegular, 12.5, tx, py+80, 0, cGray)
	f.mediaButtons(py+70, right, m.Playing())

	// progress
	pos := m.Position(now)
	by := py + 110
	f.text(fmtDur(time.Duration(pos*float64(time.Second))), fRegular, 11, px+16, by, 0, cDim)
	bx0, bx1 := px+56, right-40
	if m.End > 0 {
		prog := clamp01(pos / m.End)
		f.text(fmtDur(time.Duration(m.End*float64(time.Second))), fRegular, 11, right, by, 1, cDim)
		bh := 4.0
		if strings.HasPrefix(v.HoverBtn, "seekbar") {
			bh = 6
			f.circle(bx0+(bx1-bx0)*prog, by, 6, withA(cText, f.a))
		}
		f.rr(bx0, by-bh/2, bx1-bx0, bh, bh/2, withA(cBtn, f.a))
		pc := cText
		if cc := v.X.CoverColor; cc != nil {
			pc = rgb(cc[0], cc[1], cc[2])
		}
		f.rr(bx0, by-bh/2, math.Max(4, (bx1-bx0)*prog), bh, bh/2, withA(pc, f.a))
		f.hit("seekbar", bx0, by-9, bx1, by+9)
	} else {
		f.text(L("Laikas apytikslis – spausk girdimą eilutę"), fRegular, 11, (bx0+bx1)/2, by, 0.5, cDim)
	}

	// lyrics box
	ly, lh := py+124, 86.0
	f.rr(px+16, ly, pw-32, lh, 14, withA(cBox, f.a))
	f.musicChips(py + 228)
	lw := pw - 32 - 28
	cx := px + pw/2
	l := v.Lyrics
	msg := ""
	switch {
	case l == nil || l.State == lyrNone || l.State == lyrLoading:
		msg = L("Ieškoma dainos žodžių…")
	case l.State == lyrNotFound:
		msg = L("Šiai dainai žodžių nerasta")
	case l.State == lyrInstrumental:
		msg = L("Instrumentinė daina – be žodžių")
	case l.State == lyrError:
		msg = L("Nepavyko prisijungti prie žodžių bazės")
	case l.State == lyrPlain:
		lines := f.r.wrap(fRegular, 12.5, l.Plain, lw, 3)
		f.text(L("Žodžiai be laiko žymų:"), fSemibold, 11, px+30, ly+14, 0, cDim)
		for i, t := range lines {
			f.text(t, fRegular, 12.5, px+30, ly+34+float64(i)*18, 0, cGray)
		}
		return
	}
	if msg != "" {
		f.text(msg, fRegular, 13, cx, ly+lh/2, 0.5, cGray)
		return
	}
	lp := pos + lyricLead + v.LyricOffset
	i := l.LineAt(lp)
	slide := 0.0
	if i >= 0 {
		slide = 1 - clamp01((lp-l.Lines[i].T)/0.35)
		slide *= slide
	}
	trans := ""
	if v.Translate && l.TransLang == curLang && i >= 0 && i < len(l.Trans) {
		trans = l.Trans[i]
	}
	mid := ly + lh/2
	gapPrev, gapNext := 28.0, 28.0
	curY := mid
	if trans != "" {
		curY, gapPrev, gapNext = mid-6, 26, 40
	}
	off := slide * 26
	base := f.a
	draw := func(j int, y float64, k fontKind, size float64, c color.NRGBA) {
		f.a = base * clamp01(1.6-math.Abs(y-mid)/30)
		if f.a < 0.02 {
			return
		}
		inRange := j >= 0 && j < len(l.Lines)
		txt := ""
		if inRange {
			txt = strings.TrimSpace(l.Lines[j].Text)
		}
		id := fmt.Sprintf("lyric:%d", j)
		if inRange && v.HoverBtn == id {
			c = cText
		}
		if txt == "" {
			if j != i {
				return
			}
			if g0, g1, ok := lyricGap(l, i, lp); ok {
				// intro or instrumental part: count down to the singing
				left := int(math.Ceil(g1 - lp))
				t := fmt.Sprintf(L("žodžiai po %d:%02d"), left/60, left%60)
				tw := f.r.measure(fSemibold, 14, t)
				x0 := cx - (tw+22)/2
				f.note(x0+6, y, withA(cMusic, f.a))
				f.text(t, fSemibold, 14, x0+22, y, 0, cText)
				bw := 120.0
				f.rr(cx-bw/2, y+13, bw, 3, 1.5, withA(cBtn, f.a))
				pc := cMusic
				if cc := v.X.CoverColor; cc != nil {
					pc = rgb(cc[0], cc[1], cc[2])
				}
				f.rr(cx-bw/2, y+13, math.Max(3, bw*clamp01((lp-g0)/math.Max(0.1, g1-g0))), 3, 1.5, withA(pc, f.a))
				for k := i + 1; k < len(l.Lines); k++ { // click: jump to the singing
					if l.Lines[k].T == g1 {
						f.hit(fmt.Sprintf("lyric:%d", k), x0-6, y-12, x0+tw+28, y+18)
						break
					}
				}
				return
			}
			// short pause: a little note instead of an empty line
			f.tabIcon(tabMusic, cx, y, withA(c, f.a))
			if inRange {
				f.hit(id, cx-14, y-11, cx+14, y+11)
			}
			return
		}
		t := f.r.fit(k, size, txt, lw)
		var tw float64
		if j == i && t == txt && !v.X.NoFill {
			tw = f.textFill(t, k, size, cx, y, 0.5, lineProgress(l, i, lp), cText, cGray)
		} else {
			tw = f.text(t, k, size, cx, y, 0.5, c)
		}
		f.hit(id, cx-tw/2-8, y-11, cx+tw/2+8, y+11)
	}
	draw(i-1, curY-gapPrev+off, fRegular, 12.5, cDim)
	draw(i, curY+off, fBold, 15.5, cText)
	if trans != "" {
		f.a = base * clamp01(1.6-math.Abs(curY+18+off-mid)/30)
		f.text(f.r.fit(fRegular, 12, trans, lw), fRegular, 12, cx, curY+18+off, 0.5, rgb(0x64, 0xd2, 0xff))
	}
	draw(i+1, curY+gapNext+off, fRegular, 12.5, cGray)
	f.a = base
}

func (f *frame) musicChips(cy float64) {
	v := f.v
	px, pw := f.px, f.pw
	x := px + pw - 16
	sleep := L("Miegas")
	if v.X.SleepLeft != "" {
		sleep = L("Miegas") + " " + v.X.SleepLeft
	}
	for _, c := range []struct {
		name, label string
		on          bool
	}{{"lyr:karaoke", "Karaoke", false}, {"lyr:trans", L("Vertimas"), v.Translate}, {"lyr:sleep", sleep, v.X.SleepLeft != ""},
		{"lyr:later", "+0,5 s", false}, {"lyr:earlier", "−0,5 s", false}} {
		w := f.r.measure(fSemibold, 11.5, c.label) + 18
		x -= w
		f.chip(c.name, c.label, x, cy, c.on)
		x -= 6
	}
	// heart = favourite song
	hx := px + 30
	bg := cBox
	if v.HoverBtn == "lyr:fav" {
		bg = cBtnHi
	}
	f.circle(hx, cy, 12, withA(bg, f.a))
	hc := cGray
	if v.X.Fav {
		hc = rgb(0xff, 0x37, 0x5f)
	}
	f.heart(hx, cy+0.5, 5.2, v.X.Fav, withA(hc, f.a))
	f.hit("lyr:fav", hx-12, cy-12, hx+12, cy+12)
	if v.LyricOffset != 0 && x-hx > 90 {
		f.text(strings.Replace(fmt.Sprintf("%+.1f s", v.LyricOffset), ".", ",", 1), fRegular, 11, hx+20, cy, 0, cDim)
	}
}

// ---------- system ----------

func (f *frame) tabSystem() {
	v := f.v
	px, py, pw := f.px, f.py, f.pw
	now := v.Now
	cy := py + 58
	rx := px + 34
	f.ring(rx, cy, 12, 3, withA(cBtn, f.a))
	u := v.Usage
	tmax := pw - 56 - 130
	if u.Has {
		f.arc(rx, cy, 12, 3, -math.Pi/2, -math.Pi/2+2*math.Pi*u.Progress(now), withA(accent(ModeWorking), f.a))
		f.text(f.r.fit(fSemibold, 12.5, L("Claude 5 val. · liko ")+u.LeftText(now), tmax), fSemibold, 12.5, px+56, cy-8, 0, cText)
		f.text(f.r.fit(fRegular, 11.5, fmt.Sprintf(L("%s žetonų · %d užklausos · iki %s"), fmtTokens(u.Tokens), u.Requests, u.End.Format("15:04")), tmax),
			fRegular, 11.5, px+56, cy+9, 0, cGray)
	} else {
		f.text(L("Claude 5 val. langas"), fSemibold, 12.5, px+56, cy-8, 0, cText)
		f.text(L("Šiuo metu neaktyvus"), fRegular, 11.5, px+56, cy+9, 0, cGray)
	}
	f.button("settings", L("Nustatymai"), px+pw-16-108, cy, 108, 28, false)

	rows := f.systemRows()
	start := min(v.X.SysScroll, max(0, len(rows)-4))
	for i := 0; i < 4 && start+i < len(rows); i++ {
		cy := py + 92 + float64(i)*21
		r := rows[start+i]
		f.rowIcon(r.label, px+26, cy)
		name := L(r.label)
		if r.name != "" {
			name = r.name
		}
		f.text(f.r.fit(fRegular, 12, name, 82), fRegular, 12, px+42, cy, 0, cGray)
		r.draw(cy)
	}
	f.scrollbar(px+pw-9, py+84, py+160, start, 4, len(rows))
}

// ---------- reminders ----------

func (f *frame) tabRemind() {
	v := f.v
	px, py, pw := f.px, f.py, f.pw
	cy := py + 58
	x := px + 16
	for _, q := range []struct{ id, label string }{{"rem:add:5", fmt.Sprintf(L("+%d min"), 5)}, {"rem:add:15", fmt.Sprintf(L("+%d min"), 15)}, {"rem:add:30", fmt.Sprintf(L("+%d min"), 30)}, {"rem:add:60", L("+1 val.")}} {
		w := f.r.measure(fSemibold, 12.5, q.label) + 22
		f.button(q.id, q.label, x, cy, w, 26, false)
		x += w + 6
	}
	f.button("rem:text", L("Su tekstu…"), px+pw-16-104, cy, 104, 26, true)

	list := v.Reminders
	if len(list) == 0 {
		f.text(L("Priminimų nėra"), fSemibold, 13, px+pw/2, py+104, 0.5, cGray)
		f.text(L("Spausk „+15 min“ arba „Su tekstu…“ – juostelė primins laiku"), fRegular, 11.5, px+pw/2, py+126, 0.5, cDim)
		return
	}
	for i, r := range list {
		if i == 3 {
			break
		}
		y := py + 80 + float64(i)*26
		f.rr(px+16, y, pw-32, 23, 8, withA(cBox, f.a))
		tw := f.text(r.At.Format("15:04"), fSemibold, 12.5, px+28, y+11.5, 0, accent(ModeWorking))
		ut := untilText(r.At, v.Now)
		uw := f.r.measure(fRegular, 11.5, ut)
		f.text(ut, fRegular, 11.5, px+pw-48, y+11.5, 1, cGray)
		f.text(f.r.fit(fRegular, 12.5, r.Text, pw-32-24-tw-uw-60), fRegular, 12.5, px+28+tw+10, y+11.5, 0, cText)
		id := fmt.Sprintf("rem:del:%d", r.ID)
		c := cDim
		if v.HoverBtn == id {
			c = cText
		}
		f.cross(px+pw-30, y+11.5, 3.5, withA(c, f.a))
		f.hit(id, px+pw-42, y, px+pw-16, y+23)
	}
	if len(list) > 3 {
		f.text(fmt.Sprintf(L("dar %d – žiūrėk nustatymuose"), len(list)-3), fRegular, 11, px+pw/2, py+160, 0.5, cDim)
	}
}

// ---------- file shelf ----------

func extColor(ext string) color.NRGBA {
	c := linkColor(ext)
	return rgb(c[0], c[1], c[2])
}

func (f *frame) tabShelf() {
	v := f.v
	px, py, pw := f.px, f.py, f.pw
	if len(v.Shelf) == 0 {
		f.tabIcon(tabShelf, px+pw/2, py+78, withA(cGray, f.a))
		f.text(L("Užtempk failus ant salelės – jie palauks čia"), fSemibold, 13, px+pw/2, py+108, 0.5, cText)
		f.text(L("Saugoma tik nuoroda, failai nekopijuojami ir nejudinami"), fRegular, 11.5, px+pw/2, py+130, 0.5, cDim)
		return
	}
	cols, gap := 3, 6.0
	tw := (pw - 32 - gap*float64(cols-1)) / float64(cols)
	th := 34.0
	for i, p := range v.Shelf {
		if i == 6 {
			break
		}
		r, c := i/cols, i%cols
		x := px + 16 + float64(c)*(tw+gap)
		y := py + 46 + float64(r)*(th+gap)
		id := fmt.Sprintf("shelf:open:%d", i)
		del := fmt.Sprintf("shelf:del:%d", i)
		hov := v.HoverBtn == id || v.HoverBtn == del
		bg := cBox
		if hov {
			bg = cBtnHi
		}
		f.rr(x, y, tw, th, 10, withA(bg, f.a))
		name, ext := fileLabel(p)
		if ext == "" {
			ext = "—"
		}
		if len(ext) > 4 {
			ext = ext[:4]
		}
		f.rr(x+8, y+8, 34, 18, 5, withA(extColor(ext), f.a))
		f.text(ext, fBold, 9.5, x+25, y+17, 0.5, cWhite)
		nameW := tw - 50
		if hov {
			nameW -= 16
		}
		f.text(f.r.fit(fSemibold, 11.5, name, nameW), fSemibold, 11.5, x+48, y+17, 0, cText)
		f.hit(id, x, y, x+tw, y+th)
		if hov {
			c := cGray
			if v.HoverBtn == del {
				c = cText
			}
			f.cross(x+tw-12, y+17, 3.5, withA(c, f.a))
			f.hit(del, x+tw-24, y, x+tw, y+th)
		}
	}
	cy := py + 142
	f.button("shelf:copy", L("Kopijuoti visus"), px+16, cy, 132, 26, true)
	f.text(L("tada Ctrl+V aplanke"), fRegular, 11.5, px+156, cy, 0, cDim)
	c := cGray
	if v.HoverBtn == "shelf:clear" {
		c = cText
	}
	lw := f.text(L("Išvalyti"), fSemibold, 12, px+pw-20, cy, 1, c)
	f.hit("shelf:clear", px+pw-20-lw-8, cy-12, px+pw-12, cy+12)
}

// cross draws a small "x" (the font may not have the ✕ symbol).
func (p *painter) cross(cx, cy, r float64, c color.NRGBA) {
	p.dc.SetLineWidth(1.6 * p.s)
	p.dc.SetColor(c)
	p.dc.DrawLine((cx-r)*p.s, (cy-r)*p.s, (cx+r)*p.s, (cy+r)*p.s)
	p.dc.Stroke()
	p.dc.DrawLine((cx+r)*p.s, (cy-r)*p.s, (cx-r)*p.s, (cy+r)*p.s)
	p.dc.Stroke()
}
