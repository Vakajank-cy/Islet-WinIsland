//go:build windows

package main

import (
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	u16 "unicode/utf16"
	"unsafe"
)

var (
	pRegEnumKeyExW          = advapi32.NewProc("RegEnumKeyExW")
	pGetPixel               = gdi32.NewProc("GetPixel")
	pGetAsyncKeyState       = user32.NewProc("GetAsyncKeyState")
	pSetWindowLongPtrW      = user32.NewProc("SetWindowLongPtrW")
	pGetWindowLongPtrW      = user32.NewProc("GetWindowLongPtrW")
	pSetFocus               = user32.NewProc("SetFocus")
	pCMGetParent            = cfgmgr32.NewProc("CM_Get_Parent")
	pCMGetDeviceIDListSizeW = cfgmgr32.NewProc("CM_Get_Device_ID_List_SizeW")
	pCMGetDeviceIDListW     = cfgmgr32.NewProc("CM_Get_Device_ID_ListW")
)

const (
	wmKeyDown    = 0x0100
	wmChar       = 0x0102
	wmActivate   = 0x0006
	wmPicked     = wmUser + 5
	gwlExStyle   = ^uintptr(19) // -20
	vkEscape     = 0x1B
	vkReturn     = 0x0D
	vkBack       = 0x08
	vkUp         = 0x26
	vkDown       = 0x28
	vkSpace      = 0x20
	vkControl    = 0x11
	maActivate   = 1
	hkeyCU       = 0x80000001
	keyRead      = 0x20019
	regQwordType = 11
)

// ---------- focus handling for typing (command bar, game) ----------

func (a *app) setTyping(on bool) {
	style, _, _ := pGetWindowLongPtrW.Call(a.hwnd, gwlExStyle)
	if on {
		pSetWindowLongPtrW.Call(a.hwnd, gwlExStyle, style&^wsExNoActivate)
		pSetForegroundWindow.Call(a.hwnd)
		pSetFocus.Call(a.hwnd)
	} else {
		pSetWindowLongPtrW.Call(a.hwnd, gwlExStyle, style|wsExNoActivate)
	}
	a.typing = on
}

func (a *app) openCmd(prefill string) {
	a.game = nil
	a.cmd = &CmdView{Input: prefill}
	a.cmd.Results = EvalCommand(prefill, a.links)
	a.setTyping(true)
	a.anim.Pop()
	a.dirty.Store(true)
}

func (a *app) closeOverlays() {
	if a.cmd != nil || a.game != nil {
		a.cmd = nil
		if a.game != nil {
			if a.game.Best > a.cfg.GameBest {
				a.cfg.GameBest = a.game.Best
				a.cfg.Save()
			}
		}
		a.game = nil
		a.setTyping(false)
		a.dirty.Store(true)
	}
}

func (a *app) onKey(vk uintptr) {
	switch {
	case a.game != nil:
		switch vk {
		case vkEscape:
			a.closeOverlays()
		case vkSpace, vkUp:
			a.game.Jump()
		}
	case a.cmd != nil:
		c := a.cmd
		switch vk {
		case vkEscape:
			a.closeOverlays()
		case vkReturn:
			if c.Sel < len(c.Results) {
				a.runCmd(c.Results[c.Sel].Action)
			}
		case vkUp:
			if c.Sel > 0 {
				c.Sel--
			}
		case vkDown:
			if c.Sel < len(c.Results)-1 {
				c.Sel++
			}
		case vkBack:
			if r := []rune(c.Input); len(r) > 0 {
				c.Input = string(r[:len(r)-1])
				a.refreshCmd()
			}
		}
	}
	a.dirty.Store(true)
}

func (a *app) onChar(ch uintptr) {
	if a.cmd == nil {
		return
	}
	switch {
	case ch == 0x16: // Ctrl+V
		if txt, ok := readClipboardText(a.hwnd); ok {
			a.cmd.Input += strings.Join(strings.Fields(txt), " ")
		}
	case ch == 0x7F: // Ctrl+Backspace
		in := strings.TrimRight(a.cmd.Input, " ")
		if i := strings.LastIndex(in, " "); i >= 0 {
			a.cmd.Input = in[:i+1]
		} else {
			a.cmd.Input = ""
		}
	case ch >= 0x20 && ch != 0x7F && !(ch >= 0xD800 && ch <= 0xDFFF):
		a.cmd.Input += string(rune(ch))
	default:
		return
	}
	a.refreshCmd()
}

func (a *app) refreshCmd() {
	if a.cmd != nil {
		a.cmd.Results = EvalCommand(a.cmd.Input, a.links)
		if a.cmd.Sel >= len(a.cmd.Results) {
			a.cmd.Sel = 0
		}
		a.dirty.Store(true)
	}
}

func (a *app) runCmd(action string) {
	kind, arg, _ := strings.Cut(action, ":")
	if kind == "" {
		return
	}
	a.closeOverlays()
	now := time.Now()
	switch kind {
	case "copy":
		setClipboardText(a.hwnd, arg)
		a.flashMsg(L("Nukopijuota")+": "+arg, flashGreen, 2*time.Second)
	case "open":
		shellOpen(a.hwnd, arg)
	case "remind":
		ms, text, _ := strings.Cut(arg, ":")
		n, _ := strconv.Atoi(ms)
		a.rem.Add(text, now.Add(time.Duration(n)*time.Minute))
		a.flashMsg(fmt.Sprintf(L("Priminsiu po %d min"), n), [3]uint8{0xff, 0x9f, 0x0a}, 2*time.Second)
	case "todo":
		a.todos.Add(arg)
		a.flashMsg(L("Užduotis pridėta"), [3]uint8{0xbf, 0x5a, 0xf2}, 1500*time.Millisecond)
	case "trans":
		a.translateToCard(arg)
	case "cmd":
		switch arg {
		case "settings":
			shellOpen(a.hwnd, settingsURL(""))
		case "karaoke":
			shellOpen(a.hwnd, karaokeURL())
		case "weather":
			a.tab, a.pinHover = tabWeather, time.Now().Add(6*time.Second)
		case "timer":
			if !a.timer.Running {
				a.timer.Toggle(now)
			}
			a.flashMsg(L("Laikmatis paleistas"), [3]uint8{0xff, 0x69, 0x61}, 1500*time.Millisecond)
		case "dnd":
			a.cfg.DND = !a.cfg.DND
			a.cfg.Save()
			if a.cfg.DND {
				a.flashImportant(L("Netrukdyti: įjungta"), [3]uint8{0xbf, 0x5a, 0xf2}, 2*time.Second)
			} else {
				a.flashImportant(L("Netrukdyti: išjungta"), flashGray, 2*time.Second)
			}
		case "color":
			a.startPicker()
		case "game":
			a.startGame()
		case "shot":
			a.snipRegion()
		case "note":
			a.openNote()
		case "shelf":
			a.tab, a.pinHover = tabShelf, time.Now().Add(6*time.Second)
		case "favs":
			shellOpen(a.hwnd, settingsURL("megstamos"))
		}
	}
	a.dirty.Store(true)
}

func (a *app) startGame() {
	a.cmd = nil
	a.game = &Game{Best: a.cfg.GameBest}
	a.game.Start()
	a.setTyping(true)
	a.dirty.Store(true)
}

// ---------- translation card ----------

func (a *app) translateToCard(text string) {
	a.card = &Card{Title: L("Verčiama…"), Body: text, Color: [3]uint8{0x5e, 0x5c, 0xe6}, Until: time.Now().Add(20 * time.Second)}
	a.dirty.Store(true)
	tl := curLang
	go func() {
		out, _, err := TranslateText(&http.Client{Timeout: 12 * time.Second}, text, tl)
		a.call(func() {
			if err != nil || strings.TrimSpace(out) == "" {
				a.card = &Card{Title: L("Nepavyko išversti"), Body: text, Color: [3]uint8{0xff, 0x45, 0x3a}, Until: time.Now().Add(4 * time.Second)}
				return
			}
			a.lastTrans = out
			a.card = &Card{Title: L("Vertimas"), Body: out, Color: [3]uint8{0x5e, 0x5c, 0xe6},
				Buttons: []CardBtn{{"trans:copy", L("Kopijuoti")}, {"card:close", L("Uždaryti")}}, Until: time.Now().Add(20 * time.Second)}
			a.anim.Pop()
		})
	}()
}

// ---------- colour picker ----------

func (a *app) startPicker() {
	a.picker = &PickerView{}
	a.picking = true
	a.dirty.Store(true)
}

func (a *app) pollPicker() {
	if !a.picking {
		return
	}
	if k, _, _ := pGetAsyncKeyState.Call(vkEscape); k&0x8000 != 0 {
		a.picking, a.picker = false, nil
		a.dirty.Store(true)
		return
	}
	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	a.picker.Color = screenPixel(pt.X, pt.Y)
	a.dirty.Store(true)
}

func screenPixel(x, y int32) [3]uint8 {
	dc, _, _ := pGetDC.Call(0)
	c, _, _ := pGetPixel.Call(dc, uintptr(x), uintptr(y))
	pReleaseDC.Call(0, dc)
	return [3]uint8{uint8(c), uint8(c >> 8), uint8(c >> 16)}
}

func (a *app) picked(x, y int32) {
	c := screenPixel(x, y)
	a.picking, a.picker = false, nil
	hex := fmt.Sprintf("#%02X%02X%02X", c[0], c[1], c[2])
	setClipboardText(a.hwnd, hex)
	sw := c
	a.pickedColor = c
	a.card = &Card{Title: hex, Body: fmt.Sprintf("RGB %d, %d, %d · %s", c[0], c[1], c[2], L("HEX nukopijuotas")), Color: c, Swatch: &sw,
		Buttons: []CardBtn{{"pick:hex", "HEX"}, {"pick:rgb", "RGB"}}, Until: time.Now().Add(10 * time.Second)}
	a.anim.Pop()
	a.dirty.Store(true)
}

// ---------- audio peak meter (real visualizer) ----------

var iidIAudioMeterInformation = guid{0xC02216F6, 0x8C67, 0x4B5B, [8]byte{0x9D, 0x00, 0xD0, 0x08, 0xE7, 0x3E, 0x00, 0x64}}

func (v *volMon) peak() float64 {
	if v.meter == 0 {
		return 0
	}
	var p float32
	if comCall(v.meter, 3, uintptr(unsafe.Pointer(&p))) != 0 { // GetPeakValue
		return 0
	}
	return float64(p)
}

// samplePeaks keeps a short history so the bars ripple like a wave.
func (a *app) samplePeaks() {
	p := a.vol.peak()
	a.peakHist = append(a.peakHist, p)
	if len(a.peakHist) > 40 {
		a.peakHist = a.peakHist[len(a.peakHist)-40:]
	}
	n := len(a.peakHist)
	for i := 0; i < 5; i++ {
		j := n - 1 - i*3
		v := 0.0
		if j >= 0 {
			v = a.peakHist[j]
		}
		v = math.Min(1, math.Sqrt(v)*1.15) // perceptual curve
		// fast attack, slower release
		if v > a.peaks[i] {
			a.peaks[i] = v
		} else {
			a.peaks[i] = a.peaks[i]*0.82 + v*0.18
		}
	}
}

// ---------- microphone / camera in use ----------

func regOpen(root uintptr, path string) (uintptr, bool) {
	var k uintptr
	r, _, _ := pRegOpenKeyExW.Call(root, uintptr(unsafe.Pointer(utf16(path))), 0, keyRead, uintptr(unsafe.Pointer(&k)))
	return k, r == 0
}

func regSubkeys(k uintptr) []string {
	var out []string
	for i := 0; i < 500; i++ {
		buf := make([]uint16, 512)
		n := uint32(len(buf))
		if r, _, _ := pRegEnumKeyExW.Call(k, uintptr(i), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)), 0, 0, 0, 0); r != 0 {
			break
		}
		out = append(out, syscall.UTF16ToString(buf[:n]))
	}
	return out
}

func regQword(k uintptr, name string) uint64 {
	var v uint64
	var typ uint32
	size := uint32(8)
	if r, _, _ := pRegQueryValueExW.Call(k, uintptr(unsafe.Pointer(utf16(name))), 0, uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&v)), uintptr(unsafe.Pointer(&size))); r != 0 {
		return 0
	}
	return v
}

func inUse(k uintptr) bool {
	return regQword(k, "LastUsedTimeStart") > 0 && regQword(k, "LastUsedTimeStop") == 0
}

func appFromKey(name string) string {
	if strings.Contains(name, "#") { // C:#Program Files#Discord#Discord.exe
		n := name[strings.LastIndex(name, "#")+1:]
		return strings.TrimSuffix(strings.TrimSuffix(n, ".exe"), ".EXE")
	}
	n := name
	if i := strings.Index(n, "_"); i > 0 {
		n = n[:i]
	}
	if i := strings.LastIndex(n, "."); i >= 0 {
		n = n[i+1:]
	}
	return n
}

// whoUses returns the apps currently using the device ("microphone" / "webcam").
func whoUses(device string) string {
	base := `Software\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\` + device
	root, ok := regOpen(hkeyCU, base)
	if !ok {
		return ""
	}
	defer pRegCloseKey.Call(root)
	var apps []string
	for _, sub := range regSubkeys(root) {
		k, ok := regOpen(hkeyCU, base+`\`+sub)
		if !ok {
			continue
		}
		if sub == "NonPackaged" {
			for _, s2 := range regSubkeys(k) {
				k2, ok := regOpen(hkeyCU, base+`\NonPackaged\`+s2)
				if ok {
					if inUse(k2) {
						apps = append(apps, appFromKey(s2))
					}
					pRegCloseKey.Call(k2)
				}
			}
		} else if inUse(k) {
			apps = append(apps, appFromKey(sub))
		}
		pRegCloseKey.Call(k)
	}
	return strings.Join(apps, ", ")
}

// ---------- downloads folder ----------

type dlWatcher struct {
	dir   string
	mod   time.Time
	known map[string]int64
	init  bool
}

var tempExt = map[string]bool{".crdownload": true, ".part": true, ".partial": true, ".tmp": true, ".download": true, ".opdownload": true}

// check returns files that finished downloading since the last call.
func (d *dlWatcher) check() []string {
	if d.dir == "" {
		home, _ := os.UserHomeDir()
		d.dir = filepath.Join(home, "Downloads")
	}
	st, err := os.Stat(d.dir)
	if err != nil || (d.init && st.ModTime().Equal(d.mod)) {
		return nil
	}
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return nil
	}
	d.mod = st.ModTime()
	cur := map[string]int64{}
	var fresh []string
	for _, e := range entries {
		if e.IsDir() || tempExt[strings.ToLower(filepath.Ext(e.Name()))] || strings.HasPrefix(e.Name(), "~") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		cur[e.Name()] = info.Size()
		if _, seen := d.known[e.Name()]; d.init && !seen && info.Size() > 0 && time.Since(info.ModTime()) < 10*time.Minute {
			fresh = append(fresh, filepath.Join(d.dir, e.Name()))
		}
	}
	d.known, d.init = cur, true
	return fresh
}

// ---------- Bluetooth battery (where Windows knows it) ----------

var keyBTBattery = devPropKey{guid{0x104EA319, 0x6EE2, 0x4701, [8]byte{0xBD, 0x47, 0x8D, 0xDB, 0xF4, 0x25, 0xBB, 0xE5}}, 2}

func btBatteries() []BTView {
	if pCMGetDeviceIDListSizeW.Find() != nil {
		return nil
	}
	best := map[string]int{}
	for _, enum := range []string{"BTHENUM", "BTHHFENUM", "BTHLEDEVICE"} {
		var size uint32
		const flags = 0x1 | 0x100 // FILTER_ENUMERATOR | FILTER_PRESENT
		if r, _, _ := pCMGetDeviceIDListSizeW.Call(uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(utf16(enum))), flags); r != 0 || size == 0 {
			continue
		}
		buf := make([]uint16, size)
		if r, _, _ := pCMGetDeviceIDListW.Call(uintptr(unsafe.Pointer(utf16(enum))), uintptr(unsafe.Pointer(&buf[0])), uintptr(size), flags); r != 0 {
			continue
		}
		for _, id := range strings.Split(string(u16.Decode(buf)), "\x00") {
			if id == "" {
				continue
			}
			var inst uint32
			if r, _, _ := pCMLocateDevNodeW.Call(uintptr(unsafe.Pointer(&inst)), uintptr(unsafe.Pointer(utf16(id))), 0); r != 0 {
				continue
			}
			var typ uint32
			var b [8]byte
			sz := uint32(len(b))
			if r, _, _ := pCMGetDevNodePropertyW.Call(uintptr(inst), uintptr(unsafe.Pointer(&keyBTBattery)), uintptr(unsafe.Pointer(&typ)),
				uintptr(unsafe.Pointer(&b[0])), uintptr(unsafe.Pointer(&sz)), 0); r != 0 || sz < 1 {
				continue
			}
			pct := int(b[0])
			name := devProp(inst, &keyFriendly)
			if name == "" {
				continue
			}
			for _, cut := range []string{" Hands-Free AG Audio", " Hands-Free AG", " Hands-Free", " Avrcp Transport", " Stereo"} {
				name = strings.TrimSuffix(name, cut)
			}
			if pct > best[name] {
				best[name] = pct
			}
		}
	}
	var out []BTView
	for n, p := range best {
		if p > 0 && p <= 100 {
			out = append(out, BTView{n, p})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ---------- Discord / Messenger / … unread counts from window titles ----------

var reUnread = regexp.MustCompile(`^\((\d+)\)\s|\((\d+)\)$|\s\((\d+)\)\s`)

func unreadFromTitle(title, exe string) (string, int) {
	t := strings.ToLower(title)
	app := ""
	switch {
	case exe == "discord.exe" || strings.Contains(t, "discord"):
		app = "Discord"
	case strings.Contains(t, "messenger") || strings.Contains(t, "facebook"):
		app = "Messenger"
	case exe == "whatsapp.exe" || strings.Contains(t, "whatsapp"):
		app = "WhatsApp"
	case exe == "telegram.exe" || strings.Contains(t, "telegram"):
		app = "Telegram"
	case strings.Contains(t, "instagram"):
		app = "Instagram"
	case strings.Contains(t, "gmail") || strings.Contains(t, "inbox ("):
		app = "Gmail"
	default:
		return "", 0
	}
	m := reUnread.FindStringSubmatch(title)
	if m == nil {
		return app, 0
	}
	for _, g := range m[1:] {
		if g != "" {
			n, _ := strconv.Atoi(g)
			return app, n
		}
	}
	return app, 0
}

// ---------- mouse / keyboard / phone classes ----------

var (
	guidMouse    = guid{0x378DE44C, 0x56EF, 0x11D1, [8]byte{0xBC, 0x8C, 0x00, 0xA0, 0xC9, 0x14, 0x05, 0xDD}}
	guidKeyboard = guid{0x884B96C3, 0x56EF, 0x11D1, [8]byte{0xBC, 0x8C, 0x00, 0xA0, 0xC9, 0x14, 0x05, 0xDD}}
	guidWPD      = guid{0x6AC27878, 0xA6FA, 0x4155, [8]byte{0xBA, 0x85, 0xF9, 0x8F, 0x49, 0x1D, 0x4F, 0x33}}
)

// parentName returns the name of the device's parent (the USB / Bluetooth device).
func parentName(instance string) (string, string) {
	var inst, parent uint32
	if r, _, _ := pCMLocateDevNodeW.Call(uintptr(unsafe.Pointer(&inst)), uintptr(unsafe.Pointer(utf16(instance))), 0); r != 0 {
		return "", instance
	}
	if pCMGetParent.Find() == nil {
		if r, _, _ := pCMGetParent.Call(uintptr(unsafe.Pointer(&parent)), uintptr(inst), 0); r == 0 {
			for _, k := range []*devPropKey{&keyBusDesc, &keyFriendly} {
				if n := devProp(parent, k); n != "" && !strings.Contains(strings.ToLower(n), "composite") {
					return n, fmt.Sprint(parent)
				}
			}
		}
	}
	for _, k := range []*devPropKey{&keyBusDesc, &keyFriendly, &keyDeviceDes} {
		if n := devProp(inst, k); n != "" {
			if i := strings.LastIndex(n, ";"); i >= 0 {
				n = n[i+1:]
			}
			return n, fmt.Sprint(parent)
		}
	}
	return "", fmt.Sprint(parent)
}

// ---------- plans (calendar + reminders + birthdays + to-dos) ----------

func (a *app) planRows(now time.Time) []PlanRow {
	var timed []struct {
		t time.Time
		r PlanRow
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dayLabel := func(t time.Time) string {
		d := int(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).Sub(today).Hours() / 24)
		switch d {
		case 0:
			return L("šiandien")
		case 1:
			return L("rytoj")
		}
		return shortDay(t) + " " + t.Format("01-02")
	}
	evs, _ := a.cal.Upcoming(now, 7)
	for i, e := range evs {
		if i >= 12 {
			break
		}
		when := e.Start.Format("15:04")
		if e.AllDay {
			when = ""
		}
		timed = append(timed, struct {
			t time.Time
			r PlanRow
		}{e.Start, PlanRow{Kind: "event", When: when, Text: e.Title, Extra: dayLabel(e.Start)}})
	}
	for _, r := range a.rem.List() {
		timed = append(timed, struct {
			t time.Time
			r PlanRow
		}{r.At, PlanRow{Kind: "remind", ID: r.ID, When: r.At.Format("15:04"), Text: r.Text, Extra: untilText(r.At, now)}})
	}
	for _, b := range a.cfg.Birthdays {
		d := b.nextDate(now)
		if d.Sub(today) <= 7*24*time.Hour {
			timed = append(timed, struct {
				t time.Time
				r PlanRow
			}{d, PlanRow{Kind: "bday", Text: fmt.Sprintf(L("%s – gimtadienis"), b.Name), Extra: dayLabel(d)}})
		}
	}
	sort.SliceStable(timed, func(i, j int) bool { return timed[i].t.Before(timed[j].t) })
	var out []PlanRow
	for _, t := range timed {
		out = append(out, t.r)
	}
	for _, t := range a.todos.List() {
		out = append(out, PlanRow{Kind: "todo", ID: t.ID, Text: t.Text, Done: t.Done})
	}
	return out
}

// ---------- background watchers ----------

type watchState struct {
	mu     sync.Mutex
	mic    string
	cam    string
	bt     []BTView
	dl     []string
	bright float64
}

func (a *app) startWatchers() {
	go func() {
		var dl dlWatcher
		lastBT := time.Time{}
		for {
			mic, cam := "", ""
			if a.cfg.MicCam {
				mic, cam = whoUses("microphone"), whoUses("webcam")
			}
			var fresh []string
			if a.cfg.Downloads {
				fresh = dl.check()
			}
			var bt []BTView
			doBT := time.Since(lastBT) > time.Minute
			if doBT {
				bt = btBatteries()
				lastBT = time.Now()
			}
			a.ws.mu.Lock()
			changed := mic != a.ws.mic || cam != a.ws.cam || len(fresh) > 0
			a.ws.mic, a.ws.cam = mic, cam
			a.ws.dl = append(a.ws.dl, fresh...)
			if doBT {
				a.ws.bt = bt
			}
			a.ws.mu.Unlock()
			if changed || doBT {
				onChange()
			}
			time.Sleep(1500 * time.Millisecond)
		}
	}()
}
