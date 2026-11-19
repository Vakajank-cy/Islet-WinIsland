//go:build windows

package main

import (
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")

	pRegisterClassExW         = user32.NewProc("RegisterClassExW")
	pCreateWindowExW          = user32.NewProc("CreateWindowExW")
	pDefWindowProcW           = user32.NewProc("DefWindowProcW")
	pGetMessageW              = user32.NewProc("GetMessageW")
	pTranslateMessage         = user32.NewProc("TranslateMessage")
	pDispatchMessageW         = user32.NewProc("DispatchMessageW")
	pPostQuitMessage          = user32.NewProc("PostQuitMessage")
	pPostMessageW             = user32.NewProc("PostMessageW")
	pSetTimer                 = user32.NewProc("SetTimer")
	pUpdateLayeredWindow      = user32.NewProc("UpdateLayeredWindow")
	pGetDC                    = user32.NewProc("GetDC")
	pReleaseDC                = user32.NewProc("ReleaseDC")
	pGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	pTrackMouseEvent          = user32.NewProc("TrackMouseEvent")
	pCreatePopupMenu          = user32.NewProc("CreatePopupMenu")
	pAppendMenuW              = user32.NewProc("AppendMenuW")
	pTrackPopupMenu           = user32.NewProc("TrackPopupMenu")
	pDestroyMenu              = user32.NewProc("DestroyMenu")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pMessageBeep              = user32.NewProc("MessageBeep")
	pKeybdEvent               = user32.NewProc("keybd_event")
	pLoadCursorW              = user32.NewProc("LoadCursorW")
	pSetCursor                = user32.NewProc("SetCursor")
	pShowWindow               = user32.NewProc("ShowWindow")
	pDestroyWindow            = user32.NewProc("DestroyWindow")
	pGetCursorPos             = user32.NewProc("GetCursorPos")
	pMessageBoxW              = user32.NewProc("MessageBoxW")
	pSetProcessDpiAwarenessCx = user32.NewProc("SetProcessDpiAwarenessContext")
	pSetProcessDPIAware       = user32.NewProc("SetProcessDPIAware")
	pGetDpiForSystem          = user32.NewProc("GetDpiForSystem")
	pSetWindowPos             = user32.NewProc("SetWindowPos")

	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pGetDeviceCaps      = gdi32.NewProc("GetDeviceCaps")

	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")

	pRegOpenKeyExW    = advapi32.NewProc("RegOpenKeyExW")
	pRegSetValueExW   = advapi32.NewProc("RegSetValueExW")
	pRegDeleteValueW  = advapi32.NewProc("RegDeleteValueW")
	pRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")
	pRegCloseKey      = advapi32.NewProc("RegCloseKey")
)

const (
	wsPopup         = 0x80000000
	wsExLayered     = 0x00080000
	wsExTopmost     = 0x00000008
	wsExToolWindow  = 0x00000080
	wsExNoActivate  = 0x08000000
	wmDestroy       = 0x0002
	wmSetCursor     = 0x0020
	wmMouseActivate = 0x0021
	wmDisplayChange = 0x007E
	wmTimer         = 0x0113
	wmMouseMove     = 0x0200
	wmLButtonUp     = 0x0202
	wmRButtonUp     = 0x0205
	wmMouseLeave    = 0x02A3
	wmDpiChanged    = 0x02E0
	wmUser          = 0x0400
	wmWake          = wmUser + 1
	maNoActivate    = 3
	tmeLeave        = 0x2
	ulwAlpha        = 0x2
	swShowNoActive  = 4
	mfString        = 0x0
	mfChecked       = 0x8
	mfGrayed        = 0x1
	mfSeparator     = 0x800
	tpmReturnCmd    = 0x0100
	tpmRightButton  = 0x0002
	idcArrow        = 32512
	idcHand         = 32649
	hkcu            = 0x80000001
	keySetValue     = 0x0002
	keyQueryValue   = 0x0001
	regSZ           = 1
	hwndTopmost     = ^uintptr(0) // -1
	swpNoActivate   = 0x0010
	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
)

type point struct{ X, Y int32 }
type size struct{ CX, CY int32 }

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

type trackMouseEvent struct {
	Size      uint32
	Flags     uint32
	HwndTrack uintptr
	HoverTime uint32
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type blendFunction struct {
	BlendOp, BlendFlags, SourceConstantAlpha, AlphaFormat byte
}

// ---------- app ----------

type app struct {
	hwnd      uintptr
	st        *State
	cfg       Config
	r         *Renderer
	anim      Anim
	start     time.Time
	last      time.Time
	hover     bool
	over      bool
	overSince time.Time
	lastFrame time.Time
	tracking  bool
	mouse     point
	hoverBtn  string
	hits      []Hit
	hooksOn   bool
	lastVer   int
	lastMin   int
	physW     int32
	physH     int32
	posX      int32
	posY      int32
	memDC     uintptr
	bmp       uintptr
	bits      unsafe.Pointer
	dirty     atomic.Bool
	arrow     uintptr
	hand      uintptr

	tab        int
	timer      *Pomodoro
	clips      ClipHistory
	clipScroll int
	note       string
	noteMod    time.Time
	noteCheck  time.Time
	flash      *Flash
	trayIcon   uintptr

	visible      bool
	prevRect     image.Rectangle
	hiddenByUser bool
	hiddenByFS   bool
	fsCheck      time.Time
	media        *MediaWatcher
	lyrics       *LyricsStore
	usageMu      sync.Mutex
	usage        Usage
	sampler      sysSampler
	sys          SysInfo
	sysCheck     time.Time

	weather    *WeatherStore
	links      []Link
	linksMod   time.Time
	linksCheck time.Time
	volume     *VolumeOSD
	shot       *ShotView
	ai         *AIWatcher
	aiReady    chan [2]any
	ocrDone    chan [2]string
	coverRef   string
	coverImg   image.Image
	keys       map[string]string // action -> shortcut in use
	keysBusy   map[string]string // action -> wanted shortcut another app holds
	setNotice  string            // one-off message for the settings page
	webCov     *WebCovers
	badCover   string // player cover that could not be read

	calls       chan func()
	pillScreen  image.Rectangle
	vol         volMon
	lastVol     float64
	lastMute    bool
	volInit     bool
	volCheck    time.Time
	kbdInit     bool
	kbdLang     uint16
	kbdWin      uintptr
	capsOn      bool
	kbdCheck    time.Time
	devSeen     map[string]time.Time
	devNames    map[string]string
	net         *netMon
	rem         *Reminders
	remCheck    time.Time
	shelf       *Shelf
	snipUntil   time.Time
	lyrOffset   map[string]float64
	typing      bool
	cmd         *CmdView
	game        *Game
	card        *Card
	picker      *PickerView
	picking     bool
	pickedColor [3]uint8
	pinHover    time.Time
	lastTrans   string
	peaks       [5]float64
	peakHist    []float64
	todos       *Todos
	favs        *Favorites
	focus       *FocusStats
	cal         *CalStore
	ws          watchState
	sleepAt     time.Time
	prevMic     string
	prevCam     string
	prevUnread  map[string]int
	pendingDev  *Flash
	devSpecific time.Time
	devPair     map[string]time.Time
	calSeen     map[string]bool
	bright      float64
	brightWant  int
	brightBusy  bool
	coverCol    *[3]uint8
	coverColRef string
	brightInit  bool
	planScroll  int
	sysScroll   int
}

var theApp *app

func utf16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

func messageBox(text, title string) {
	pMessageBoxW.Call(0, uintptr(unsafe.Pointer(utf16(text))), uintptr(unsafe.Pointer(utf16(title))), 0x40)
}

func main() {
	runtime.LockOSThread()
	pCoInitEx.Call(0, 2) // COM (STA) for volume control

	// Sharp text on high-DPI screens.
	if pSetProcessDpiAwarenessCx.Find() == nil {
		pSetProcessDpiAwarenessCx.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2 (-4)
	} else if pSetProcessDPIAware.Find() == nil {
		pSetProcessDPIAware.Call()
	}

	st := NewState()
	ln, err := startServer(st)
	if err != nil {
		messageBox(L("Islet jau veikia (žiūrėk ekrano viršuje)."), appName)
		return
	}
	defer ln.Close()

	migrateDataDir()
	migrateAutostart()
	a := &app{st: st, cfg: LoadConfig(), start: time.Now(), last: time.Now()}
	a.timer = NewPomodoro(a.cfg.TimerPreset)
	setTheme(a.cfg.Theme == "light")
	a.calls = make(chan func(), 16)
	a.devSeen, a.devNames = map[string]time.Time{}, map[string]string{}
	a.lyrOffset = map[string]float64{}
	a.net = &netMon{}
	a.rem = LoadReminders(filepath.Join(filepath.Dir(configPath()), "priminimai.json"))
	a.shelf = LoadShelf(filepath.Join(filepath.Dir(configPath()), "lentyna.json"))
	setLang(a.cfg.Lang)
	a.todos = LoadTodos(dataPath("uzduotys.json"))
	a.favs = LoadFavorites(dataPath("megstamos.json"))
	a.focus = LoadFocus(dataPath("fokusas.json"))
	a.cal = &CalStore{}
	a.cal.SetURL(a.cfg.CalURL)
	a.prevUnread, a.devPair, a.calSeen = map[string]int{}, map[string]time.Time{}, map[string]bool{}
	a.bright, a.brightWant = -1, -1
	a.visible = true
	a.media = &MediaWatcher{}
	a.weather = NewWeatherStore()
	a.ai = NewAIWatcher()
	a.aiReady = make(chan [2]any, 4)
	a.ocrDone = make(chan [2]string, 4)
	a.links, a.linksMod = readLinks()
	a.lyrics = NewLyricsStore()
	theApp = a

	// First run: connect Claude Code (autostart is a menu option).
	if !a.cfg.FirstRunDone {
		a.cfg.FirstRunDone = true
		if err := SetHooks(true); err != nil {
			messageBox(L("Nepavyko prijungti Claude Code:\n")+err.Error(), appName)
		}
		a.cfg.Save()
	}
	a.hooksOn = HooksInstalled()

	a.createWindow()
	a.addTray()
	a.startClipboard()
	a.reloadNote()
	if note := a.registerHotkeys(); note != "" {
		a.flashMsg(note, flashBlue, 5*time.Second)
	} else if !a.cfg.KeyNote {
		// the command bar moved off Ctrl+Alt+Space (Claude uses it)
		a.cfg.KeyNote = true
		a.cfg.Save()
		if k := a.keys["cmd"]; k != "" {
			a.flashMsg(withKey(L("Komandų juosta"), k), flashBlue, 5*time.Second)
		}
	}
	wake := func() { onChange() }
	a.media.OnTrack = wake
	a.lyrics.OnReady = wake
	a.webCov = NewWebCovers(dataPath("covers"))
	a.webCov.OnReady = wake
	a.media.Start()
	a.media.OnOCR = func(_ string, text, errMsg string) {
		a.ocrDone <- [2]string{text, errMsg}
		onChange()
	}
	a.weather.OnReady = wake
	a.weather.Start()
	a.ai.OnState = wake
	a.ai.OnReady = func(name string, h uintptr) {
		a.aiReady <- [2]any{name, h}
		onChange()
	}
	a.ai.Start()
	a.net.OnChange = func(on bool) {
		a.call(func() {
			if !a.cfg.NetNotify {
				return
			}
			if on {
				a.flashMsg(L("Internetas vėl veikia"), flashGreen, 3*time.Second)
			} else {
				a.flashMsg(L("Dingo interneto ryšys"), [3]uint8{0xff, 0x45, 0x3a}, 5*time.Second)
				a.notify(L("Dingo internetas"), L("Patikrink Wi-Fi ar laidą"))
			}
		})
	}
	a.net.Start()
	a.registerDevices()
	a.installMouseHook()
	webBackend = a
	a.cal.OnReady = wake
	a.cal.Start()
	ratesReady = func() { a.call(a.refreshCmd) }
	go indexApps()
	a.startWatchers()
	a.media.OnBright = func(p int) {
		a.call(func() {
			a.brightBusy = false
			if p < 0 {
				a.bright = -1
				return
			}
			a.bright = float64(p) / 100
			if a.brightWant >= 0 && a.brightWant != p {
				a.setBrightness(a.brightWant) // the user kept scrolling
			} else {
				a.brightWant = -1
			}
		})
	}
	go func() {
		time.Sleep(4 * time.Second)
		a.media.Brightness(-1)
	}()
	go func() {
		for {
			u := ScanUsage(claudeProjectDirs(), time.Now())
			a.usageMu.Lock()
			a.usage = u
			a.usageMu.Unlock()
			time.Sleep(time.Minute)
		}
	}()
	onChange = func() {
		a.dirty.Store(true)
		pPostMessageW.Call(a.hwnd, wmWake, 0, 0)
	}

	var m msg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func systemScale() float64 {
	dpi := uintptr(96)
	if pGetDpiForSystem.Find() == nil {
		dpi, _, _ = pGetDpiForSystem.Call()
	} else {
		hdc, _, _ := pGetDC.Call(0)
		dpi, _, _ = pGetDeviceCaps.Call(hdc, 88) // LOGPIXELSX
		pReleaseDC.Call(0, hdc)
	}
	if dpi == 0 {
		dpi = 96
	}
	return float64(dpi) / 96
}

func (a *app) createWindow() {
	inst, _, _ := pGetModuleHandleW.Call(0)
	a.arrow, _, _ = pLoadCursorW.Call(0, idcArrow)
	a.hand, _, _ = pLoadCursorW.Call(0, idcHand)
	cls := utf16("IsletWnd")
	wc := wndClassEx{
		WndProc:   syscall.NewCallback(wndProc),
		Instance:  inst,
		Cursor:    a.arrow,
		ClassName: cls,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	a.hwnd, _, _ = pCreateWindowExW.Call(
		wsExLayered|wsExTopmost|wsExToolWindow|wsExNoActivate,
		uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(utf16(appName))),
		wsPopup, 0, 0, 10, 10, 0, 0, inst, 0)

	a.layout()
	pShowWindow.Call(a.hwnd, swShowNoActive)
	pSetTimer.Call(a.hwnd, 1, 16, 0)
	a.render()
}

// layout (re)creates the drawing surface for the current DPI / screen size.
func (a *app) layout() {
	scale := systemScale()
	mons := monitors()
	var mx, my, mw int32
	if len(mons) > 0 {
		m := mons[0]
		if a.cfg.Monitor > 0 && a.cfg.Monitor < len(mons) {
			m = mons[a.cfg.Monitor]
		}
		mx, my, mw = m.x, m.y, m.w
		if m.dpi > 0 {
			scale = float64(m.dpi) / 96
		}
	} else {
		w, _, _ := pGetSystemMetrics.Call(0) // SM_CXSCREEN
		mw = int32(w)
	}
	scale *= sizeFactors[a.cfg.Size]
	a.r = NewRenderer(scale)
	a.coverRef = "" // re-scale album art
	a.physW = int32(canvasW*scale + 0.999)
	a.physH = int32(canvasH*scale + 0.999)
	a.posX = mx + (mw-a.physW)/2
	a.posY = my

	if a.bmp != 0 {
		pDeleteObject.Call(a.bmp)
		pDeleteDC.Call(a.memDC)
	}
	screen, _, _ := pGetDC.Call(0)
	a.memDC, _, _ = pCreateCompatibleDC.Call(screen)
	pReleaseDC.Call(0, screen)
	bi := bitmapInfoHeader{Width: a.physW, Height: -a.physH, Planes: 1, BitCount: 32}
	bi.Size = uint32(unsafe.Sizeof(bi))
	a.bmp, _, _ = pCreateDIBSection.Call(a.memDC, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&a.bits)), 0, 0)
	pSelectObject.Call(a.memDC, a.bmp)
	a.prevRect = image.Rect(0, 0, int(a.physW), int(a.physH))
	if a.anim.W == 0 {
		v := a.view()
		a.anim.W, a.anim.H = a.r.Target(v)
	}
	a.dirty.Store(true)
}

func (a *app) view() View {
	v := View{
		Snap:     a.st.Snapshot(),
		Hover:    a.hover,
		HoverBtn: a.hoverBtn,
		Now:      time.Now(),
		T:        time.Since(a.start).Seconds(),
		W:        a.anim.W,
		H:        a.anim.H,
		HooksOn:  a.hooksOn,

		Tab:        a.tab,
		Timer:      a.timerView(time.Now()),
		Clips:      a.clips.Items,
		ClipScroll: a.clipScroll,
		Note:       a.note,
		RobotColor: a.cfg.RobotColor,
		Face:       a.cfg.Face,
		Flash:      a.flash,
		Media:      a.currentMedia(),
		Usage:      a.currentUsage(),
		Sys:        a.sys,
		LyricsOff:  !a.cfg.LyricsBar,
	}
	if v.Media != nil {
		v.Lyrics = a.lyrics.Get(v.Media)
		if ref := a.coverRefFor(v.Media); ref != a.coverRef {
			a.coverRef = ref
			a.coverImg = LoadCover(ref, int(52*a.r.S+0.5), 12*a.r.S)
			if a.coverImg == nil && ref != "" && ref == a.media.CoverPath() {
				a.badCover = ref // unreadable: look online next frame
			}
		}
		v.Cover = a.coverImg
	}
	v.Weather = a.weather.Current()
	v.Links = a.links
	v.Volume = a.volume
	v.AI = a.ai.State()
	v.Shot = a.shot
	v.Reminders = a.rem.List()
	v.Shelf = a.shelf.Paths
	v.Net = a.net.Info()
	v.Translate = a.cfg.Translate
	v.X = a.extraView(v)
	if v.Media != nil {
		v.LyricOffset = a.lyrOffset[v.Media.Key()]
		if a.cfg.Translate && v.Lyrics != nil && v.Lyrics.State == lyrSynced && (v.Lyrics.Trans == nil || v.Lyrics.TransLang != curLang) {
			a.lyrics.EnsureTranslation(v.Media)
		}
	}
	return v
}

func (a *app) currentMedia() *Media { return a.media.Current() }

func (a *app) currentUsage() Usage {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	return a.usage
}

// tick runs ~60 times a second; it only redraws when something moves.
func (a *app) tick() {
	now := time.Now()
	dt := now.Sub(a.last).Seconds()
	a.last = now
	if a.st.Tick(now) {
		a.dirty.Store(true)
	}
	if msg, ok := a.timer.Tick(now); ok {
		c := flashGreen
		if a.timer.Phase == 0 {
			c = [3]uint8{0xff, 0x69, 0x61}
		}
		a.flashMsg(msg, c, 8*time.Second)
		if a.cfg.Sound {
			pMessageBeep.Call(0x40)
		}
		a.notify("Laikmatis", msg)
	}
	for _, n := range a.st.TakeNotices() {
		a.notify(n.Title, n.Body)
	}
	a.runCalls()
	a.tickFeatures(now, dt)
	if now.Sub(a.remCheck) >= time.Second {
		a.remCheck = now
		for _, r := range a.rem.Due(now) {
			a.flash = &Flash{Text: L("Priminimas: ") + r.Text, Color: [3]uint8{0xff, 0x9f, 0x0a}, Until: now.Add(15 * time.Second)}
			a.anim.Pop()
			if a.cfg.Sound {
				pMessageBeep.Call(0x30)
			}
			a.notify(L("Priminimas"), r.Text)
			a.dirty.Store(true)
		}
	}
	if now.Sub(a.kbdCheck) >= 250*time.Millisecond {
		a.kbdCheck = now
		a.pollKeyboard()
	}
	if now.Sub(a.volCheck) >= 250*time.Millisecond {
		a.volCheck = now
		if name, changed := a.vol.check(false); changed && a.cfg.Devices {
			if name == "" {
				name = L("kitas įrenginys")
			}
			a.flashMsg(L("Garsas: ")+name, flashBlue, 3*time.Second)
		}
		if lvl, muted, ok := a.vol.get(); ok {
			if a.volInit && (math.Abs(lvl-a.lastVol) > 0.004 || muted != a.lastMute) {
				a.showVolume(lvl, muted) // volume changed anywhere (keys, mixer, …)
			}
			a.volInit, a.lastVol, a.lastMute = true, lvl, muted
		}
	}
	if now.Sub(a.sysCheck) >= 2*time.Second {
		a.sysCheck = now
		prev := a.sys
		a.sys = a.sampler.sample()
		a.powerEvents(prev, a.sys)
	}
	if now.Sub(a.fsCheck) >= time.Second {
		a.fsCheck = now
		a.hiddenByFS = a.cfg.AutoHide && fullscreenBusy()
	}
	permission := a.st.Snapshot().Perm != nil
	a.setVisible(permission || (!a.hiddenByUser && !a.hiddenByFS))
	select {
	case r := <-a.aiReady:
		name, h := r[0].(string), r[1].(uintptr)
		a.flash = &Flash{Text: name + L(" atsakymas paruoštas · spausk"), Color: [3]uint8{0x10, 0xa3, 0x7f}, Until: now.Add(10 * time.Second), Action: h}
		if a.cfg.Sound {
			pMessageBeep.Call(0x40)
		}
		a.notify(name+L(" atsakė"), L("Atsakymas paruoštas"))
		a.dirty.Store(true)
	case r := <-a.ocrDone:
		text, errMsg := r[0], r[1]
		if a.shot != nil {
			a.shot.Until = now.Add(4 * time.Second)
		}
		switch {
		case strings.TrimSpace(text) != "":
			setClipboardText(a.hwnd, text)
			n := strings.Count(text, "\n") + 1
			if a.shot != nil {
				a.shot.Busy = fmt.Sprintf(L("Nukopijuota %d eil. teksto"), n)
			}
		case errMsg != "":
			if a.shot != nil {
				a.shot.Busy = L("Nepavyko: ") + errMsg
			}
		default:
			if a.shot != nil {
				a.shot.Busy = L("Tekstas nerastas")
			}
		}
		a.dirty.Store(true)
	default:
	}
	if a.shot != nil {
		if a.over && now.Before(a.shot.Until) && a.shot.Until.Sub(now) < 2*time.Second {
			a.shot.Until = now.Add(2 * time.Second) // keep open while the mouse is on it
		}
		if now.After(a.shot.Until) {
			a.shot = nil
			a.dirty.Store(true)
		}
	}
	if a.volume != nil && now.After(a.volume.Until) {
		a.volume = nil
		a.dirty.Store(true)
	}
	if a.flash != nil && now.After(a.flash.Until) {
		a.flash = nil
		a.dirty.Store(true)
	}
	if a.hover && a.tab == tabLinks && now.Sub(a.linksCheck) > 2*time.Second {
		a.linksCheck = now
		if _, mod := readLinks(); !mod.Equal(a.linksMod) {
			a.links, a.linksMod = readLinks()
			a.dirty.Store(true)
		}
	}
	if a.hover && a.tab == tabNote && now.Sub(a.noteCheck) > time.Second {
		a.noteCheck = now
		if _, mod := readNote(); !mod.Equal(a.noteMod) {
			a.reloadNote()
			a.dirty.Store(true)
		}
	}
	for _, s := range a.st.TakeSounds() {
		if a.cfg.Sound {
			switch s {
			case SoundDone:
				pMessageBeep.Call(0x40) // asterisk
			case SoundPermission:
				pMessageBeep.Call(0x30) // exclamation
			default:
				pMessageBeep.Call(0)
			}
		}
	}

	// Expand on hover only after a short delay, so passing the mouse over
	// the top of the screen doesn't pop it open.
	wantHover := (a.over && now.Sub(a.overSince) > 220*time.Millisecond) || now.Before(a.pinHover)
	if wantHover != a.hover {
		a.hover = wantHover
		a.dirty.Store(true)
	}

	if !a.visible {
		return
	}
	v := a.view()
	tw, th := a.r.Target(v)
	moving := a.anim.Step(tw, th, dt)

	// Frame budget: smooth while moving, gentle while animating, rare when idle.
	interval := time.Minute
	switch v.Snap.Mode {
	case ModeWorking, ModePermission:
		interval = 40 * time.Millisecond
	case ModeDone, ModeWaiting:
		interval = 66 * time.Millisecond
	default:
		if math_mod(v.T, 4.3) < 0.2 { // blink
			interval = 33 * time.Millisecond
		} else if v.flashOn() {
			interval = 66 * time.Millisecond
		} else if a.cmd != nil || a.game != nil || a.picking {
			interval = 16 * time.Millisecond
		} else if v.volOn() || v.shotOn() || v.AI.Generating || a.card != nil {
			interval = 66 * time.Millisecond
		} else if txt, _ := v.musicLine(); txt != "" || (v.Hover && v.Tab == tabMusic && v.Media != nil) {
			interval = 66 * time.Millisecond // equalizer + lyrics
		} else if a.timer.Running || v.Hover {
			interval = 250 * time.Millisecond
		}
	}
	if v.Snap.Version != a.lastVer {
		a.lastVer = v.Snap.Version
		a.dirty.Store(true)
	}
	if now.Minute() != a.lastMin {
		a.lastMin = now.Minute()
		a.dirty.Store(true)
	}
	if moving || a.dirty.Swap(false) || now.Sub(a.lastFrame) >= interval {
		a.lastFrame = now
		a.render()
	}
}

func math_mod(x, y float64) float64 { return x - y*float64(int(x/y)) }

func (a *app) render() {
	img, hits := a.r.Draw(a.view())
	a.hits = hits
	a.blit(img)
}

func (a *app) blit(img *image.RGBA) {
	if a.bits == nil {
		return
	}
	n := int(a.physW) * int(a.physH) * 4
	dst := unsafe.Slice((*byte)(a.bits), n)
	w := int(a.physW)
	full := image.Rect(0, 0, w, int(a.physH))
	cur := img.Bounds().Intersect(full)
	// clear what the previous frame drew outside the new area
	dirty := cur.Union(a.prevRect).Intersect(full)
	for y := dirty.Min.Y; y < dirty.Max.Y; y++ {
		row := y * w * 4
		for x := dirty.Min.X; x < dirty.Max.X; x++ {
			if (image.Point{x, y}).In(cur) {
				continue
			}
			d := row + x*4
			dst[d], dst[d+1], dst[d+2], dst[d+3] = 0, 0, 0, 0
		}
	}
	src := img.Pix
	for y := cur.Min.Y; y < cur.Max.Y; y++ {
		so := img.PixOffset(cur.Min.X, y)
		do := (y*w + cur.Min.X) * 4
		for x := cur.Min.X; x < cur.Max.X; x++ {
			// image.RGBA is premultiplied RGBA; Windows wants premultiplied BGRA
			dst[do] = src[so+2]
			dst[do+1] = src[so+1]
			dst[do+2] = src[so]
			dst[do+3] = src[so+3]
			so += 4
			do += 4
		}
	}
	a.prevRect = cur
	for _, h := range a.hits {
		if h.Name == "pill" {
			a.pillScreen = image.Rect(int(h.X0)+int(a.posX), int(h.Y0)+int(a.posY), int(h.X1)+int(a.posX), int(h.Y1)+int(a.posY))
		}
	}
	screen, _, _ := pGetDC.Call(0)
	pos := point{a.posX, a.posY}
	sz := size{a.physW, a.physH}
	zero := point{}
	bf := blendFunction{0, 0, 255, 1}
	pUpdateLayeredWindow.Call(a.hwnd, screen, uintptr(unsafe.Pointer(&pos)), uintptr(unsafe.Pointer(&sz)),
		a.memDC, uintptr(unsafe.Pointer(&zero)), 0, uintptr(unsafe.Pointer(&bf)), ulwAlpha)
	pReleaseDC.Call(0, screen)
}

func (a *app) hitAt(x, y float64) string {
	name := ""
	for _, h := range a.hits {
		if h.Contains(x, y) && h.Name != "pill" {
			name = h.Name
		}
	}
	return name
}

func (a *app) overPill(x, y float64) bool {
	for _, h := range a.hits {
		if h.Name == "pill" && h.Contains(x, y) {
			return true
		}
	}
	return false
}

func lparamXY(l uintptr) (float64, float64) {
	return float64(int16(l & 0xffff)), float64(int16((l >> 16) & 0xffff))
}

func wndProc(hwnd, umsg, wparam, lparam uintptr) uintptr {
	a := theApp
	if taskbarCreatedMsg != 0 && umsg == taskbarCreatedMsg {
		a.addTray() // Explorer restarted
		return 0
	}
	switch umsg {
	case wmHotkey:
		switch wparam {
		case 2:
			a.snipRegion()
			return 0
		case 3:
			if a.cmd != nil {
				a.closeOverlays()
			} else {
				a.openCmd("")
			}
			return 0
		case 4:
			a.startPicker()
			return 0
		}
		a.hiddenByUser = !a.hiddenByUser
		if !a.hiddenByUser {
			a.flashMsg(L("Islet vėl matoma"), flashBlue, 2*time.Second)
		}
		a.tick()
		return 0
	case wmCall:
		a.runCalls()
		return 0
	case wmKeyDown:
		a.onKey(wparam)
		return 0
	case wmChar:
		a.onChar(wparam)
		return 0
	case wmActivate:
		if wparam&0xffff == 0 && a.typing { // clicked elsewhere
			a.closeOverlays()
		}
	case wmPicked:
		a.picked(int32(int16(lparam&0xffff)), int32(int16(lparam>>16)))
		return 0
	case wmWheelHook:
		a.onWheel(int(int16(uint16(wparam))))
		return 0
	case wmDeviceChange:
		a.onDeviceChange(wparam, lparam)
		return 1
	case wmPowerBroad:
		if wparam == 0x8013 { // PBT_POWERSETTINGCHANGE
			a.onPowerSetting(lparam)
		}
		return 1
	case wmDropFiles:
		a.onDropFiles(wparam)
		return 0
	case wmClipboardUpdate:
		if time.Now().Before(a.snipUntil) {
			if img := readClipboardImage(hwnd); img != nil {
				a.snipUntil = time.Time{}
				a.showShot(img, false)
				return 0
			}
		}
		if txt, ok := readClipboardText(hwnd); ok && a.clips.Add(txt) {
			a.clipScroll = 0
			if a.cfg.ClipTrans && curLang == "lt" && isMostlyForeign(txt) && !a.dndOn() && a.cmd == nil {
				a.card = &Card{Title: L("Išversti nukopijuotą tekstą?"), Body: oneLine(txt), Color: [3]uint8{0x5e, 0x5c, 0xe6},
					Buttons: []CardBtn{{"trans:clip", L("Versti")}, {"card:close", L("Ne")}}, Until: time.Now().Add(5 * time.Second)}
			}
			a.dirty.Store(true)
		}
		return 0
	case wmMouseWheel:
		a.onWheel(int(int16(wparam >> 16)))
		return 0
	case wmTray:
		switch lparam & 0xffff {
		case wmRButtonUp, wmLButtonUp:
			a.menu()
		}
		return 0
	case wmTimer, wmWake:
		a.tick()
		return 0
	case wmMouseActivate:
		if a.typing {
			return maActivate
		}
		return maNoActivate
	case wmMouseMove:
		if !a.tracking {
			tme := trackMouseEvent{Flags: tmeLeave, HwndTrack: hwnd}
			tme.Size = uint32(unsafe.Sizeof(tme))
			pTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
			a.tracking = true
		}
		x, y := lparamXY(lparam)
		over := a.overPill(x, y)
		if over && !a.over {
			a.overSince = time.Now()
		}
		a.over = over
		if btn := a.hitAt(x, y); btn != a.hoverBtn {
			a.hoverBtn = btn
			a.dirty.Store(true)
		}
		return 0
	case wmMouseLeave:
		a.tracking = false
		a.over = false
		a.hoverBtn = ""
		a.dirty.Store(true)
		return 0
	case wmSetCursor:
		if a.hoverBtn != "" {
			pSetCursor.Call(a.hand)
			return 1
		}
	case wmLButtonUp:
		x, y := lparamXY(lparam)
		name := a.hitAt(x, y)
		if name == "brightbar" {
			for _, h := range a.hits {
				if h.Name == "brightbar" && h.Contains(x, y) {
					a.setBrightness(int(math.Round(clamp01((x-h.X0)/(h.X1-h.X0)) * 100)))
				}
			}
			return 0
		}
		if name == "seekbar" {
			for _, h := range a.hits {
				if h.Name == "seekbar" && h.Contains(x, y) {
					if m := a.media.Current(); m != nil && m.End > 0 {
						a.media.Seek(clamp01((x-h.X0)/(h.X1-h.X0)) * m.End)
					}
				}
			}
			a.dirty.Store(true)
			return 0
		}
		a.click(name)
		return 0
	case wmRButtonUp:
		a.menu()
		return 0
	case wmDpiChanged, wmDisplayChange:
		a.layout()
		a.render()
		return 0
	case wmDestroy:
		a.removeTray()
		for _, act := range hotkeyActions {
			pUnregisterHotKey.Call(hwnd, uintptr(act.ID))
		}
		pRemoveClipboardFormatListener.Call(hwnd)
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, umsg, wparam, lparam)
	return r
}

func mediaKey(vk uintptr) {
	pKeybdEvent.Call(vk, 0, 1, 0)   // KEYEVENTF_EXTENDEDKEY
	pKeybdEvent.Call(vk, 0, 1|2, 0) // + KEYUP
}

func (a *app) click(name string) {
	if a.clickExtra(name) {
		return
	}
	switch name {
	case "allow":
		a.st.Answer("allow")
	case "deny":
		a.st.Answer("deny")
	case "terminal":
		a.st.Answer("")
	case "play":
		mediaKey(0xB3)
	case "next":
		mediaKey(0xB0)
	case "prev":
		mediaKey(0xB1)
	case "":
		if a.flash != nil && time.Now().Before(a.flash.Until) {
			if a.flash.Action != 0 {
				focusWindow(a.flash.Action)
				a.flash = nil
			} else if a.flash.Open != "" {
				shellOpen(a.hwnd, a.flash.Open)
				a.flash = nil
			}
		}
		a.st.Dismiss()
	}
	a.dirty.Store(true)
}

func (a *app) menu() {
	h, _, _ := pCreatePopupMenu.Call()
	defer pDestroyMenu.Call(h)
	add := func(id uintptr, text string, checked bool) {
		flags := uintptr(mfString)
		if checked {
			flags |= mfChecked
		}
		pAppendMenuW.Call(h, flags, id, uintptr(unsafe.Pointer(utf16(text))))
	}
	pAppendMenuW.Call(h, mfString|mfGrayed, 0, uintptr(unsafe.Pointer(utf16(appName))))
	pAppendMenuW.Call(h, mfSeparator, 0, 0)
	add(1, L("Claude Code ryšys"), a.hooksOn)
	add(2, L("Garsas"), a.cfg.Sound)
	add(3, L("Paleisti kartu su Windows"), autostartOn())
	add(4, L("Windows pranešimai"), a.cfg.Notify)
	pAppendMenuW.Call(h, mfSeparator, 0, 0)
	add(7, L("Slėpti per pilną ekraną"), a.cfg.AutoHide)
	add(8, L("Dainos žodžiai juostelėje"), a.cfg.LyricsBar)
	if a.hiddenByUser {
		add(6, withKey(L("Rodyti salelę"), a.keys["hide"]), false)
	} else {
		add(6, withKey(L("Slėpti dabar"), a.keys["hide"]), false)
	}
	pAppendMenuW.Call(h, mfSeparator, 0, 0)
	add(11, L("Nustatymai…"), false)
	add(12, L("Netrukdyti"), a.cfg.DND)
	add(13, withKey(L("Komandų juosta"), a.keys["cmd"]), false)
	add(10, withKey(L("Ekrano nuotrauka"), a.keys["shot"]), false)
	add(5, L("Atidaryti užrašą"), false)
	pAppendMenuW.Call(h, mfSeparator, 0, 0)
	add(9, L("Uždaryti"), false)

	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWindow.Call(a.hwnd)
	cmd, _, _ := pTrackPopupMenu.Call(h, tpmReturnCmd|tpmRightButton, uintptr(pt.X), uintptr(pt.Y), 0, a.hwnd, 0)
	switch cmd {
	case 1:
		if err := SetHooks(!a.hooksOn); err != nil {
			messageBox(L("Nepavyko pakeisti Claude Code nustatymų:\n")+err.Error(), appName)
		}
		a.hooksOn = HooksInstalled()
	case 2:
		a.cfg.Sound = !a.cfg.Sound
		a.cfg.Save()
	case 3:
		setAutostart(!autostartOn())
	case 4:
		a.cfg.Notify = !a.cfg.Notify
		a.cfg.Save()
		if a.cfg.Notify {
			a.notify(appName, L("Pranešimai įjungti"))
		}
	case 5:
		a.openNote()
	case 10:
		a.snipRegion()
	case 11:
		shellOpen(a.hwnd, settingsURL(""))
	case 12:
		a.cfg.DND = !a.cfg.DND
		a.cfg.Save()
	case 13:
		a.openCmd("")
	case 6:
		a.hiddenByUser = !a.hiddenByUser
	case 7:
		a.cfg.AutoHide = !a.cfg.AutoHide
		a.cfg.Save()
	case 8:
		a.cfg.LyricsBar = !a.cfg.LyricsBar
		a.cfg.Save()
	case 9:
		pDestroyWindow.Call(a.hwnd)
	}
	a.dirty.Store(true)
}

// ---------- autostart (HKCU\...\Run) ----------

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const runValue = appName
const oldRunValue = "DynamicIsland" // name used by older versions

func openRunKey(access uintptr) (uintptr, bool) {
	var k uintptr
	r, _, _ := pRegOpenKeyExW.Call(hkcu, uintptr(unsafe.Pointer(utf16(runKey))), 0, access, uintptr(unsafe.Pointer(&k)))
	return k, r == 0
}

func autostartOn() bool {
	k, ok := openRunKey(keyQueryValue)
	if !ok {
		return false
	}
	defer pRegCloseKey.Call(k)
	r, _, _ := pRegQueryValueExW.Call(k, uintptr(unsafe.Pointer(utf16(runValue))), 0, 0, 0, 0)
	return r == 0
}

// migrateAutostart: older versions used another name in the Run key, and the
// exe may have been moved or renamed since. Point the entry at this exe.
func migrateAutostart() {
	k, ok := openRunKey(keyQueryValue | keySetValue)
	if !ok {
		return
	}
	r, _, _ := pRegQueryValueExW.Call(k, uintptr(unsafe.Pointer(utf16(oldRunValue))), 0, 0, 0, 0)
	had := r == 0
	if had {
		pRegDeleteValueW.Call(k, uintptr(unsafe.Pointer(utf16(oldRunValue))))
	}
	pRegCloseKey.Call(k)
	if had || autostartOn() {
		setAutostart(true)
	}
}

func setAutostart(on bool) {
	k, ok := openRunKey(keySetValue)
	if !ok {
		return
	}
	defer pRegCloseKey.Call(k)
	if !on {
		pRegDeleteValueW.Call(k, uintptr(unsafe.Pointer(utf16(runValue))))
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	val, _ := syscall.UTF16FromString(`"` + exe + `"`)
	pRegSetValueExW.Call(k, uintptr(unsafe.Pointer(utf16(runValue))), 0, regSZ,
		uintptr(unsafe.Pointer(&val[0])), uintptr(len(val)*2))
}
