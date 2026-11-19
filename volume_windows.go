//go:build windows

package main

import (
	"math"
	"syscall"
	"time"
	u16 "unicode/utf16"
	"unsafe"
)

var (
	ole32         = syscall.NewLazyDLL("ole32.dll")
	oleaut32      = syscall.NewLazyDLL("oleaut32.dll")
	pCoInitEx     = ole32.NewProc("CoInitializeEx")
	pCoCreateInst = ole32.NewProc("CoCreateInstance")
	pSysAllocStr  = oleaut32.NewProc("SysAllocString")
	pSysFreeStr   = oleaut32.NewProc("SysFreeString")
)

type guid struct {
	D1 uint32
	D2 uint16
	D3 uint16
	D4 [8]byte
}

var (
	clsidMMDeviceEnumerator = guid{0xBCDE0395, 0xE52F, 0x467C, [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidIMMDeviceEnumerator  = guid{0xA95664D2, 0x9614, 0x4F35, [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidIAudioEndpointVolume = guid{0x5CDF2C82, 0x841E, 0x4546, [8]byte{0x97, 0x22, 0x0C, 0xF7, 0x40, 0x78, 0x22, 0x9A}}
)

// comCall calls method #idx of a COM object's vtable.
func comCall(obj uintptr, idx int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

func utf16Decode(u []uint16) []rune { return u16.Decode(u) }

func comRelease(obj uintptr) {
	if obj != 0 {
		comCall(obj, 2)
	}
}

// volMon keeps the default speakers' IAudioEndpointVolume and notices when
// the default device changes (e.g. Bluetooth headphones connected).
type volMon struct {
	meter     uintptr // IAudioMeterInformation (for the visualizer)
	ep        uintptr
	id        string
	name      string
	lastCheck time.Time
}

func readCoString(p uintptr) string {
	if p == 0 {
		return ""
	}
	n := 0
	for *(*uint16)(unsafe.Pointer(p + uintptr(n)*2)) != 0 && n < 1024 {
		n++
	}
	return string(utf16Decode(unsafe.Slice((*uint16)(unsafe.Pointer(p)), n)))
}

func defaultDevice() uintptr {
	var enum uintptr
	if hr, _, _ := pCoCreateInst.Call(uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, 0x17, // CLSCTX_ALL
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)), uintptr(unsafe.Pointer(&enum))); hr != 0 || enum == 0 {
		return 0
	}
	defer comRelease(enum)
	var dev uintptr
	// GetDefaultAudioEndpoint(eRender=0, eMultimedia=1)
	if comCall(enum, 4, 0, 1, uintptr(unsafe.Pointer(&dev))) != 0 {
		return 0
	}
	return dev
}

func deviceID(dev uintptr) string {
	var p uintptr
	if comCall(dev, 5, uintptr(unsafe.Pointer(&p))) != 0 { // GetId
		return ""
	}
	defer pCoTaskMemFree.Call(p)
	return readCoString(p)
}

func deviceFriendlyName(dev uintptr) string {
	var store uintptr
	if comCall(dev, 4, 0, uintptr(unsafe.Pointer(&store))) != 0 || store == 0 { // OpenPropertyStore(STGM_READ)
		return ""
	}
	defer comRelease(store)
	var pv [24]byte
	if comCall(store, 5, uintptr(unsafe.Pointer(&keyFriendly)), uintptr(unsafe.Pointer(&pv))) != 0 {
		return ""
	}
	defer pPropVariantClear.Call(uintptr(unsafe.Pointer(&pv)))
	if *(*uint16)(unsafe.Pointer(&pv[0])) != 31 { // VT_LPWSTR
		return ""
	}
	return readCoString(*(*uintptr)(unsafe.Pointer(&pv[8])))
}

// check re-reads the default device every 2 s. Returns its name if it changed.
func (v *volMon) check(force bool) (string, bool) {
	if !force && v.ep != 0 && time.Since(v.lastCheck) < 2*time.Second {
		return "", false
	}
	v.lastCheck = time.Now()
	dev := defaultDevice()
	if dev == 0 {
		return "", false
	}
	defer comRelease(dev)
	id := deviceID(dev)
	if id == v.id && v.ep != 0 {
		return "", false
	}
	var ep uintptr
	// IMMDevice::Activate(iid, CLSCTX_ALL, NULL, &out)
	if comCall(dev, 3, uintptr(unsafe.Pointer(&iidIAudioEndpointVolume)), 0x17, 0, uintptr(unsafe.Pointer(&ep))) != 0 {
		return "", false
	}
	comRelease(v.ep)
	comRelease(v.meter)
	v.meter = 0
	comCall(dev, 3, uintptr(unsafe.Pointer(&iidIAudioMeterInformation)), 0x17, 0, uintptr(unsafe.Pointer(&v.meter)))
	first := v.id == ""
	v.ep, v.id, v.name = ep, id, deviceFriendlyName(dev)
	return v.name, !first
}

func (v *volMon) get() (float64, bool, bool) {
	v.check(false)
	if v.ep == 0 {
		return 0, false, false
	}
	var level float32
	var mute int32
	if comCall(v.ep, 9, uintptr(unsafe.Pointer(&level))) != 0 { // GetMasterVolumeLevelScalar
		v.ep, v.id = 0, ""
		return 0, false, false
	}
	comCall(v.ep, 15, uintptr(unsafe.Pointer(&mute))) // GetMute
	return float64(level), mute != 0, true
}

// change adds delta (e.g. +0.02) and returns the new state.
func (v *volMon) change(delta float64) (float64, bool, bool) {
	level, muted, ok := v.get()
	if !ok {
		return 0, false, false
	}
	nl := math.Max(0, math.Min(1, math.Round((level+delta)*50)/50))
	// float arguments travel in XMM registers; Go's syscall loads them too
	comCall(v.ep, 7, uintptr(math.Float32bits(float32(nl))), 0) // SetMasterVolumeLevelScalar
	if muted && delta > 0 {
		comCall(v.ep, 14, 0, 0) // SetMute(false)
		muted = false
	}
	return nl, muted, true
}

func (v *volMon) toggleMute() (float64, bool, bool) {
	level, muted, ok := v.get()
	if !ok {
		return 0, false, false
	}
	nm := uintptr(1)
	if muted {
		nm = 0
	}
	comCall(v.ep, 14, nm, 0)
	return level, nm == 1, true
}
