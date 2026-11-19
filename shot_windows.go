//go:build windows

package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	xdraw "golang.org/x/image/draw"
)

var (
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pGetDIBits              = gdi32.NewProc("GetDIBits")
)

// captureScreen grabs the primary monitor as RGBA (physical pixels).
func captureScreen() *image.RGBA {
	w, _, _ := pGetSystemMetrics.Call(0)
	h, _, _ := pGetSystemMetrics.Call(1)
	if w == 0 || h == 0 {
		return nil
	}
	screen, _, _ := pGetDC.Call(0)
	defer pReleaseDC.Call(0, screen)
	mem, _, _ := pCreateCompatibleDC.Call(screen)
	defer pDeleteDC.Call(mem)
	bmp, _, _ := pCreateCompatibleBitmap.Call(screen, w, h)
	defer pDeleteObject.Call(bmp)
	old, _, _ := pSelectObject.Call(mem, bmp)
	pBitBlt.Call(mem, 0, 0, w, h, screen, 0, 0, 0x00CC0020|0x40000000) // SRCCOPY | CAPTUREBLT
	pSelectObject.Call(mem, old)

	bi := bitmapInfoHeader{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	bi.Size = uint32(unsafe.Sizeof(bi))
	buf := make([]byte, int(w)*int(h)*4+64) // header slack for the color table GetDIBits may write
	pGetDIBits.Call(mem, bmp, 0, h, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), 0)
	img := image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
	px := img.Pix
	for i := 0; i < int(w)*int(h); i++ {
		o := i * 4
		px[o], px[o+1], px[o+2], px[o+3] = buf[o+2], buf[o+1], buf[o], 255
	}
	return img
}

func screenshotDir() string {
	home, _ := os.UserHomeDir()
	for _, d := range []string{
		filepath.Join(home, "Pictures", "Screenshots"),
		filepath.Join(home, "OneDrive", "Pictures", "Screenshots"),
		filepath.Join(home, "Pictures"),
	} {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	d := filepath.Join(home, "Pictures", "Screenshots")
	os.MkdirAll(d, 0o755)
	return d
}

func savePNG(img image.Image, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	return enc.Encode(f, img)
}

// setClipboardImage puts an image on the clipboard as CF_DIB.
func setClipboardImage(hwnd uintptr, img *image.RGBA) bool {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	hdr := bitmapInfoHeader{Width: int32(w), Height: int32(h), Planes: 1, BitCount: 32}
	hdr.Size = uint32(unsafe.Sizeof(hdr))
	hdr.SizeImage = uint32(w * h * 4)
	total := int(hdr.Size) + w*h*4
	if !openClipboard(hwnd) {
		return false
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()
	hmem, _, _ := pGlobalAlloc.Call(gmemMoveable, uintptr(total))
	if hmem == 0 {
		return false
	}
	p, _, _ := pGlobalLock.Call(hmem)
	if p == 0 {
		pGlobalFree.Call(hmem)
		return false
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(p)), total)
	copy(dst, unsafe.Slice((*byte)(unsafe.Pointer(&hdr)), hdr.Size))
	o := int(hdr.Size)
	for y := h - 1; y >= 0; y-- { // DIB rows are bottom-up
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for x := 0; x < w; x++ {
			dst[o], dst[o+1], dst[o+2], dst[o+3] = row[x*4+2], row[x*4+1], row[x*4], 255
			o += 4
		}
	}
	pGlobalUnlock.Call(hmem)
	if r, _, _ := pSetClipboardData.Call(8, hmem); r == 0 { // CF_DIB
		pGlobalFree.Call(hmem)
		return false
	}
	return true
}

func makeThumb(img *image.RGBA, scale float64) image.Image {
	w, h := int(112*scale), int(63*scale)
	t := image.NewRGBA(image.Rect(0, 0, w, h))
	// keep aspect ratio: fit inside, centered
	b := img.Bounds()
	r := float64(b.Dx()) / float64(b.Dy())
	tw, th := w, int(float64(w)/r)
	if th > h {
		th, tw = h, int(float64(h)*r)
	}
	dst := image.Rect((w-tw)/2, (h-th)/2, (w-tw)/2+tw, (h-th)/2+th)
	xdraw.ApproxBiLinear.Scale(t, dst, img, b, xdraw.Src, nil)
	return LoadCoverFromImage(t, 8*scale)
}

// takeScreenshot hides the island, captures the screen, saves a PNG,
// copies the image to the clipboard and shows a preview card.
func (a *app) takeScreenshot() {
	wasVisible := a.visible
	if wasVisible {
		pShowWindow.Call(a.hwnd, swHide)
		time.Sleep(160 * time.Millisecond) // let the desktop repaint without the island
	}
	img := captureScreen()
	if wasVisible {
		pShowWindow.Call(a.hwnd, swShowNoActive)
	}
	if img == nil {
		a.flashMsg(L("Nepavyko padaryti nuotraukos"), flashGray, 2*time.Second)
		return
	}
	a.showShot(img, true)
}

// showShot saves the image, optionally copies it, and shows the preview card.
func (a *app) showShot(img *image.RGBA, copyIt bool) {
	if copyIt {
		setClipboardImage(a.hwnd, img)
	}
	path := filepath.Join(screenshotDir(), fmt.Sprintf("Ekrano nuotrauka %s.png", time.Now().Format("2006-01-02 15-04-05")))
	a.shot = &ShotView{Thumb: makeThumb(img, a.r.S), Path: path, Busy: L("Saugoma…"), Until: time.Now().Add(7 * time.Second)}
	a.anim.Pop()
	if a.cfg.Sound {
		pMessageBeep.Call(0)
	}
	a.dirty.Store(true)
	shot := a.shot
	go func() {
		if err := savePNG(img, path); err != nil {
			shot.Busy = L("Nepavyko išsaugoti")
		} else {
			shot.Busy = ""
		}
		onChange()
	}()
}
