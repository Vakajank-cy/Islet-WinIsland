//go:build windows

package main

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"
)

// setBrightness asks Windows to change the screen brightness (0..100).
// Requests are coalesced: only one is in flight at a time.
func (a *app) setBrightness(p int) {
	p = max(0, min(100, p))
	a.brightWant = p
	a.volume = &VolumeOSD{Level: float64(p) / 100, Until: time.Now().Add(1500 * time.Millisecond), Bright: true}
	a.dirty.Store(true)
	if a.brightBusy {
		return
	}
	if a.media.Brightness(p) {
		a.brightBusy = true
		a.bright = float64(p) / 100
	}
}

// extraView collects everything the new features show.
func (a *app) extraView(v View) Extra {
	now := v.Now
	x := Extra{
		Peaks:      a.peaks,
		DND:        a.dndOn(),
		Card:       a.card,
		Cmd:        a.cmd,
		Game:       a.game,
		Picker:     a.picker,
		Bright:     a.bright,
		PlanScroll: a.planScroll,
		SysScroll:  a.sysScroll,
		Caret:      math.Mod(time.Since(a.start).Seconds(), 1) < 0.55,
		NoFill:     !a.cfg.KaraokeFill,
	}
	if a.cfg.CoverGlow {
		x.CoverColor = a.coverCol
	}
	if !a.cfg.Moods {
		x.Mood = moodNormal
	} else {
		x.Mood = moodOf(v)
	}
	if m := v.Media; m != nil {
		x.Fav = a.favs.Has(m.Artist, m.Title)
		if a.coverColRef != a.coverRef {
			a.coverColRef = a.coverRef
			if c, ok := coverColor(a.coverImg); ok {
				a.coverCol = &c
			} else {
				a.coverCol = nil
			}
		}
	}
	if !a.sleepAt.IsZero() {
		x.SleepLeft = fmt.Sprintf(L("%d min"), int(math.Ceil(time.Until(a.sleepAt).Minutes())))
	}
	if v.Hover && v.Tab == tabRemind {
		x.Plan = a.planRows(now)
	}
	if v.Hover && v.Tab == tabTimer {
		x.Focus = FocusView{Today: a.focus.Day(now), Week: a.focus.Week(now)}
	}
	if v.Hover && v.Tab == tabWeather {
		x.Clocks = worldClocks(a.cfg.Clocks, now)
	}
	a.ws.mu.Lock()
	x.Mic, x.Cam, x.BT = a.ws.mic, a.ws.cam, a.ws.bt
	a.ws.mu.Unlock()
	if a.cfg.Messages {
		x.Unread = a.ai.Unread()
	}
	return x
}

// tickFeatures runs every frame on the UI thread.
func (a *app) tickFeatures(now time.Time, dt float64) {
	if a.cfg.CalURL != a.cal.url {
		a.cal.SetURL(a.cfg.CalURL)
	}
	// visualizer
	a.samplePeaks()
	// colour picker preview
	a.pollPicker()
	// mini game
	if a.game != nil {
		a.game.Step(dt, 460-60)
		a.dirty.Store(true)
	}
	// focus statistics
	if a.timer.Running && a.timer.Phase == 0 {
		a.focus.Add(now, dt)
	}
	// sleep timer for music
	if !a.sleepAt.IsZero() && now.After(a.sleepAt) {
		a.sleepAt = time.Time{}
		if m := a.media.Current(); m.Playing() {
			mediaKey(0xB3)
		}
		a.flashImportant(L("Miego laikmatis: muzika sustabdyta"), [3]uint8{0x5e, 0x5c, 0xe6}, 4*time.Second)
	}
	// cards expire
	if a.card != nil && now.After(a.card.Until) {
		if a.over {
			a.card.Until = now.Add(time.Second) // keep it while the mouse is on it
		} else {
			a.card = nil
			a.dirty.Store(true)
		}
	}
	// a generic USB message waits briefly in case a more specific one comes
	if p := a.pendingDev; p != nil && now.Sub(p.Until) > 0 {
		a.pendingDev = nil
		if a.devSpecific.Before(p.Until.Add(-900 * time.Millisecond)) {
			a.flashMsg(p.Text, p.Color, 3500*time.Millisecond)
		}
	}

	if now.Second()%2 == 0 && now.Sub(a.remCheck) >= 900*time.Millisecond {
		a.slowChecks(now)
	}
}

// slowChecks: once per ~2 s.
func (a *app) slowChecks(now time.Time) {
	// microphone / camera
	a.ws.mu.Lock()
	mic, cam, dl := a.ws.mic, a.ws.cam, a.ws.dl
	a.ws.dl = nil
	a.ws.mu.Unlock()
	if mic != "" && mic != a.prevMic {
		a.flashMsg(L("Mikrofoną naudoja: ")+mic, [3]uint8{0xff, 0x9f, 0x0a}, 3*time.Second)
	}
	if cam != "" && cam != a.prevCam {
		a.flashMsg(L("Kamerą naudoja: ")+cam, flashGreen, 3*time.Second)
	}
	a.prevMic, a.prevCam = mic, cam
	// finished downloads
	for _, p := range dl {
		a.flash = &Flash{Text: L("Atsisiųsta: ") + filepath.Base(p) + L(" · atidaryti"), Color: flashBlue, Until: now.Add(7 * time.Second), Open: p}
		if !a.dndOn() {
			a.anim.Pop()
		} else {
			a.flash = nil
		}
	}
	// unread messages
	if a.cfg.Messages {
		for _, u := range a.ai.Unread() {
			if u.N > a.prevUnread[u.App] {
				a.flashMsg(fmt.Sprintf(L("%s: %d neskaityt."), u.App, u.N), flashBlue, 3*time.Second)
			}
		}
		cur := map[string]int{}
		for _, u := range a.ai.Unread() {
			cur[u.App] = u.N
		}
		a.prevUnread = cur
	}
	// calendar: 10 minutes before an event
	evs, _ := a.cal.Upcoming(now, 1)
	for _, e := range evs {
		if e.AllDay {
			continue
		}
		key := e.Start.String() + e.Title
		if d := e.Start.Sub(now); d > 0 && d <= 10*time.Minute && !a.calSeen[key] {
			a.calSeen[key] = true
			a.flashImportant(fmt.Sprintf(L("Po %d min: %s"), int(math.Ceil(d.Minutes())), e.Title), flashBlue, 10*time.Second)
			a.notifyForce(e.Title, fmt.Sprintf(L("Prasidės %s"), e.Start.Format("15:04")))
		}
	}
	day := now.Format("2006-01-02")
	busy := a.cmd != nil || a.game != nil || a.card != nil || a.st.Snapshot().Mode != ModeIdle
	// birthdays (once a day)
	if a.cfg.LastBdayDay != day && now.Hour() >= 8 && !busy {
		a.cfg.LastBdayDay = day
		a.cfg.Save()
		var names []string
		for _, b := range a.cfg.Birthdays {
			if int(now.Month()) == b.Month && now.Day() == b.Day {
				names = append(names, b.Name)
			}
		}
		if len(names) > 0 {
			who := strings.Join(names, ", ")
			a.card = &Card{Title: L("Šiandien gimtadienis!"), Body: fmt.Sprintf(L("%s šiandien švenčia gimtadienį. Nepamiršk pasveikinti!"), who),
				Color: [3]uint8{0xff, 0x37, 0x5f}, Buttons: []CardBtn{{"card:close", L("Gerai")}}, Until: now.Add(30 * time.Second)}
			a.notifyForce(L("Gimtadienis"), who)
			a.anim.Pop()
			return
		}
	}
	// fact of the day (once a day, in the morning)
	if a.cfg.Fact && a.cfg.LastFactDay != day && now.Hour() >= 7 && !busy && !a.dndOn() {
		a.cfg.LastFactDay = day
		a.cfg.Save()
		a.card = &Card{Title: L("Dienos faktas"), Body: todaysFact(now), Color: [3]uint8{0x0a, 0x84, 0xff},
			Buttons: []CardBtn{{"card:close", L("Gerai")}}, Until: now.Add(20 * time.Second)}
		a.anim.Pop()
	}
	a.dirty.Store(true)
}

// onSpecificDevice handles mouse / keyboard / phone arrivals and removals.
func (a *app) onSpecificDevice(kind string, instance string, arrive bool) {
	name, parent := parentName(instance)
	key := kind + "|" + parent
	if t, ok := a.devPair[key]; ok && time.Since(t) < 3*time.Second {
		return
	}
	// a receiver with mouse+keyboard: show only the first one
	if t, ok := a.devPair["any|"+parent]; ok && time.Since(t) < 2*time.Second && parent != "0" {
		return
	}
	a.devPair[key] = time.Now()
	a.devPair["any|"+parent] = time.Now()
	a.devSpecific = time.Now()
	a.pendingDev = nil
	labels := map[string][2]string{
		"mouse":    {L("Pelė prijungta"), L("Pelė atjungta")},
		"keyboard": {L("Klaviatūra prijungta"), L("Klaviatūra atjungta")},
		"phone":    {L("Telefonas prijungtas"), L("Telefonas atjungtas")},
	}[kind]
	if arrive {
		txt := labels[0]
		if name != "" {
			txt += ": " + name
			a.devNames[key] = name
		}
		a.flashMsg(txt, flashBlue, 3500*time.Millisecond)
	} else {
		txt := labels[1]
		if n := a.devNames[key]; n != "" {
			txt += ": " + n
		}
		a.flashMsg(txt, flashGray, 3*time.Second)
	}
}
