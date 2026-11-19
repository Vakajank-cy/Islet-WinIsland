package main

import (
	"bytes"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"strings"

	"github.com/fogleman/gg"
	xdraw "golang.org/x/image/draw"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

// LoadCover reads album art ("path|n" from the helper), crops it square,
// scales it to size px and rounds the corners.
func LoadCover(ref string, size int, radius float64) image.Image {
	path, _, _ := strings.Cut(ref, "|")
	if path == "" || size <= 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	sq := squareCover(src, size)
	if sq == nil {
		return nil
	}
	return LoadCoverFromImage(sq, radius)
}

// squareCover drops black video bars, crops the middle square and scales it.
func squareCover(src image.Image, size int) *image.RGBA {
	b := trimBars(src)
	side := min(b.Dx(), b.Dy())
	if side <= 0 || size <= 0 {
		return nil
	}
	// (image.Rect would reorder the corners, so build the square by hand)
	min0 := image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2)
	sq := image.Rectangle{Min: min0, Max: min0.Add(image.Pt(side, side))}
	scaled := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), src, sq, xdraw.Src, nil)
	return scaled
}

// CoverJPEG returns the cleaned, square cover as a JPEG (for the karaoke page).
func CoverJPEG(ref string, size int) []byte {
	path, _, _ := strings.Cut(ref, "|")
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	sq := squareCover(src, size)
	if sq == nil {
		return nil
	}
	var buf bytes.Buffer
	if jpeg.Encode(&buf, sq, &jpeg.Options{Quality: 88}) != nil {
		return nil
	}
	return buf.Bytes()
}

// LoadCoverFromImage returns img with rounded corners.
func LoadCoverFromImage(img image.Image, radius float64) image.Image {
	b := img.Bounds()
	dc := gg.NewContext(b.Dx(), b.Dy())
	dc.DrawRoundedRectangle(0, 0, float64(b.Dx()), float64(b.Dy()), radius)
	dc.Clip()
	dc.DrawImage(img, 0, 0)
	return dc.Image()
}
