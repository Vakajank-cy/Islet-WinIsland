//go:build windows

package main

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// AIWatcher follows ChatGPT / Claude windows (desktop apps or browser tabs).
// While such a window is in the background it asks Windows UI Automation
// whether a "Stop…" button is visible (= an answer is being written).
// When that button disappears, the answer is ready.
//
// To stay light it only scans windows you left in the last 30 minutes,
// at most every 1.5 s, and stops scanning a window once it is idle.

var (
	pEnumWindows                = user32.NewProc("EnumWindows")
	pGetWindowTextW             = user32.NewProc("GetWindowTextW")
	pIsWindowVisible            = user32.NewProc("IsWindowVisible")
	pGetForegroundWindow        = user32.NewProc("GetForegroundWindow")
	pGetWindowThreadProcessId   = user32.NewProc("GetWindowThreadProcessId")
	pIsIconic                   = user32.NewProc("IsIconic")
	pOpenProcess                = kernel32.NewProc("OpenProcess")
	pCloseHandle                = kernel32.NewProc("CloseHandle")
	pQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")

	clsidCUIAutomation = guid{0xFF48DBA4, 0x60EF, 0x4201, [8]byte{0xAA, 0x87, 0x54, 0x10, 0x3E, 0xEF, 0x59, 0x4E}}
	iidIUIAutomation   = guid{0x30CBE57D, 0xD9D0, 0x452A, [8]byte{0xAB, 0x13, 0x7A, 0xC5, 0xAC, 0x48, 0x25, 0xEE}}
)

type aiWin struct {
	hwnd       uintptr
	name       string
	lastFront  time.Time
	generating bool
	scan       bool // keep scanning (just left it, or it is writing)
	lastScan   time.Time
}

type AIWatcher struct {
	unread  []UnreadView
	mu      sync.Mutex
	wins    map[uintptr]*aiWin
	state   AIState
	OnReady func(name string, hwnd uintptr)
	OnState func()
	uia     uintptr
	cond    uintptr
}

func NewAIWatcher() *AIWatcher { return &AIWatcher{wins: map[uintptr]*aiWin{}} }

func (w *AIWatcher) Unread() []UnreadView {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.unread
}

func (w *AIWatcher) State() AIState {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state
}

func windowText(h uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := pGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), 256)
	return syscall.UTF16ToString(buf[:n])
}

func processName(h uintptr) string {
	var pid uint32
	pGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
	ph, _, _ := pOpenProcess.Call(0x1000, 0, uintptr(pid)) // PROCESS_QUERY_LIMITED_INFORMATION
	if ph == 0 {
		return ""
	}
	defer pCloseHandle.Call(ph)
	buf := make([]uint16, 520)
	n := uint32(len(buf))
	if r, _, _ := pQueryFullProcessImageNameW.Call(ph, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n))); r == 0 {
		return ""
	}
	p := syscall.UTF16ToString(buf[:n])
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		p = p[i+1:]
	}
	return strings.ToLower(p)
}

var browsers = []string{"chrome.exe", "msedge.exe", "firefox.exe", "opera.exe", "brave.exe", "vivaldi.exe", "arc.exe"}

// classify returns "ChatGPT", "Claude" or "".
func classify(title, exe string) string {
	t := strings.ToLower(title)
	if strings.Contains(t, "claude code") || strings.Contains(t, "dynamic island") {
		return ""
	}
	switch exe {
	case "chatgpt.exe":
		return "ChatGPT"
	case "claude.exe":
		return "Claude"
	}
	isBrowser := false
	for _, b := range browsers {
		if exe == b {
			isBrowser = true
		}
	}
	if !isBrowser {
		return ""
	}
	if strings.Contains(t, "chatgpt") {
		return "ChatGPT"
	}
	if strings.Contains(t, "claude") {
		return "Claude"
	}
	return ""
}

var enumFound []uintptr
var enumCB = syscall.NewCallback(func(h, _ uintptr) uintptr {
	if v, _, _ := pIsWindowVisible.Call(h); v != 0 {
		enumFound = append(enumFound, h)
	}
	return 1
})

func (w *AIWatcher) Start() {
	go func() {
		runtime.LockOSThread()
		pCoInitEx.Call(0, 0)                                                   // COINIT_MULTITHREADED
		pCoCreateInst.Call(uintptr(unsafe.Pointer(&clsidCUIAutomation)), 0, 1, // CLSCTX_INPROC_SERVER
			uintptr(unsafe.Pointer(&iidIUIAutomation)), uintptr(unsafe.Pointer(&w.uia)))
		if w.uia != 0 {
			w.cond = w.makeCondition()
		}
		for {
			w.step()
			time.Sleep(1500 * time.Millisecond)
		}
	}()
}

// makeCondition builds "Button whose name contains one of the stop phrases".
// (Plain "stop" would also match e.g. "Desktop".)
func (w *AIWatcher) makeCondition() uintptr {
	var vb [24]byte
	vb[0] = 3                                 // VT_I4
	*(*int32)(unsafe.Pointer(&vb[8])) = 50000 // UIA_ButtonControlTypeId
	var btn uintptr
	comCall(w.uia, 23, 30003, uintptr(unsafe.Pointer(&vb)), uintptr(unsafe.Pointer(&btn))) // CreatePropertyCondition(ControlType)
	if btn == 0 {
		return 0
	}
	var names uintptr
	for _, phrase := range []string{"stop generating", "stop streaming", "stop response", "stop respond", "sustabdyti"} {
		var v [24]byte
		v[0] = 8 // VT_BSTR
		bs, _, _ := pSysAllocStr.Call(uintptr(unsafe.Pointer(utf16(phrase))))
		*(*uintptr)(unsafe.Pointer(&v[8])) = bs
		var c uintptr
		// CreatePropertyConditionEx(Name, value, IgnoreCase|MatchSubstring) – Windows 10 1809+
		comCall(w.uia, 24, 30005, uintptr(unsafe.Pointer(&v)), 3, uintptr(unsafe.Pointer(&c)))
		pSysFreeStr.Call(bs)
		if c == 0 {
			continue
		}
		if names == 0 {
			names = c
			continue
		}
		var or uintptr
		comCall(w.uia, 28, names, c, uintptr(unsafe.Pointer(&or))) // CreateOrCondition
		if or != 0 {
			names = or
		}
	}
	if names == 0 {
		return 0
	}
	var and uintptr
	comCall(w.uia, 25, btn, names, uintptr(unsafe.Pointer(&and))) // CreateAndCondition
	return and
}

// hasStopButton asks UI Automation whether the window shows a Stop button.
func (w *AIWatcher) hasStopButton(hwnd uintptr) (bool, bool) {
	if w.uia == 0 || w.cond == 0 {
		return false, false
	}
	var el uintptr
	if comCall(w.uia, 6, hwnd, uintptr(unsafe.Pointer(&el))) != 0 || el == 0 { // ElementFromHandle
		return false, false
	}
	defer comRelease(el)
	var found uintptr
	if comCall(el, 5, 4, w.cond, uintptr(unsafe.Pointer(&found))) != 0 { // FindFirst(TreeScope_Descendants)
		return false, false
	}
	if found != 0 {
		comRelease(found)
		return true, true
	}
	return false, true
}

func (w *AIWatcher) step() {
	enumFound = enumFound[:0]
	pEnumWindows.Call(enumCB, 0)
	fg, _, _ := pGetForegroundWindow.Call()
	now := time.Now()

	seen := map[uintptr]bool{}
	counts := map[string]int{}
	for _, h := range enumFound {
		title := windowText(h)
		if title == "" {
			continue
		}
		exe := processName(h)
		if app, n := unreadFromTitle(title, exe); app != "" && n > counts[app] {
			counts[app] = n
		}
		name := classify(title, exe)
		if name == "" {
			continue
		}
		seen[h] = true
		aw := w.wins[h]
		if aw == nil {
			aw = &aiWin{hwnd: h, name: name}
			w.wins[h] = aw
		}
		aw.name = name
		if h == fg {
			aw.lastFront = now
			aw.scan = true // will scan once right after you leave it
			if aw.generating {
				aw.generating = false
			}
			continue
		}
		if !aw.scan || now.Sub(aw.lastFront) > 30*time.Minute {
			continue
		}
		if mini, _, _ := pIsIconic.Call(h); mini != 0 {
			continue // minimized windows usually don't update their UI tree
		}
		gen, ok := w.hasStopButton(h)
		if !ok {
			continue
		}
		aw.lastScan = now
		if aw.generating && !gen {
			if w.OnReady != nil {
				w.OnReady(aw.name, h)
			}
		}
		aw.generating = gen
		if !gen {
			aw.scan = false // idle: stop scanning until you visit it again
		}
	}
	for h := range w.wins {
		if !seen[h] {
			delete(w.wins, h)
		}
	}

	var st AIState
	for _, aw := range w.wins {
		if aw.generating {
			st = AIState{Name: aw.name, Generating: true}
		}
	}
	var un []UnreadView
	for app, n := range counts {
		if n > 0 {
			un = append(un, UnreadView{app, n})
		}
	}
	sort.Slice(un, func(i, j int) bool { return un[i].App < un[j].App })
	w.mu.Lock()
	changed := st != w.state || fmt.Sprint(un) != fmt.Sprint(w.unread)
	w.state = st
	w.unread = un
	w.mu.Unlock()
	if changed && w.OnState != nil {
		w.OnState()
	}
}
