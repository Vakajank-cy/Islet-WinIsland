//go:build windows

package main

import (
	"fmt"
	"unsafe"
)

var (
	pGetSystemPowerStatus         = kernel32.NewProc("GetSystemPowerStatus")
	pGlobalMemoryStatusEx         = kernel32.NewProc("GlobalMemoryStatusEx")
	pGetSystemTimes               = kernel32.NewProc("GetSystemTimes")
	pSHQueryUserNotificationState = shell32.NewProc("SHQueryUserNotificationState")
	pRegisterHotKey               = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey             = user32.NewProc("UnregisterHotKey")
)

const (
	wmHotkey     = 0x0312
	wmPowerBroad = 0x0218
	modAlt       = 0x0001
	modControl   = 0x0002
	modNoRepeat  = 0x4000
	swHide       = 0
)

type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

type fileTime struct{ Low, High uint32 }

func (f fileTime) u64() uint64 { return uint64(f.High)<<32 | uint64(f.Low) }

type sysSampler struct {
	lastIdle, lastTotal uint64
	info                SysInfo
}

func (s *sysSampler) sample() SysInfo {
	info := SysInfo{Has: true, Battery: -1}

	var ps systemPowerStatus
	if r, _, _ := pGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&ps))); r != 0 {
		if ps.BatteryFlag != 128 && ps.BatteryFlag != 255 && ps.BatteryLifePercent <= 100 {
			info.Battery = int(ps.BatteryLifePercent)
			info.Charging = ps.ACLineStatus == 1
		}
	}

	var ms memoryStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))
	if r, _, _ := pGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms))); r != 0 && ms.TotalPhys > 0 {
		const gb = 1 << 30
		info.RAMTotal = float64(ms.TotalPhys) / gb
		info.RAMUsed = float64(ms.TotalPhys-ms.AvailPhys) / gb
		info.RAM = info.RAMUsed / info.RAMTotal
	}

	var idle, kernel, user fileTime
	if r, _, _ := pGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); r != 0 {
		i, t := idle.u64(), kernel.u64()+user.u64() // kernel time includes idle
		if s.lastTotal != 0 && t > s.lastTotal {
			info.CPU = clamp01(1 - float64(i-s.lastIdle)/float64(t-s.lastTotal))
		} else {
			info.CPU = s.info.CPU
		}
		s.lastIdle, s.lastTotal = i, t
	}
	s.info = info
	return info
}

// fullscreenBusy reports whether a full-screen app, game or presentation runs.
func fullscreenBusy() bool {
	var state int32
	if pSHQueryUserNotificationState.Find() != nil {
		return false
	}
	if r, _, _ := pSHQueryUserNotificationState.Call(uintptr(unsafe.Pointer(&state))); r != 0 {
		return false
	}
	// 2 = busy (full-screen app), 3 = full-screen Direct3D game, 4 = presentation
	return state == 2 || state == 3 || state == 4
}

// registerHotkeys takes, for every action, the first key combination that
// no other program holds: the user's own choice first, then the defaults.
// It returns a message when the user's own choice was taken.
func (a *app) registerHotkeys() string {
	a.keys, a.keysBusy = map[string]string{}, map[string]string{}
	note := ""
	for _, act := range hotkeyActions {
		pUnregisterHotKey.Call(a.hwnd, uintptr(act.ID))
	}
	for _, act := range hotkeyActions {
		chosen := a.cfg.Keys[act.Name]
		for i, h := range hotkeyCandidates(act, chosen) {
			r, _, _ := pRegisterHotKey.Call(a.hwnd, uintptr(act.ID), uintptr(h.Mods)|modNoRepeat, uintptr(h.VK))
			if r != 0 {
				a.keys[act.Name] = h.String()
				break
			}
			if i == 0 {
				a.keysBusy[act.Name] = h.String()
			}
		}
		if b := a.keysBusy[act.Name]; b != "" && chosen != "" && note == "" {
			note = fmt.Sprintf(L("%s užimtas kitos programos – naudojamas %s"), b, a.keys[act.Name])
		}
	}
	return note
}

// focusWindow brings another app's window to the front.
func focusWindow(h uintptr) {
	if mini, _, _ := pIsIconic.Call(h); mini != 0 {
		pShowWindow.Call(h, 9) // SW_RESTORE
	}
	pSetForegroundWindow.Call(h)
}

// setVisible shows or hides the island window.
func (a *app) setVisible(on bool) {
	if on == a.visible {
		return
	}
	a.visible = on
	if on {
		pShowWindow.Call(a.hwnd, swShowNoActive)
		a.dirty.Store(true)
	} else {
		pShowWindow.Call(a.hwnd, swHide)
		a.over, a.hover = false, false
	}
}

// powerEvents shows a flash when the charger is (un)plugged or battery is low.
func (a *app) powerEvents(prev, cur SysInfo) {
	if !prev.Has || cur.Battery < 0 {
		return
	}
	if cur.Charging != prev.Charging {
		if cur.Charging {
			a.flashMsg(fmtPct(L("Kraunasi · "), cur.Battery), flashGreen, 3e9)
		} else {
			a.flashMsg(fmtPct(L("Kroviklis atjungtas · "), cur.Battery), flashGray, 3e9)
		}
	}
	if !cur.Charging {
		for _, lvl := range []int{20, 10, 5} {
			if prev.Battery > lvl && cur.Battery <= lvl {
				msg := fmtPct(L("Baterija senka · "), cur.Battery)
				a.flashMsg(msg, [3]uint8{0xff, 0x45, 0x3a}, 8e9)
				a.notify(L("Baterija senka"), fmtPct(L("Liko "), cur.Battery)+L(" – prijunk kroviklį"))
				if a.cfg.Sound {
					pMessageBeep.Call(0x30)
				}
				break
			}
		}
	}
}

func fmtPct(prefix string, p int) string {
	return prefix + itoa(p) + "%"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
