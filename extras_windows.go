//go:build windows

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	u16 "unicode/utf16"
	"unsafe"
)

var (
	shell32 = syscall.NewLazyDLL("shell32.dll")

	pAddClipboardFormatListener    = user32.NewProc("AddClipboardFormatListener")
	pRemoveClipboardFormatListener = user32.NewProc("RemoveClipboardFormatListener")
	pOpenClipboard                 = user32.NewProc("OpenClipboard")
	pCloseClipboard                = user32.NewProc("CloseClipboard")
	pGetClipboardData              = user32.NewProc("GetClipboardData")
	pSetClipboardData              = user32.NewProc("SetClipboardData")
	pEmptyClipboard                = user32.NewProc("EmptyClipboard")
	pIsClipboardFormatAvailable    = user32.NewProc("IsClipboardFormatAvailable")
	pRegisterClipboardFormatW      = user32.NewProc("RegisterClipboardFormatW")
	pRegisterWindowMessageW        = user32.NewProc("RegisterWindowMessageW")
	pLoadIconW                     = user32.NewProc("LoadIconW")

	pGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	pGlobalFree   = kernel32.NewProc("GlobalFree")
	pGlobalLock   = kernel32.NewProc("GlobalLock")
	pGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	pGlobalSize   = kernel32.NewProc("GlobalSize")

	pShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	pExtractIconW     = shell32.NewProc("ExtractIconW")
	pShellExecuteW    = shell32.NewProc("ShellExecuteW")
)

const (
	wmClipboardUpdate = 0x031D
	wmMouseWheel      = 0x020A
	wmTray            = wmUser + 2
	cfUnicodeText     = 13
	gmemMoveable      = 0x0002
	nimAdd            = 0
	nimModify         = 1
	nimDelete         = 2
	nifMessage        = 0x01
	nifIcon           = 0x02
	nifTip            = 0x04
	nifInfo           = 0x10
	niifInfo          = 0x01
	niifRespectQuiet  = 0x80
)

type notifyIconData struct {
	Size            uint32
	Wnd             uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	TimeoutVersion  uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GuidItem        [16]byte
	BalloonIcon     uintptr
}

var taskbarCreatedMsg uintptr

func copyUTF16(dst []uint16, s string) {
	u := u16.Encode([]rune(s))
	if len(u) > len(dst)-1 {
		u = u[:len(dst)-1]
	}
	n := copy(dst, u)
	dst[n] = 0
}

// ---------- tray icon + Windows notifications ----------

func (a *app) trayData() *notifyIconData {
	nid := &notifyIconData{Wnd: a.hwnd, ID: 1}
	nid.Size = uint32(unsafe.Sizeof(*nid))
	return nid
}

func (a *app) addTray() {
	taskbarCreatedMsg, _, _ = pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(utf16("TaskbarCreated"))))
	inst, _, _ := pGetModuleHandleW.Call(0)
	exe, _ := os.Executable()
	icon, _, _ := pExtractIconW.Call(inst, uintptr(unsafe.Pointer(utf16(exe))), 0)
	if icon <= 1 {
		icon, _, _ = pLoadIconW.Call(0, 32512) // IDI_APPLICATION
	}
	a.trayIcon = icon
	nid := a.trayData()
	nid.Flags = nifMessage | nifIcon | nifTip
	nid.CallbackMessage = wmTray
	nid.Icon = icon
	copyUTF16(nid.Tip[:], appName)
	pShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(nid)))
}

func (a *app) removeTray() {
	pShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(a.trayData())))
}

func (a *app) notify(title, body string) {
	if !a.cfg.Notify || a.dndOn() {
		return
	}
	a.notifyForce(title, body)
}

func (a *app) notifyForce(title, body string) {
	if !a.cfg.Notify {
		return
	}
	nid := a.trayData()
	nid.Flags = nifInfo
	nid.InfoFlags = niifInfo | niifRespectQuiet
	copyUTF16(nid.InfoTitle[:], title)
	if body == "" {
		body = " "
	}
	copyUTF16(nid.Info[:], body)
	pShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(nid)))
}

// ---------- clipboard ----------

func (a *app) startClipboard() {
	pAddClipboardFormatListener.Call(a.hwnd)
}

func clipboardExcluded() bool {
	for _, name := range []string{"ExcludeClipboardContentFromMonitorProcessing", "Clipboard Viewer Ignore"} {
		f, _, _ := pRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(utf16(name))))
		if f != 0 {
			if r, _, _ := pIsClipboardFormatAvailable.Call(f); r != 0 {
				return true // e.g. a password manager asked to be ignored
			}
		}
	}
	return false
}

func openClipboard(hwnd uintptr) bool {
	for i := 0; i < 6; i++ {
		if r, _, _ := pOpenClipboard.Call(hwnd); r != 0 {
			return true
		}
		time.Sleep(15 * time.Millisecond)
	}
	return false
}

func readClipboardText(hwnd uintptr) (string, bool) {
	if clipboardExcluded() {
		return "", false
	}
	if r, _, _ := pIsClipboardFormatAvailable.Call(cfUnicodeText); r == 0 {
		return "", false
	}
	if !openClipboard(hwnd) {
		return "", false
	}
	defer pCloseClipboard.Call()
	h, _, _ := pGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", false
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		return "", false
	}
	defer pGlobalUnlock.Call(h)
	size, _, _ := pGlobalSize.Call(h)
	n := int(size / 2)
	if n > 200000 {
		n = 200000
	}
	u := unsafe.Slice((*uint16)(unsafe.Pointer(p)), n)
	end := 0
	for end < n && u[end] != 0 {
		end++
	}
	return string(u16.Decode(u[:end])), true
}

func setClipboardText(hwnd uintptr, s string) bool {
	u := u16.Encode([]rune(strings.ReplaceAll(s, "\x00", "")))
	u = append(u, 0)
	if !openClipboard(hwnd) {
		return false
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()
	h, _, _ := pGlobalAlloc.Call(gmemMoveable, uintptr(len(u)*2))
	if h == 0 {
		return false
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		pGlobalFree.Call(h)
		return false
	}
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(p)), len(u)), u)
	pGlobalUnlock.Call(h)
	if r, _, _ := pSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		pGlobalFree.Call(h)
		return false
	}
	return true
}

// ---------- extra clicks ----------

// dndOn: "do not disturb" by hand, or automatically while focusing.
func (a *app) dndOn() bool {
	return a.cfg.DND || (a.cfg.DNDFocus && a.timer.Running && a.timer.Phase == 0)
}

// flashImportant shows a message even in "do not disturb" mode.
func (a *app) flashImportant(text string, c [3]uint8, d time.Duration) {
	a.flash = &Flash{Text: text, Color: c, Until: time.Now().Add(d)}
	a.anim.Pop()
	a.dirty.Store(true)
}

func (a *app) flashMsg(text string, c [3]uint8, d time.Duration) {
	if a.dndOn() {
		return
	}
	a.flash = &Flash{Text: text, Color: c, Until: time.Now().Add(d)}
	a.anim.Pop()
	a.dirty.Store(true)
}

var (
	flashGreen = [3]uint8{0x30, 0xd1, 0x58}
	flashBlue  = [3]uint8{0x0a, 0x84, 0xff}
	flashGray  = [3]uint8{0x98, 0x98, 0x9f}
)

func (a *app) reloadNote() {
	a.note, a.noteMod = readNote()
}

// shellOpen opens a URL, file or program (optional arguments).
func shellOpen(hwnd uintptr, target string, args ...string) {
	var params uintptr
	if len(args) > 0 {
		params = uintptr(unsafe.Pointer(utf16(strings.Join(args, " "))))
	}
	pShellExecuteW.Call(hwnd, uintptr(unsafe.Pointer(utf16("open"))), uintptr(unsafe.Pointer(utf16(target))), params, 0, 1)
}

func (a *app) openNote() {
	p := ensureNote()
	pShellExecuteW.Call(a.hwnd, uintptr(unsafe.Pointer(utf16("open"))), uintptr(unsafe.Pointer(utf16("notepad.exe"))),
		uintptr(unsafe.Pointer(utf16(`"`+p+`"`))), 0, 1)
}

// clickExtra handles tab, timer, clipboard, note and robot buttons.
func (a *app) clickExtra(name string) bool {
	now := time.Now()
	kind, arg, _ := strings.Cut(name, ":")
	n, _ := strconv.Atoi(arg)
	switch kind {
	case "tab":
		a.tab = n
		if n == tabNote {
			a.reloadNote()
		}
		if n == tabLinks {
			a.links, a.linksMod = readLinks()
		}
	case "timer":
		switch arg {
		case "toggle":
			a.timer.Toggle(now)
		case "reset":
			a.timer.Reset()
		case "preset":
			a.timer.NextPreset()
			a.cfg.TimerPreset = a.timer.Preset
			a.cfg.Save()
		}
	case "clip":
		switch arg {
		case "clear":
			a.clips.Items = nil
			a.clipScroll = 0
		default:
			if n >= 0 && n < len(a.clips.Items) {
				if setClipboardText(a.hwnd, a.clips.Items[n]) {
					a.flashMsg(L("Nukopijuota"), flashGreen, 2*time.Second)
				}
			}
		}
	case "note":
		switch arg {
		case "open":
			a.openNote()
		case "paste":
			if len(a.clips.Items) == 0 {
				a.flashMsg(L("Iškarpinė tuščia"), flashGray, 2*time.Second)
			} else if appendNote(a.clips.Items[0], now) == nil {
				a.reloadNote()
				a.flashMsg(L("Įdėta į užrašą"), flashGreen, 2*time.Second)
			}
		}
	case "lyric":
		if m := a.media.Current(); m != nil {
			if l := a.lyrics.Get(m); l != nil && n >= 0 && n < len(l.Lines) {
				if m.Estimated {
					// no real song time: "this is the line I hear now"
					a.media.SyncTo(l.Lines[n].T + 0.05 - lyricLead - a.lyrOffset[m.Key()])
					a.flashMsg(L("Žodžiai suderinti"), flashGreen, 1200*time.Millisecond)
				} else {
					a.media.Seek(l.Lines[n].T + 0.05)
				}
			}
		}
	case "lyr":
		m := a.media.Current()
		switch arg {
		case "later", "earlier":
			if m != nil {
				d := 0.5
				if arg == "earlier" {
					d = -0.5
				}
				a.lyrOffset[m.Key()] += d
			}
		case "trans":
			a.cfg.Translate = !a.cfg.Translate
			a.cfg.Save()
		case "fav":
			if m != nil {
				if a.favs.Toggle(m.Artist, m.Title) {
					a.flashImportant(L("Pridėta prie mėgstamų"), [3]uint8{0xff, 0x37, 0x5f}, 1500*time.Millisecond)
				} else {
					a.flashImportant(L("Pašalinta iš mėgstamų"), flashGray, 1500*time.Millisecond)
				}
			}
		case "sleep":
			steps := []int{0, 15, 30, 60}
			cur := 0
			if !a.sleepAt.IsZero() {
				left := time.Until(a.sleepAt).Minutes()
				for i, s := range steps {
					if float64(s) >= left-1 {
						cur = i
						break
					}
				}
			}
			next := steps[(cur+1)%len(steps)]
			if next == 0 {
				a.sleepAt = time.Time{}
				a.flashImportant(L("Miego laikmatis išjungtas"), flashGray, 1500*time.Millisecond)
			} else {
				a.sleepAt = now.Add(time.Duration(next) * time.Minute)
				a.flashImportant(fmt.Sprintf(L("Muzika sustos po %d min"), next), [3]uint8{0x5e, 0x5c, 0xe6}, 1500*time.Millisecond)
			}
		case "karaoke":
			shellOpen(a.hwnd, karaokeURL())
		}
	case "settings":
		shellOpen(a.hwnd, settingsURL(""))
	case "cmdsel":
		if a.cmd != nil && n >= 0 && n < len(a.cmd.Results) {
			a.runCmd(a.cmd.Results[n].Action)
		}
	case "game":
		if a.game != nil {
			a.game.Jump()
		}
	case "card":
		a.card = nil
	case "trans":
		switch arg {
		case "copy":
			setClipboardText(a.hwnd, a.lastTrans)
			a.card = nil
			a.flashMsg(L("Vertimas nukopijuotas"), flashGreen, 1500*time.Millisecond)
		case "clip":
			if len(a.clips.Items) > 0 {
				a.translateToCard(a.clips.Items[0])
			}
		}
	case "pick":
		c := a.pickedColor
		if arg == "rgb" {
			setClipboardText(a.hwnd, fmt.Sprintf("rgb(%d, %d, %d)", c[0], c[1], c[2]))
		} else {
			setClipboardText(a.hwnd, fmt.Sprintf("#%02X%02X%02X", c[0], c[1], c[2]))
		}
		a.card = nil
		a.flashMsg(L("Nukopijuota"), flashGreen, 1200*time.Millisecond)
	case "plan":
		if arg == "todo" {
			a.openCmd(L("užduotis") + " ")
		} else {
			a.openCmd(L("priminimas") + " 15 min ")
		}
	case "todo":
		parts := strings.Split(arg, ":")
		id := 0
		if len(parts) > 1 {
			id, _ = strconv.Atoi(parts[1])
		}
		switch parts[0] {
		case "toggle":
			a.todos.Toggle(id)
		case "del":
			a.todos.Delete(id)
		case "clear":
			a.todos.ClearDone()
		}
	case "tool":
		switch arg {
		case "color":
			a.startPicker()
		case "game":
			a.startGame()
		}
	case "rem":
		parts := strings.Split(arg, ":")
		switch parts[0] {
		case "add":
			mins, _ := strconv.Atoi(parts[len(parts)-1])
			a.rem.Add(L("Priminimas"), now.Add(time.Duration(mins)*time.Minute))
			a.flashMsg(fmt.Sprintf(L("Priminsiu po %d min"), mins), [3]uint8{0xff, 0x9f, 0x0a}, 1500*time.Millisecond)
		case "del":
			id, _ := strconv.Atoi(parts[len(parts)-1])
			a.rem.Delete(id)
		case "text":
			shellOpen(a.hwnd, settingsURL("priminimai"))
		}
	case "shelf":
		parts := strings.Split(arg, ":")
		i := -1
		if len(parts) > 1 {
			i, _ = strconv.Atoi(parts[1])
		}
		switch parts[0] {
		case "open":
			if i >= 0 && i < len(a.shelf.Paths) {
				shellOpen(a.hwnd, a.shelf.Paths[i])
			}
		case "del":
			a.shelf.Remove(i)
		case "copy":
			if setClipboardFiles(a.hwnd, a.shelf.Paths) {
				a.flashMsg(L("Nukopijuota – aplanke spausk Ctrl+V"), flashGreen, 2500*time.Millisecond)
			}
		case "clear":
			a.shelf.Clear()
		}
	case "link":
		if n >= 0 && n < len(a.links) {
			shellOpen(a.hwnd, a.links[n].Target)
			a.flashMsg(L("Atidaroma: ")+a.links[n].Name, flashBlue, 1500*time.Millisecond)
		}
	case "links":
		shellOpen(a.hwnd, settingsURL("nuorodos"))
	case "weather":
		shellOpen(a.hwnd, settingsURL("orai"))
	case "shot":
		switch arg {
		case "take":
			a.takeScreenshot()
		case "region":
			a.snipRegion()
		case "open":
			if a.shot != nil && a.shot.Busy == "" {
				shellOpen(a.hwnd, a.shot.Path)
			}
		case "ocr":
			if a.shot != nil {
				if a.shot.Busy == "Saugoma…" {
					break
				}
				if a.media.OCR(a.shot.Path) {
					a.shot.Busy = L("Atpažįstamas tekstas…")
					a.shot.Until = now.Add(30 * time.Second)
				} else {
					a.shot.Busy = L("Teksto atpažinimas dar nepasiruošęs")
				}
			}
		}
	case "vol":
		if lvl, muted, ok := a.vol.toggleMute(); ok {
			a.showVolume(lvl, muted)
		}
	case "color":
		a.cfg.RobotColor = n
		a.cfg.Save()
	case "face":
		a.cfg.Face = n
		a.cfg.Save()
	default:
		return false
	}
	a.dirty.Store(true)
	return true
}

func (a *app) timerView(now time.Time) TimerView {
	t := a.timer
	return TimerView{
		Active:   t.Active(),
		Running:  t.Running,
		Text:     fmtDur(t.Remaining(now)),
		Preset:   t.PresetLabel(),
		Phase:    t.Phase,
		Rounds:   t.Rounds,
		Progress: t.Progress(now),
	}
}
