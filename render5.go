package main

import (
	"fmt"
	"image/color"
	"math"
)

// ---------- colourful, emoji-like icons ----------
// The Windows emoji font is drawn in colour layers that our text engine
// can't render, so these small icons are drawn as shapes instead.

var tabColors = []color.NRGBA{
	rgb(0x64, 0xd2, 0xff), // home
	rgb(0xff, 0x37, 0x5f), // music
	rgb(0xff, 0xd6, 0x0a), // weather
	rgb(0xff, 0x69, 0x61), // timer
	rgb(0xff, 0x9f, 0x0a), // plans
	rgb(0x30, 0xd1, 0x58), // clipboard
	rgb(0xc8, 0x9b, 0x6d), // shelf
	rgb(0xff, 0xcc, 0x66), // note
	rgb(0xbf, 0x5a, 0xf2), // links
	rgb(0x0a, 0x84, 0xff), // system
}

func tabColor(i int) color.NRGBA {
	if i >= 0 && i < len(tabColors) {
		return tabColors[i]
	}
	return cGray
}

// batteryIcon: 18x9 battery filled to pct, with a bolt when charging.
func (p *painter) batteryIcon(cx, cy float64, pct int, charging bool) {
	c := cRest
	switch {
	case charging:
		c = cRest
	case pct <= 20:
		c = cWork
	case pct <= 40:
		c = accent(ModeWorking)
	}
	x, y := cx-9, cy-4.5
	p.dc.NewSubPath()
	p.dc.DrawRoundedRectangle(x*p.s, y*p.s, 16*p.s, 9*p.s, 2.2*p.s)
	p.dc.SetLineWidth(1.2 * p.s)
	p.dc.SetColor(withA(cGray, p.a))
	p.dc.Stroke()
	p.rr(x+16.6, cy-1.8, 1.6, 3.6, 0.8, withA(cGray, p.a))
	fw := 12.6 * clamp01(float64(pct)/100)
	if fw > 0.5 {
		p.rr(x+1.7, y+1.7, fw, 5.6, 1.2, withA(c, p.a))
	}
	if charging {
		p.poly(withA(cWhite, p.a), cx-0.5, cy-4, cx-3, cy+0.6, cx-0.6, cy+0.6, cx-1.4, cy+4, cx+2.4, cy-0.8, cx, cy-0.8)
	}
}

// wifiIcon: three arcs and a dot; red cross when offline.
func (p *painter) wifiIcon(cx, cy float64, online bool) {
	c := rgb(0x64, 0xd2, 0xff)
	if !online {
		c = cDim
	}
	for i, r := range []float64{7, 4.6} {
		_ = i
		p.arc(cx, cy+3, r, 1.6, math.Pi*1.25, math.Pi*1.75, withA(c, p.a))
	}
	p.circle(cx, cy+3, 1.5, withA(c, p.a))
	if !online {
		p.cross(cx+5, cy+2, 2.5, withA(cWork, p.a))
	}
}

func (p *painter) chipIcon(cx, cy float64, c color.NRGBA) {
	p.rr(cx-5, cy-5, 10, 10, 2.2, c)
	p.rr(cx-2.4, cy-2.4, 4.8, 4.8, 1, withA(cPill, p.a))
	p.dc.SetLineWidth(1.1 * p.s)
	p.dc.SetColor(c)
	for _, o := range []float64{-2.5, 0, 2.5} {
		p.dc.DrawLine((cx+o)*p.s, (cy-7)*p.s, (cx+o)*p.s, (cy-5)*p.s)
		p.dc.DrawLine((cx+o)*p.s, (cy+5)*p.s, (cx+o)*p.s, (cy+7)*p.s)
		p.dc.DrawLine((cx-7)*p.s, (cy+o)*p.s, (cx-5)*p.s, (cy+o)*p.s)
		p.dc.DrawLine((cx+5)*p.s, (cy+o)*p.s, (cx+7)*p.s, (cy+o)*p.s)
	}
	p.dc.Stroke()
}

func (p *painter) memIcon(cx, cy float64, c color.NRGBA) {
	p.rr(cx-6.5, cy-3.5, 13, 7, 1.6, c)
	for i := 0; i < 4; i++ {
		p.rr(cx-5+float64(i)*3.2, cy+3.5, 1.4, 2.2, 0.4, c)
	}
}

func (p *painter) micIcon(cx, cy float64, c color.NRGBA) {
	p.rr(cx-2.5, cy-6, 5, 8.5, 2.5, c)
	p.arc(cx, cy-0.5, 4.6, 1.3, 0, math.Pi, c)
	p.dc.SetLineWidth(1.3 * p.s)
	p.dc.SetColor(c)
	p.dc.DrawLine(cx*p.s, (cy+4.1)*p.s, cx*p.s, (cy+6.5)*p.s)
	p.dc.Stroke()
}

func (p *painter) headphones(cx, cy float64, c color.NRGBA) {
	p.arc(cx, cy+1, 5.5, 1.6, math.Pi, 2*math.Pi, c)
	p.rr(cx-6.8, cy+0.5, 3, 5.5, 1.2, c)
	p.rr(cx+3.8, cy+0.5, 3, 5.5, 1.2, c)
}

func (p *painter) bubble(cx, cy float64, c color.NRGBA) {
	p.rr(cx-6.5, cy-5, 13, 9, 3.5, c)
	p.poly(c, cx-3.5, cy+3.5, cx-4.5, cy+7, cx, cy+3.5)
}

// sparkle: a four-pointed star (used for Claude).
func (p *painter) sparkle(cx, cy, r float64, c color.NRGBA) {
	k := r * 0.28
	p.poly(c, cx, cy-r, cx+k, cy-k, cx+r, cy, cx+k, cy+k, cx, cy+r, cx-k, cy+k, cx-r, cy, cx-k, cy-k)
}

func (p *painter) stopwatch(cx, cy float64, c color.NRGBA) {
	p.ring(cx, cy+1, 5.5, 1.6, c)
	p.rr(cx-2, cy-7.5, 4, 1.8, 0.8, c)
	p.dc.SetLineWidth(1.6 * p.s)
	p.dc.SetColor(c)
	p.dc.DrawLine(cx*p.s, (cy+1)*p.s, (cx+2)*p.s, (cy-2)*p.s)
	p.dc.Stroke()
}

func (p *painter) note(cx, cy float64, c color.NRGBA) {
	p.circle(cx-2.5, cy+4, 2.6, c)
	p.circle(cx+4.5, cy+2.5, 2.6, c)
	p.rr(cx-0.4, cy-5.5, 1.6, 9.5, 0.6, c)
	p.rr(cx+6.6, cy-7, 1.6, 9.5, 0.6, c)
	p.poly(c, cx-0.4, cy-5.5, cx+8.2, cy-7, cx+8.2, cy-4.6, cx-0.4, cy-3.1)
}

// rowIcon draws the coloured icon in front of a row in the System tab.
func (f *frame) rowIcon(kind string, cx, cy float64) {
	a := f.a
	switch kind {
	case "Claude":
		f.sparkle(cx, cy, 6, withA(rgb(0xff, 0x9f, 0x0a), a))
	case "Baterija":
		s := f.v.Sys
		f.batteryIcon(cx, cy, s.Battery, s.Charging)
	case "Procesorius":
		f.chipIcon(cx, cy, withA(rgb(0x64, 0xd2, 0xff), a))
	case "Atmintis":
		f.memIcon(cx, cy, withA(rgb(0xbf, 0x5a, 0xf2), a))
	case "Internetas":
		f.wifiIcon(cx, cy, f.v.Net.Online || !f.v.Net.Has)
	case "Šviesumas":
		f.sun(cx, cy, 5.5, withA(rgb(0xff, 0xd6, 0x0a), a))
	case "Privatumas":
		f.micIcon(cx, cy, withA(rgb(0xff, 0x9f, 0x0a), a))
	case "Bluetooth":
		f.headphones(cx, cy, withA(rgb(0x0a, 0x84, 0xff), a))
	case "Žinutės":
		f.bubble(cx, cy, withA(rgb(0xff, 0x37, 0x5f), a))
	}
}

// scrollbar: a thin bar on the right showing where in a list we are.
func (f *frame) scrollbar(x, y0, y1 float64, start, visible, total int) {
	if total <= visible {
		return
	}
	h := y1 - y0
	f.rr(x, y0, 3, h, 1.5, withA(cBtn, f.a))
	th := math.Max(10, h*float64(visible)/float64(total))
	ty := y0 + (h-th)*float64(start)/float64(total-visible)
	f.rr(x, ty, 3, th, 1.5, withA(cGray, f.a))
}

// statusIcons: small phone-like status icons (weather, battery, Wi-Fi)
// drawn right-to-left ending at x. Returns the used width.
func (f *frame) statusIcons(x, cy float64) float64 {
	v := f.v
	start := x
	if v.Net.Has {
		f.wifiIcon(x-7, cy-1, v.Net.Online)
		x -= 22
	}
	if s := v.Sys; s.Has && s.Battery >= 0 {
		w := f.text(fmt.Sprintf("%d%%", s.Battery), fSemibold, 12, x, cy, 1, cGray)
		f.batteryIcon(x-w-14, cy, s.Battery, s.Charging)
		x -= w + 30
	}
	if v.Weather.Has {
		w := f.text(fmtTemp(v.Weather.Temp), fSemibold, 12, x, cy, 1, cGray)
		f.weatherIcon(v.Weather.Code, v.Weather.IsDay, x-w-10, cy, 15, f.a)
		x -= w + 26
	}
	return start - x
}

// compactDate: "Kt 19" / "Thu 19" / "Чт 19"
func compactDate(v View) string { return fmt.Sprintf("%s %d", shortDay(v.Now), v.Now.Day()) }
