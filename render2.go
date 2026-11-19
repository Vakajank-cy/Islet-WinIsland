package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"time"

	"github.com/fogleman/gg"
)

// VolumeOSD is shown for a moment after the wheel changes the volume.
type VolumeOSD struct {
	Level  float64
	Muted  bool
	Until  time.Time
	Bright bool // screen brightness instead of volume
}

// AIState tracks a ChatGPT / Claude window.
type AIState struct {
	Name       string // "ChatGPT" or "Claude"
	Generating bool
}

// ShotView is the preview card after a screenshot.
type ShotView struct {
	Thumb image.Image // already sized 112x63 * S
	Path  string
	Busy  string // e.g. L("Atpažįstamas tekstas…")
	Until time.Time
}

func (v View) volOn() bool  { return v.Volume != nil && v.Now.Before(v.Volume.Until) }
func (v View) shotOn() bool { return v.Shot != nil && v.Now.Before(v.Shot.Until) }

// ---------- small vector icons ----------

func (p *painter) speaker(cx, cy float64, level float64, muted bool, c color.NRGBA) {
	p.poly(c, cx-7, cy-3, cx-3.5, cy-3, cx+1, cy-7, cx+1, cy+7, cx-3.5, cy+3, cx-7, cy+3)
	if muted {
		p.dc.SetLineWidth(1.6 * p.s)
		p.dc.SetColor(c)
		p.dc.DrawLine((cx+4)*p.s, (cy-3.5)*p.s, (cx+9)*p.s, (cy+3.5)*p.s)
		p.dc.Stroke()
		p.dc.DrawLine((cx+9)*p.s, (cy-3.5)*p.s, (cx+4)*p.s, (cy+3.5)*p.s)
		p.dc.Stroke()
		return
	}
	if level > 0.01 {
		p.arc(cx+2, cy, 4, 1.6, -0.8, 0.8, c)
	}
	if level > 0.5 {
		p.arc(cx+2, cy, 7.5, 1.6, -0.85, 0.85, c)
	}
}

// weatherIcon draws a simple icon of the given size (logical px) centered at cx, cy.
func (p *painter) weatherIcon(code int, day bool, cx, cy, size float64, alpha float64) {
	k := size / 24
	sun := withA(rgb(0xff, 0xcc, 0x33), alpha)
	moon := withA(rgb(0xd9, 0xdf, 0xf2), alpha)
	cloud := withA(rgb(0xe6, 0xe9, 0xef), alpha)
	dark := withA(rgb(0x9a, 0xa3, 0xb2), alpha)
	drawSun := func(x, y, r float64) {
		if !day {
			p.circle(x, y, r, moon)
			p.circle(x+r*0.45, y-r*0.35, r*0.85, withA(cPill, alpha))
			return
		}
		p.circle(x, y, r, sun)
		p.dc.SetLineWidth(1.5 * k * p.s)
		p.dc.SetColor(sun)
		for i := 0; i < 8; i++ {
			a := float64(i) * math.Pi / 4
			p.dc.DrawLine((x+math.Cos(a)*(r+2*k))*p.s, (y+math.Sin(a)*(r+2*k))*p.s,
				(x+math.Cos(a)*(r+4.5*k))*p.s, (y+math.Sin(a)*(r+4.5*k))*p.s)
			p.dc.Stroke()
		}
	}
	drawCloud := func(x, y float64, c color.NRGBA) {
		p.circle(x-5*k, y+1*k, 5*k, c)
		p.circle(x+1*k, y-2.5*k, 6.5*k, c)
		p.circle(x+6.5*k, y+1.5*k, 4.5*k, c)
		p.rr(x-10*k, y+1*k, 21*k, 5.5*k, 2.7*k, c)
	}
	switch weatherKind(code) {
	case wxClear:
		drawSun(cx, cy, 6*k)
	case wxPartly:
		drawSun(cx-4*k, cy-4*k, 5*k)
		drawCloud(cx+2*k, cy+3*k, cloud)
	case wxCloudy:
		drawCloud(cx, cy, cloud)
	case wxFog:
		for i := 0; i < 3; i++ {
			p.rr(cx-10*k+float64(i%2)*3*k, cy-6*k+float64(i)*5.5*k, 18*k, 2.6*k, 1.3*k, cloud)
		}
	case wxRain:
		drawCloud(cx, cy-3*k, cloud)
		for i := 0; i < 3; i++ {
			x := cx - 6*k + float64(i)*6*k
			p.dc.SetLineWidth(1.8 * k * p.s)
			p.dc.SetColor(withA(rgb(0x64, 0xd2, 0xff), alpha))
			p.dc.DrawLine(x*p.s, (cy+6*k)*p.s, (x-2*k)*p.s, (cy+10*k)*p.s)
			p.dc.Stroke()
		}
	case wxSnow:
		drawCloud(cx, cy-3*k, cloud)
		for i := 0; i < 3; i++ {
			p.circle(cx-6*k+float64(i)*6*k, cy+8*k+float64(i%2)*1.5*k, 1.6*k, cloud)
		}
	case wxStorm:
		drawCloud(cx, cy-3*k, dark)
		p.poly(withA(rgb(0xff, 0xd6, 0x0a), alpha), cx+1*k, cy+3*k, cx-3*k, cy+9*k, cx, cy+9*k, cx-2*k, cy+13*k, cx+4*k, cy+6.5*k, cx+1*k, cy+6.5*k)
	}
}

// ---------- collapsed extras ----------

func (f *frame) drawVolume() {
	v := f.v
	px, py, pw := f.px, f.py, f.pw
	cy := py + smallH/2
	vo := v.Volume
	if vo.Bright {
		f.sun(px+52, cy, 7, withA(rgb(0xff, 0xd6, 0x0a), f.a))
	} else {
		f.speaker(px+52, cy, vo.Level, vo.Muted, withA(cText, f.a))
	}
	f.hit("vol:mute", px+40, py, px+66, py+smallH)
	x0, x1 := px+72, px+pw-60
	f.rr(x0, cy-3, x1-x0, 6, 3, withA(cBtn, f.a))
	if !vo.Muted && vo.Level > 0 {
		f.rr(x0, cy-3, math.Max(6, (x1-x0)*vo.Level), 6, 3, withA(cText, f.a))
	}
	lbl := fmt.Sprintf("%d%%", int(math.Round(vo.Level*100)))
	if vo.Muted {
		lbl = "tylu"
	}
	f.text(lbl, fSemibold, 13, px+pw-18, cy, 1, cText)
}

func (f *frame) drawShot() {
	v := f.v
	px, py, pw := f.px, f.py, f.pw
	sh := v.Shot
	tx, ty := px+16, py+16
	if sh.Thumb != nil {
		f.blitImage(sh.Thumb, tx, ty)
	} else {
		f.rr(tx, ty, 112, 63, 8, withA(cBox, f.a))
	}
	x := tx + 124
	f.text(L("Ekrano nuotrauka"), fSemibold, 14, x, py+28, 0, cText)
	sub := L("Išsaugota ir nukopijuota")
	if sh.Busy != "" {
		sub = sh.Busy
	}
	f.text(f.r.fit(fRegular, 12, sub, pw-140-16), fRegular, 12, x, py+48, 0, cGray)
	cy := py + 76
	f.button("shot:ocr", L("Kopijuoti tekstą"), x, cy, 128, 28, true)
	f.button("shot:open", L("Atidaryti"), x+136, cy, 86, 28, false)
	f.hit("shot:open", tx, ty, tx+112, ty+63)
}

// ---------- tabs ----------

func (f *frame) tabWeather() {
	v := f.v
	px, py, pw := f.px, f.py, f.pw
	w := v.Weather
	if !w.Has {
		msg := L("Gaunami orai…")
		if w.Err != "" {
			msg = w.Err
		}
		f.text(msg, fRegular, 13, px+pw/2, py+90, 0.5, cGray)
		f.cityLink(px+pw/2, py+116, 0.5)
		return
	}
	f.weatherIcon(w.Code, w.IsDay, px+40, py+70, 36, f.a)
	f.text(fmtTemp(w.Temp), fBold, 34, px+66, py+70, 0, cText)
	desc := weatherText(w.Code)
	if len(w.Days) > 0 {
		desc += " · ↑" + fmtTemp(w.Days[0].Max) + " ↓" + fmtTemp(w.Days[0].Min)
	}
	f.text(f.r.fit(fSemibold, 12.5, desc, 190), fSemibold, 12.5, px+22, py+104, 0, cText)
	f.cityLink(px+22, py+126, 0)
	if len(v.X.Clocks) > 0 {
		x := px + 22
		for _, c := range v.X.Clocks {
			w := f.text(c.Name, fRegular, 11.5, x, py+150, 0, cGray)
			w += f.text(c.Time, fSemibold, 12, x+w+5, py+150, 0, cText)
			w += f.text(c.Diff, fRegular, 10.5, x+w+10, py+150, 0, cDim)
			x += w + 30
		}
	}

	// next days
	days := w.Days
	if len(days) > 1 {
		days = days[1:]
	}
	if len(days) > 5 {
		days = days[:5]
	}
	colW := 42.0
	x0 := px + pw - 16 - colW*float64(len(days))
	for i, d := range days {
		cx := x0 + colW*float64(i) + colW/2
		f.text(shortDay(d.Date), fSemibold, 11.5, cx, py+56, 0.5, cGray)
		f.weatherIcon(d.Code, true, cx, py+82, 20, f.a)
		f.text(fmtTemp(d.Max), fSemibold, 12, cx, py+106, 0.5, cText)
		f.text(fmtTemp(d.Min), fRegular, 11.5, cx, py+122, 0.5, cDim)
	}
}

func (f *frame) cityLink(x, cy, ax float64) {
	v := f.v
	city := v.Weather.City
	if city == "" {
		city = L("nežinomas")
	}
	c := cGray
	if v.HoverBtn == "weather:city" {
		c = cText
	}
	lbl := L("Miestas: ") + city + " · " + L("keisti")
	w := f.r.measure(fRegular, 12, lbl)
	f.text(lbl, fRegular, 12, x, cy, ax, c)
	x0 := x - ax*w
	f.hit("weather:city", x0-6, cy-11, x0+w+6, cy+11)
}

func (f *frame) tabLinks() {
	v := f.v
	px, py, pw := f.px, f.py, f.pw
	cols := 4
	gap := 6.0
	tw := (pw - 32 - gap*float64(cols-1)) / float64(cols)
	th := 34.0
	for i, l := range v.Links {
		r, c := i/cols, i%cols
		x := px + 16 + float64(c)*(tw+gap)
		y := py + 44 + float64(r)*(th+gap)
		id := fmt.Sprintf("link:%d", i)
		bg := cBox
		if v.HoverBtn == id {
			bg = cBtnHi
		}
		f.rr(x, y, tw, th, 12, withA(bg, f.a))
		lc := linkColor(l.Name)
		f.circle(x+19, y+th/2, 11, withA(rgb(lc[0], lc[1], lc[2]), f.a))
		f.text(strings.ToUpper(string([]rune(l.Name)[:1])), fBold, 12, x+19, y+th/2, 0.5, cText)
		f.text(f.r.fit(fSemibold, 12, l.Name, tw-40), fSemibold, 12, x+36, y+th/2, 0, cText)
		f.hit(id, x, y, x+tw, y+th)
	}
	cy := py + 142
	x := px + 16
	for i, b := range []struct{ id, label string }{{"shot:region", L("Sritis")}, {"shot:take", L("Ekranas")}, {"tool:color", L("Spalva")}, {"tool:game", L("Žaidimas")}} {
		w := f.r.measure(fSemibold, 12.5, b.label) + 22
		f.button(b.id, b.label, x, cy, w, 24, i == 0)
		x += w + 6
	}
	c := cGray
	if v.HoverBtn == "links:edit" {
		c = cText
	}
	lw := f.text(L("Redaguoti"), fSemibold, 12, px+pw-20, cy, 1, c)
	f.hit("links:edit", px+pw-20-lw-8, cy-12, px+pw-12, cy+12)
}

// statusVolume replaces the status row while the volume OSD is active.
func (f *frame) statusVolume(cy float64) {
	vo := f.v.Volume
	px, pw := f.px, f.pw
	if vo.Bright {
		f.sun(px+56, cy, 7, withA(rgb(0xff, 0xd6, 0x0a), f.a))
	} else {
		f.speaker(px+56, cy, vo.Level, vo.Muted, withA(cText, f.a))
	}
	x0, x1 := px+72, px+pw-160
	f.rr(x0, cy-3, x1-x0, 6, 3, withA(cBtn, f.a))
	if !vo.Muted {
		f.rr(x0, cy-3, math.Max(6, (x1-x0)*vo.Level), 6, 3, withA(cText, f.a))
	}
	f.text(fmt.Sprintf("%d%%", int(math.Round(vo.Level*100))), fSemibold, 12.5, x1+10, cy, 0, cText)
}

func (f *frame) gridIcon(cx, cy float64, c color.NRGBA) {
	for i := 0; i < 4; i++ {
		x := cx - 6 + float64(i%2)*7
		y := cy - 6 + float64(i/2)*7
		f.dc.NewSubPath()
		f.dc.DrawRoundedRectangle(x*f.s, y*f.s, 5*f.s, 5*f.s, 1.5*f.s)
		f.dc.SetColor(c)
		f.dc.SetLineWidth(1.4 * f.s)
		f.dc.Stroke()
	}
}

var _ = gg.NewContext
