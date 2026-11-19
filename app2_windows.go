//go:build windows

package main

import (
	"errors"
	"fmt"
	"image"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	cfgmgr32 = syscall.NewLazyDLL("cfgmgr32.dll")
	iphlpapi = syscall.NewLazyDLL("iphlpapi.dll")
	shcore   = syscall.NewLazyDLL("shcore.dll")

	pRegisterDeviceNotificationW = user32.NewProc("RegisterDeviceNotificationW")
	pGetKeyboardLayout           = user32.NewProc("GetKeyboardLayout")
	pGetKeyState                 = user32.NewProc("GetKeyState")
	pSetWindowsHookExW           = user32.NewProc("SetWindowsHookExW")
	pCallNextHookEx              = user32.NewProc("CallNextHookEx")
	pEnumDisplayMonitors         = user32.NewProc("EnumDisplayMonitors")
	pGetMonitorInfoW             = user32.NewProc("GetMonitorInfoW")
	pGetDpiForMonitor            = shcore.NewProc("GetDpiForMonitor")
	pCMLocateDevNodeW            = cfgmgr32.NewProc("CM_Locate_DevNodeW")
	pCMGetDevNodePropertyW       = cfgmgr32.NewProc("CM_Get_DevNode_PropertyW")
	pGetIfTable                  = iphlpapi.NewProc("GetIfTable")
	pGetVolumeInformationW       = kernel32.NewProc("GetVolumeInformationW")
	pGetDiskFreeSpaceExW         = kernel32.NewProc("GetDiskFreeSpaceExW")
	pDragAcceptFiles             = shell32.NewProc("DragAcceptFiles")
	pDragQueryFileW              = shell32.NewProc("DragQueryFileW")
	pDragFinish                  = shell32.NewProc("DragFinish")
	pCoTaskMemFree               = ole32.NewProc("CoTaskMemFree")
	pPropVariantClear            = ole32.NewProc("PropVariantClear")
)

const (
	wmDeviceChange = 0x0219
	wmDropFiles    = 0x0233
	wmWheelHook    = wmUser + 3
	wmCall         = wmUser + 4
)

// ---------- running things on the UI thread (for the web pages) ----------

func (a *app) call(fn func()) {
	done := make(chan struct{})
	select {
	case a.calls <- func() { fn(); close(done) }:
	case <-time.After(3 * time.Second):
		return
	}
	pPostMessageW.Call(a.hwnd, wmCall, 0, 0)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

func (a *app) runCalls() {
	for {
		select {
		case fn := <-a.calls:
			fn()
		default:
			return
		}
	}
}

// ---------- monitors ----------

type monitor struct {
	h           uintptr
	x, y, w, ht int32
	primary     bool
	dpi         uint32
}

type rect struct{ L, T, R, B int32 }

type monitorInfo struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
}

var monList []monitor
var monCB = syscall.NewCallback(func(h, _, _, _ uintptr) uintptr {
	mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	pGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi)))
	m := monitor{h: h, x: mi.Monitor.L, y: mi.Monitor.T, w: mi.Monitor.R - mi.Monitor.L, ht: mi.Monitor.B - mi.Monitor.T, primary: mi.Flags&1 != 0, dpi: 96}
	if pGetDpiForMonitor.Find() == nil {
		var dx, dy uint32
		if r, _, _ := pGetDpiForMonitor.Call(h, 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r == 0 && dx > 0 {
			m.dpi = dx
		}
	}
	monList = append(monList, m)
	return 1
})

// monitors returns all screens, primary first.
func monitors() []monitor {
	monList = monList[:0]
	pEnumDisplayMonitors.Call(0, 0, monCB, 0)
	out := make([]monitor, 0, len(monList))
	for _, m := range monList {
		if m.primary {
			out = append([]monitor{m}, out...)
		} else {
			out = append(out, m)
		}
	}
	return out
}

func monitorNames() []string {
	var n []string
	for i, m := range monitors() {
		s := fmt.Sprintf(L("%d ekranas · %d×%d"), i+1, m.w, m.ht)
		if m.primary {
			s += L(" (pagrindinis)")
		}
		n = append(n, s)
	}
	return n
}

var sizeFactors = []float64{0.85, 1, 1.2}

// ---------- mouse wheel over the island (works even if Windows sends
// the wheel to another window) ----------

type msllHook struct {
	Pt        point
	MouseData uint32
	Flags     uint32
	Time      uint32
	Extra     uintptr
}

var mouseHookCB = syscall.NewCallback(func(nCode, wParam, lParam uintptr) uintptr {
	a := theApp
	if int32(nCode) >= 0 && a != nil && a.picking {
		info := (*msllHook)(unsafe.Pointer(lParam))
		switch wParam {
		case 0x0201: // left button down: pick this pixel
			pPostMessageW.Call(a.hwnd, wmPicked, 0, uintptr(uint16(info.Pt.X))|uintptr(uint16(info.Pt.Y))<<16)
			return 1
		case 0x0202:
			return 1
		}
	}
	if int32(nCode) >= 0 && wParam == wmMouseWheel && a != nil && a.visible {
		info := (*msllHook)(unsafe.Pointer(lParam))
		if (image.Point{int(info.Pt.X), int(info.Pt.Y)}).In(a.pillScreen) {
			pPostMessageW.Call(a.hwnd, wmWheelHook, uintptr(uint16(info.MouseData>>16)), 0)
			return 1 // eat it, so the window below doesn't scroll
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, nCode, wParam, lParam)
	return r
})

func (a *app) installMouseHook() {
	inst, _, _ := pGetModuleHandleW.Call(0)
	pSetWindowsHookExW.Call(14, mouseHookCB, inst, 0) // WH_MOUSE_LL
}

func (a *app) onWheel(delta int) {
	if k, _, _ := pGetAsyncKeyState.Call(vkControl); k&0x8000 != 0 && a.bright >= 0 {
		// Ctrl + wheel = screen brightness
		cur := a.bright * 100
		if a.brightWant >= 0 {
			cur = float64(a.brightWant)
		}
		a.setBrightness(int(math.Max(0, math.Min(100, cur+5*float64(delta)/120))))
		return
	}
	if a.hover && (a.tab == tabRemind || a.tab == tabSystem) {
		p := &a.planScroll
		if a.tab == tabSystem {
			p = &a.sysScroll
		}
		if delta < 0 {
			*p++
		} else if *p > 0 {
			*p--
		}
		a.dirty.Store(true)
		return
	}
	if a.hover && a.tab == tabClips && len(a.clips.Items) > 3 {
		maxScroll := len(a.clips.Items) - 3
		if delta < 0 && a.clipScroll < maxScroll {
			a.clipScroll++
		} else if delta > 0 && a.clipScroll > 0 {
			a.clipScroll--
		}
		a.dirty.Store(true)
		return
	}
	steps := float64(delta) / 120
	if lvl, muted, ok := a.vol.change(0.02 * steps); ok {
		a.showVolume(lvl, muted)
	} else {
		// fall back to the volume keys (Windows shows its own slider)
		vk := uintptr(0xAF)
		if steps < 0 {
			vk = 0xAE
		}
		for i := 0; i < int(math.Abs(steps)); i++ {
			mediaKey(vk)
		}
	}
}

func (a *app) showVolume(lvl float64, muted bool) {
	a.volume = &VolumeOSD{Level: lvl, Muted: muted, Until: time.Now().Add(1500 * time.Millisecond)}
	a.lastVol, a.lastMute = lvl, muted
	a.dirty.Store(true)
}

// ---------- keyboard layout + Caps Lock ----------

var langNames = map[uint16][2]string{
	0x27: {"LT", "Lietuvių"}, 0x09: {"EN", "English"}, 0x19: {"RU", "Русский"}, 0x15: {"PL", "Polski"},
	0x07: {"DE", "Deutsch"}, 0x22: {"UA", "Українська"}, 0x26: {"LV", "Latviešu"}, 0x25: {"ET", "Eesti"},
	0x0c: {"FR", "Français"}, 0x0a: {"ES", "Español"}, 0x10: {"IT", "Italiano"}, 0x1d: {"SV", "Svenska"},
}

func (a *app) pollKeyboard() {
	fg, _, _ := pGetForegroundWindow.Call()
	tid, _, _ := pGetWindowThreadProcessId.Call(fg, 0)
	hkl, _, _ := pGetKeyboardLayout.Call(tid)
	lang := uint16(hkl & 0x3ff)
	caps, _, _ := pGetKeyState.Call(0x14)
	capsOn := caps&1 != 0
	if a.kbdInit && a.cfg.Kbd {
		if lang != a.kbdLang && fg == a.kbdWin && lang != 0 {
			n, ok := langNames[lang]
			if !ok {
				n = [2]string{fmt.Sprintf("%02X", lang), ""}
			}
			txt := n[0]
			if n[1] != "" {
				txt += "  ·  " + n[1]
			}
			a.flashMsg(txt, flashBlue, 1300*time.Millisecond)
		}
		if capsOn != a.capsOn {
			if capsOn {
				a.flashMsg(L("Caps Lock įjungtas"), [3]uint8{0xff, 0x9f, 0x0a}, 1500*time.Millisecond)
			} else {
				a.flashMsg(L("Caps Lock išjungtas"), flashGray, 1200*time.Millisecond)
			}
		}
	}
	a.kbdInit, a.kbdLang, a.kbdWin, a.capsOn = true, lang, fg, capsOn
}

// ---------- USB and other devices ----------

type devBroadcastInterface struct {
	Size       uint32
	DeviceType uint32
	Reserved   uint32
	ClassGUID  guid
	Name       [1]uint16
}

var guidUSBDevice = guid{0xA5DCBF10, 0x6530, 0x11D2, [8]byte{0x90, 0x1F, 0x00, 0xC0, 0x4F, 0xB9, 0x51, 0xED}}

type devPropKey struct {
	Fmt guid
	Pid uint32
}

var (
	keyBusDesc   = devPropKey{guid{0x540b947e, 0x8b40, 0x45bc, [8]byte{0xa8, 0xa2, 0x6a, 0x0b, 0x89, 0x4c, 0xbd, 0xa2}}, 4}
	keyFriendly  = devPropKey{guid{0xa45c254e, 0xdf1c, 0x4efd, [8]byte{0x80, 0x20, 0x67, 0xd1, 0x46, 0xa8, 0x50, 0xe0}}, 14}
	keyDeviceDes = devPropKey{guid{0xa45c254e, 0xdf1c, 0x4efd, [8]byte{0x80, 0x20, 0x67, 0xd1, 0x46, 0xa8, 0x50, 0xe0}}, 2}
)

func (a *app) registerDevices() {
	f := devBroadcastInterface{DeviceType: 5}
	f.Size = uint32(unsafe.Sizeof(f))
	pRegisterDeviceNotificationW.Call(a.hwnd, uintptr(unsafe.Pointer(&f)), 4) // ALL_INTERFACE_CLASSES
	pDragAcceptFiles.Call(a.hwnd, 1)
	// Windows tells us whenever the screen brightness changes (keys, slider, …)
	if pRegisterPowerSettingNotification.Find() == nil {
		pRegisterPowerSettingNotification.Call(a.hwnd, uintptr(unsafe.Pointer(&guidMonitorBrightness)), 0)
	}
}

// GUID_VIDEO_CURRENT_MONITOR_BRIGHTNESS: data is a DWORD 0..100.
var guidMonitorBrightness = guid{0x8FFEE2C6, 0x2D01, 0x46BE, [8]byte{0xAD, 0xB9, 0x39, 0x8A, 0xDD, 0xC5, 0xB4, 0xFF}}

var pRegisterPowerSettingNotification = user32.NewProc("RegisterPowerSettingNotification")

// onPowerSetting shows the brightness slider when the brightness changed.
func (a *app) onPowerSetting(lparam uintptr) {
	if lparam == 0 {
		return
	}
	g := *(*guid)(unsafe.Pointer(lparam))
	if g != guidMonitorBrightness || *(*uint32)(unsafe.Pointer(lparam + 16)) < 4 {
		return
	}
	p := int(*(*uint32)(unsafe.Pointer(lparam + 20)))
	if p < 0 || p > 100 {
		return
	}
	old := a.bright
	a.bright = float64(p) / 100
	if !a.brightInit {
		a.brightInit = true // the first message is just the current value
		return
	}
	if old < 0 || math.Abs(old-a.bright) > 0.001 || time.Now().Before(a.volumeUntil()) {
		a.volume = &VolumeOSD{Level: a.bright, Until: time.Now().Add(1500 * time.Millisecond), Bright: true}
		a.dirty.Store(true)
	}
}

func (a *app) volumeUntil() time.Time {
	if a.volume != nil && a.volume.Bright {
		return a.volume.Until
	}
	return time.Time{}
}

// instanceFromPath: \\?\USB#VID_1&PID_2#SER#{guid} -> USB\VID_1&PID_2\SER
func instanceFromPath(p string) string {
	p = strings.TrimPrefix(p, `\\?\`)
	if i := strings.LastIndex(p, "#{"); i > 0 {
		p = p[:i]
	}
	return strings.ReplaceAll(p, "#", `\`)
}

func devProp(inst uint32, key *devPropKey) string {
	var typ, size uint32
	buf := make([]uint16, 256)
	size = uint32(len(buf) * 2)
	if r, _, _ := pCMGetDevNodePropertyW.Call(uintptr(inst), uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&typ)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0); r != 0 {
		return ""
	}
	return strings.TrimSpace(syscall.UTF16ToString(buf))
}

func deviceName(instance string) string {
	if pCMLocateDevNodeW.Find() != nil {
		return ""
	}
	var inst uint32
	if r, _, _ := pCMLocateDevNodeW.Call(uintptr(unsafe.Pointer(&inst)), uintptr(unsafe.Pointer(utf16(instance))), 0); r != 0 {
		return ""
	}
	generic := func(s string) bool {
		l := strings.ToLower(s)
		return s == "" || strings.Contains(l, "composite") || strings.Contains(l, "usb input device") || strings.Contains(l, "mass storage")
	}
	for _, k := range []*devPropKey{&keyBusDesc, &keyFriendly, &keyDeviceDes} {
		if n := devProp(inst, k); !generic(n) {
			if i := strings.LastIndex(n, ";"); i >= 0 { // "@usb.inf,%x%;Name"
				n = n[i+1:]
			}
			return n
		}
	}
	return ""
}

func (a *app) onDeviceChange(wparam, lparam uintptr) {
	if lparam == 0 || !a.cfg.Devices {
		return
	}
	arrive := wparam == 0x8000
	remove := wparam == 0x8004
	if !arrive && !remove {
		return
	}
	hdr := (*[3]uint32)(unsafe.Pointer(lparam))
	switch hdr[1] {
	case 2: // volume (USB stick, card…)
		mask := *(*uint32)(unsafe.Pointer(lparam + 12))
		for i := 0; i < 26; i++ {
			if mask&(1<<i) == 0 {
				continue
			}
			root := string(rune('A'+i)) + `:\`
			if remove {
				a.flashMsg(L("Atjungtas diskas ")+root[:2], flashGray, 2500*time.Millisecond)
				continue
			}
			label := make([]uint16, 64)
			pGetVolumeInformationW.Call(uintptr(unsafe.Pointer(utf16(root))), uintptr(unsafe.Pointer(&label[0])), 64, 0, 0, 0, 0, 0)
			var free, total uint64
			pGetDiskFreeSpaceExW.Call(uintptr(unsafe.Pointer(utf16(root))), uintptr(unsafe.Pointer(&free)), uintptr(unsafe.Pointer(&total)), 0)
			txt := L("USB diskas ") + root[:2]
			if l := syscall.UTF16ToString(label); l != "" {
				txt += " · " + l
			}
			if total > 0 {
				txt += fmt.Sprintf(" · %.0f GB", float64(total)/(1<<30))
			}
			a.devSpecific = time.Now()
			a.pendingDev = nil
			a.flash = &Flash{Text: txt + L(" · atidaryti"), Color: flashBlue, Until: time.Now().Add(6 * time.Second), Open: root}
			a.anim.Pop()
			a.dirty.Store(true)
		}
	case 5: // device interface
		di := (*devBroadcastInterface)(unsafe.Pointer(lparam))
		n := (int(di.Size) - 28) / 2
		if n <= 0 || n > 1024 {
			return
		}
		path := syscall.UTF16ToString(unsafe.Slice(&di.Name[0], n))
		switch di.ClassGUID {
		case guidMouse, guidKeyboard, guidWPD:
			kind := map[guid]string{guidMouse: "mouse", guidKeyboard: "keyboard", guidWPD: "phone"}[di.ClassGUID]
			a.onSpecificDevice(kind, instanceFromPath(path), arrive)
			return
		}
		isUSB := di.ClassGUID == guidUSBDevice
		isBT := strings.Contains(strings.ToUpper(path), "BTHLE#DEV_") || strings.Contains(strings.ToUpper(path), "BTHENUM#DEV_")
		if !isUSB && !isBT {
			return
		}
		inst := instanceFromPath(path)
		key := strings.ToUpper(inst)
		if t, ok := a.devSeen[key]; ok && time.Since(t) < 3*time.Second {
			return
		}
		a.devSeen[key] = time.Now()
		kind := L("USB įrenginys")
		if isBT {
			kind = L("Bluetooth įrenginys")
		}
		if arrive {
			name := deviceName(inst)
			if name != "" {
				a.devNames[key] = name
			} else {
				name = kind
			}
			// wait a moment: a mouse, keyboard, phone or drive message may follow
			a.pendingDev = &Flash{Text: L("Prijungta: ") + name, Color: flashBlue, Until: time.Now().Add(900 * time.Millisecond)}
		} else {
			name := a.devNames[key]
			if name == "" {
				name = kind
			}
			a.flashMsg(L("Atjungta: ")+name, flashGray, 3*time.Second)
		}
	}
}

// ---------- internet ----------

type netMon struct {
	mu       sync.Mutex
	info     NetInfo
	lastIn   map[string]uint32
	lastOut  map[string]uint32
	lastAt   time.Time
	OnChange func(online bool)
}

// sampleSpeed sums traffic of the active network adapters.
func (n *netMon) sampleSpeed() {
	var size uint32
	pGetIfTable.Call(0, uintptr(unsafe.Pointer(&size)), 0)
	if size == 0 || size > 1<<22 {
		return
	}
	buf := make([]byte, size)
	if r, _, _ := pGetIfTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0); r != 0 {
		return
	}
	count := int(*(*uint32)(unsafe.Pointer(&buf[0])))
	const rowSize = 860
	in, out := map[string]uint32{}, map[string]uint32{}
	for i := 0; i < count && 4+(i+1)*rowSize <= len(buf); i++ {
		row := buf[4+i*rowSize:]
		typ := *(*uint32)(unsafe.Pointer(&row[516]))
		oper := *(*uint32)(unsafe.Pointer(&row[544]))
		if typ == 24 || oper != 5 { // skip loopback and adapters that are down
			continue
		}
		mac := fmt.Sprintf("%x", row[532:540])
		if mac == "0000000000000000" {
			continue
		}
		ib := *(*uint32)(unsafe.Pointer(&row[552]))
		ob := *(*uint32)(unsafe.Pointer(&row[576]))
		if ib >= in[mac] { // the same adapter can appear several times (filters): keep the max
			in[mac], out[mac] = ib, ob
		}
	}
	now := time.Now()
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.lastIn != nil {
		dt := now.Sub(n.lastAt).Seconds()
		var din, dout float64
		for k, v := range in {
			if p, ok := n.lastIn[k]; ok {
				din += float64(v - p) // uint32 wrap-around is fine
			}
			if p, ok := n.lastOut[k]; ok {
				dout += float64(out[k] - p)
			}
		}
		if dt > 0.2 {
			// smooth a little
			n.info.Down = n.info.Down*0.4 + din/dt*0.6
			n.info.Up = n.info.Up*0.4 + dout/dt*0.6
		}
	}
	n.lastIn, n.lastOut, n.lastAt = in, out, now
}

func online() bool {
	for _, addr := range []string{"1.1.1.1:443", "8.8.8.8:53", "9.9.9.9:443"} {
		c, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err == nil {
			c.Close()
			return true
		}
	}
	return false
}

func (n *netMon) Start() {
	go func() {
		next := time.Now()
		for {
			n.sampleSpeed()
			if !time.Now().Before(next) {
				on := online()
				n.mu.Lock()
				had, was := n.info.Has, n.info.Online
				n.info.Has, n.info.Online = true, on
				n.mu.Unlock()
				if had && on != was && n.OnChange != nil {
					n.OnChange(on)
				}
				next = time.Now().Add(10 * time.Second)
				if !on {
					next = time.Now().Add(4 * time.Second)
				}
			}
			time.Sleep(time.Second)
		}
	}()
}

func (n *netMon) Info() NetInfo {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.info
}

// ---------- file shelf ----------

func (a *app) onDropFiles(h uintptr) {
	defer pDragFinish.Call(h)
	n, _, _ := pDragQueryFileW.Call(h, 0xFFFFFFFF, 0, 0)
	var paths []string
	for i := uintptr(0); i < n; i++ {
		l, _, _ := pDragQueryFileW.Call(h, i, 0, 0)
		buf := make([]uint16, l+1)
		pDragQueryFileW.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), l+1)
		paths = append(paths, syscall.UTF16ToString(buf))
	}
	added := a.shelf.Add(paths...)
	a.tab = tabShelf
	if added > 0 {
		a.flashMsg(fmt.Sprintf(L("Į lentyną: %d %s"), added, filesWord(added)), flashGreen, 2*time.Second)
	} else {
		a.flashMsg(L("Jau yra lentynoje"), flashGray, 1500*time.Millisecond)
	}
}

// setClipboardFiles puts files on the clipboard (paste with Ctrl+V in Explorer).
func setClipboardFiles(hwnd uintptr, paths []string) bool {
	var list []uint16
	for _, p := range paths {
		u, _ := syscall.UTF16FromString(p)
		list = append(list, u...)
	}
	list = append(list, 0)
	const hdr = 20 // DROPFILES
	total := hdr + len(list)*2
	if !openClipboard(hwnd) {
		return false
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()
	h, _, _ := pGlobalAlloc.Call(gmemMoveable|0x40, uintptr(total))
	if h == 0 {
		return false
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		pGlobalFree.Call(h)
		return false
	}
	b := unsafe.Slice((*byte)(unsafe.Pointer(p)), total)
	*(*uint32)(unsafe.Pointer(&b[0])) = hdr // pFiles
	*(*uint32)(unsafe.Pointer(&b[16])) = 1  // fWide
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(&b[hdr])), len(list)), list)
	pGlobalUnlock.Call(h)
	if r, _, _ := pSetClipboardData.Call(15, h); r == 0 { // CF_HDROP
		pGlobalFree.Call(h)
		return false
	}
	return true
}

// ---------- region screenshot via the Windows snipping overlay ----------

func (a *app) snipRegion() {
	a.snipUntil = time.Now().Add(2 * time.Minute)
	shellOpen(a.hwnd, "ms-screenclip:")
}

// readClipboardImage reads a CF_DIB bitmap from the clipboard.
func readClipboardImage(hwnd uintptr) *image.RGBA {
	if r, _, _ := pIsClipboardFormatAvailable.Call(8); r == 0 {
		return nil
	}
	if !openClipboard(hwnd) {
		return nil
	}
	defer pCloseClipboard.Call()
	h, _, _ := pGetClipboardData.Call(8)
	if h == 0 {
		return nil
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		return nil
	}
	defer pGlobalUnlock.Call(h)
	size, _, _ := pGlobalSize.Call(h)
	b := unsafe.Slice((*byte)(unsafe.Pointer(p)), int(size))
	if len(b) < 40 {
		return nil
	}
	hs := int(*(*uint32)(unsafe.Pointer(&b[0])))
	w := int(*(*int32)(unsafe.Pointer(&b[4])))
	hh := int(*(*int32)(unsafe.Pointer(&b[8])))
	bpp := int(*(*uint16)(unsafe.Pointer(&b[14])))
	comp := *(*uint32)(unsafe.Pointer(&b[16]))
	clr := int(*(*uint32)(unsafe.Pointer(&b[32])))
	if w <= 0 || hh == 0 || (bpp != 32 && bpp != 24) {
		return nil
	}
	off := hs
	if comp == 3 && hs == 40 {
		off += 12
	}
	off += clr * 4
	topDown := hh < 0
	if topDown {
		hh = -hh
	}
	stride := ((w*bpp + 31) / 32) * 4
	if off+stride*hh > len(b) {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, w, hh))
	bytesPP := bpp / 8
	for y := 0; y < hh; y++ {
		sy := hh - 1 - y
		if topDown {
			sy = y
		}
		row := b[off+sy*stride:]
		for x := 0; x < w; x++ {
			s := x * bytesPP
			d := img.PixOffset(x, y)
			img.Pix[d], img.Pix[d+1], img.Pix[d+2], img.Pix[d+3] = row[s+2], row[s+1], row[s], 255
		}
	}
	return img
}

// ---------- Backend for the settings / karaoke pages ----------

func (a *app) Settings() SettingsDTO {
	var s SettingsDTO
	a.call(func() {
		c := a.cfg
		city, _ := readCity()
		links, _ := os.ReadFile(ensureLinks())
		s = SettingsDTO{Theme: c.Theme, Size: c.Size, Monitor: c.Monitor, RobotColor: c.RobotColor, Face: c.Face,
			Sound: c.Sound, Notify: c.Notify, LyricsBar: c.LyricsBar, Translate: c.Translate, AutoHide: c.AutoHide,
			Kbd: c.Kbd, Devices: c.Devices, NetNotify: c.NetNotify, Autostart: autostartOn(), Hooks: a.hooksOn,
			City: city, Links: strings.ReplaceAll(string(links), "\r\n", "\n"), Monitors: monitorNames(),
			Lang: c.Lang, CoverGlow: c.CoverGlow, KaraokeFill: c.KaraokeFill, ClipTrans: c.ClipTrans, MicCam: c.MicCam,
			Downloads: c.Downloads, Messages: c.Messages, Moods: c.Moods, Fact: c.Fact, DND: c.DND, DNDFocus: c.DNDFocus,
			CalURL: c.CalURL, Birthdays: FormatBirthdays(c.Birthdays), Clocks: c.Clocks}
		s.Keys, s.KeyBusy = map[string]string{}, map[string]string{}
		for k, v := range a.keys {
			s.Keys[k] = v
		}
		for k, v := range a.keysBusy {
			if v != a.keys[k] {
				s.KeyBusy[k] = v
			}
		}
		s.Notice, a.setNotice = a.setNotice, ""
		for _, rc := range robotColors {
			s.Colors = append(s.Colors, L(rc.Name))
		}
		for _, fc := range robotFaces {
			s.Faces = append(s.Faces, L(fc))
		}
		for _, wc := range worldCities {
			s.Cities = append(s.Cities, wc.Name)
			s.CityNames = append(s.CityNames, L(wc.Name))
		}
	})
	return s
}

func (a *app) ApplySettings(s SettingsDTO) error {
	var err error
	a.call(func() {
		c := &a.cfg
		relayout := c.Theme != s.Theme || c.Size != s.Size || c.Monitor != s.Monitor
		if s.Theme == "light" {
			c.Theme = "light"
		} else {
			c.Theme = "dark"
		}
		if s.Size >= 0 && s.Size <= 2 {
			c.Size = s.Size
		}
		c.Monitor = s.Monitor
		if s.RobotColor >= 0 && s.RobotColor < len(robotColors) {
			c.RobotColor = s.RobotColor
		}
		if s.Face >= 0 && s.Face < len(robotFaces) {
			c.Face = s.Face
		}
		c.Sound, c.Notify, c.LyricsBar, c.Translate, c.AutoHide = s.Sound, s.Notify, s.LyricsBar, s.Translate, s.AutoHide
		c.Kbd, c.Devices, c.NetNotify = s.Kbd, s.Devices, s.NetNotify
		c.CoverGlow, c.KaraokeFill, c.ClipTrans, c.MicCam, c.Downloads = s.CoverGlow, s.KaraokeFill, s.ClipTrans, s.MicCam, s.Downloads
		c.Messages, c.Moods, c.Fact, c.DND, c.DNDFocus = s.Messages, s.Moods, s.Fact, s.DND, s.DNDFocus
		c.CalURL = strings.TrimSpace(s.CalURL)
		c.Birthdays = ParseBirthdays(s.Birthdays)
		if len(s.Clocks) > 3 {
			s.Clocks = s.Clocks[:3]
		}
		c.Clocks = s.Clocks
		if a.applyKeys(s.Keys) {
			if note := a.registerHotkeys(); note != "" {
				a.setNotice = note
			}
		}
		if s.Lang != "" && s.Lang != c.Lang {
			c.Lang = s.Lang
			setLang(c.Lang)
			relayout = true // clears cached texts
		}
		if s.Autostart != autostartOn() {
			setAutostart(s.Autostart)
		}
		if s.Hooks != a.hooksOn {
			if e := SetHooks(s.Hooks); e != nil {
				err = errors.New(L("Claude Code: ") + e.Error())
			}
			a.hooksOn = HooksInstalled()
		}
		if city, _ := readCity(); strings.TrimSpace(s.City) != city {
			if strings.TrimSpace(s.City) == "" {
				os.Remove(cityPath())
			} else {
				os.MkdirAll(filepath.Dir(cityPath()), 0o755)
				os.WriteFile(cityPath(), []byte(strings.TrimSpace(s.City)+"\r\n"), 0o644)
			}
		}
		if s.Links != "" {
			os.WriteFile(ensureLinks(), []byte(strings.ReplaceAll(strings.ReplaceAll(s.Links, "\r\n", "\n"), "\n", "\r\n")), 0o644)
			a.links, a.linksMod = readLinks()
		}
		c.Save()
		if relayout {
			setTheme(c.Theme == "light")
			a.layout()
		}
		a.dirty.Store(true)
	})
	return err
}

// applyKeys stores shortcuts chosen on the settings page; true if any changed.
func (a *app) applyKeys(in map[string]string) bool {
	changed := false
	for _, act := range hotkeyActions {
		want, ok := in[act.Name]
		if !ok || want == "" || want == a.keys[act.Name] {
			continue
		}
		if want == "default" {
			if _, had := a.cfg.Keys[act.Name]; had {
				delete(a.cfg.Keys, act.Name)
				changed = true
			}
			continue
		}
		h, ok := ParseHotkey(want)
		if !ok {
			a.setNotice = L("Toks klavišų derinys netinka – reikia Ctrl, Alt arba Win ir raidės, skaičiaus, tarpo ar F1–F12")
			continue
		}
		if a.cfg.Keys == nil {
			a.cfg.Keys = map[string]string{}
		}
		if a.cfg.Keys[act.Name] != h.String() || a.keys[act.Name] != h.String() {
			a.cfg.Keys[act.Name] = h.String()
			changed = true
		}
	}
	return changed
}

func (a *app) Reminders() []Reminder { return a.rem.List() }

func (a *app) Favs() []Fav      { return a.favs.List() }
func (a *app) DelFav(i int)     { a.favs.DeleteAt(i) }
func (a *app) Todos() []Todo    { return a.todos.List() }
func (a *app) AddTodo(t string) { a.todos.Add(t); onChange() }
func (a *app) ToggleTodo(id int) {
	a.todos.Toggle(id)
	onChange()
}
func (a *app) DelTodo(id int) {
	a.todos.Delete(id)
	onChange()
}

func (a *app) AddReminder(text string, at time.Time) {
	a.rem.Add(text, at)
	onChange()
}

func (a *app) DelReminder(id int) {
	a.rem.Delete(id)
	onChange()
}

func (a *app) AdjustOffset(d float64) {
	a.call(func() {
		if m := a.media.Current(); m != nil {
			a.lyrOffset[m.Key()] += d
			a.dirty.Store(true)
		}
	})
}

func (a *app) Now() NowDTO {
	var d NowDTO
	a.call(func() {
		m := a.media.Current()
		if m == nil {
			return
		}
		c1, c2 := trackHue(m.Artist + m.Title)
		d = NowDTO{Key: m.Key(), Title: m.Title, Artist: m.Artist, Playing: m.Playing(), Pos: m.Position(time.Now()), End: m.End,
			Estimated: m.Estimated, Offset: a.lyrOffset[m.Key()], HasCover: a.coverRefFor(m) != "",
			Colors: [2]string{hexColor(c1.R, c1.G, c1.B), hexColor(c2.R, c2.G, c2.B)}}
		if l := a.lyrics.Get(m); l != nil {
			d.State = l.State
			d.Plain = l.Plain
			for i, ln := range l.Lines {
				dto := LineDTO{T: ln.T, Tx: ln.Text}
				if a.cfg.Translate && i < len(l.Trans) {
					dto.Tr = l.Trans[i]
				}
				d.Lines = append(d.Lines, dto)
			}
		}
	})
	return d
}

func hexColor(r, g, b uint8) string { return fmt.Sprintf("#%02x%02x%02x", r, g, b) }

func (a *app) Cover() []byte {
	var ref string
	a.call(func() { ref = a.coverRefFor(a.media.Current()) })
	return CoverJPEG(ref, 600)
}

// coverRefFor: the player's own cover if it gave a readable one, otherwise
// one found online (Deezer / iTunes).
func (a *app) coverRefFor(m *Media) string {
	if m == nil {
		return ""
	}
	if ref := a.media.CoverPath(); ref != "" && ref != a.badCover {
		return ref
	}
	return a.webCov.Get(m)
}

var _ = strconv.Itoa
