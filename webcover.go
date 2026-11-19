package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// WebCovers finds album art online when the player gives none (YouTube in a
// browser often doesn't): Deezer first, then iTunes. Both are free and need
// no key. Covers are kept in a folder next to the settings, so each song is
// looked up only once.
type WebCovers struct {
	mu      sync.Mutex
	client  *http.Client
	dir     string
	seen    map[string]time.Time // when the song was first asked about
	result  map[string]string    // key -> "path|n" ("" = nothing found)
	busy    map[string]bool
	n       int
	OnReady func()

	deezer, itunes string // API bases (tests swap them)
}

func NewWebCovers(dir string) *WebCovers {
	return &WebCovers{client: &http.Client{Timeout: 8 * time.Second}, dir: dir,
		seen: map[string]time.Time{}, result: map[string]string{}, busy: map[string]bool{},
		deezer: "https://api.deezer.com", itunes: "https://itunes.apple.com"}
}

// coverWait gives the player a moment to publish its own cover first.
const coverWait = 2500 * time.Millisecond

// Get returns a cover reference for the song, or "" while still looking.
func (w *WebCovers) Get(m *Media) string {
	if w == nil || m == nil || m.Title == "" {
		return ""
	}
	key := m.Key()
	w.mu.Lock()
	defer w.mu.Unlock()
	if r, ok := w.result[key]; ok {
		return r
	}
	file := filepath.Join(w.dir, coverFileName(key))
	if st, err := os.Stat(file); err == nil && st.Size() > 0 {
		w.n++
		w.result[key] = fmt.Sprintf("%s|w%d", file, w.n)
		return w.result[key]
	}
	t, ok := w.seen[key]
	if !ok {
		w.seen[key] = time.Now()
		return ""
	}
	if time.Since(t) < coverWait || w.busy[key] {
		return ""
	}
	w.busy[key] = true
	artist, title := cleanTrack(m.Artist, m.Title)
	go w.fetch(key, artist, title, file)
	return ""
}

func coverFileName(key string) string {
	h := sha1.Sum([]byte(key))
	return hex.EncodeToString(h[:8]) + ".jpg"
}

func (w *WebCovers) fetch(key, artist, title, file string) {
	ref := ""
	for _, find := range []func(string, string) string{w.findDeezer, w.findITunes} {
		u := find(artist, title)
		if u == "" {
			continue
		}
		if w.download(u, file) {
			w.mu.Lock()
			w.n++
			ref = fmt.Sprintf("%s|w%d", file, w.n)
			w.mu.Unlock()
			break
		}
	}
	w.mu.Lock()
	w.result[key] = ref
	delete(w.busy, key)
	cb := w.OnReady
	w.mu.Unlock()
	if ref != "" && cb != nil {
		cb()
	}
}

// coverMatch: the found song must have the same title and artist.
func coverMatch(gotArtist, gotTitle, artist, title string) bool {
	gt, t := normName(gotTitle), normName(title)
	if gt == "" || t == "" {
		return false
	}
	if gt != t && !strings.HasPrefix(gt, t+" ") && !strings.HasPrefix(t, gt+" ") {
		return false
	}
	a, ga := normName(artist), normName(gotArtist)
	return a == "" || strings.Contains(ga, a) || strings.Contains(a, ga)
}

func (w *WebCovers) getJSON(u string, v any) bool {
	resp, err := w.client.Get(u)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(v) == nil
}

func (w *WebCovers) findDeezer(artist, title string) string {
	var r struct {
		Data []struct {
			Title  string `json:"title"`
			Artist struct {
				Name string `json:"name"`
			} `json:"artist"`
			Album struct {
				CoverXL  string `json:"cover_xl"`
				CoverBig string `json:"cover_big"`
			} `json:"album"`
		} `json:"data"`
	}
	q := strings.TrimSpace(artist + " " + title)
	if !w.getJSON(w.deezer+"/search?limit=10&q="+url.QueryEscape(q), &r) {
		return ""
	}
	for _, d := range r.Data {
		if coverMatch(d.Artist.Name, d.Title, artist, title) {
			if d.Album.CoverXL != "" {
				return d.Album.CoverXL
			}
			return d.Album.CoverBig
		}
	}
	return ""
}

func (w *WebCovers) findITunes(artist, title string) string {
	var r struct {
		Results []struct {
			TrackName  string `json:"trackName"`
			ArtistName string `json:"artistName"`
			ArtworkURL string `json:"artworkUrl100"`
		} `json:"results"`
	}
	q := strings.TrimSpace(artist + " " + title)
	if !w.getJSON(w.itunes+"/search?media=music&entity=song&limit=10&term="+url.QueryEscape(q), &r) {
		return ""
	}
	for _, d := range r.Results {
		if d.ArtworkURL != "" && coverMatch(d.ArtistName, d.TrackName, artist, title) {
			return strings.Replace(d.ArtworkURL, "100x100bb", "600x600bb", 1)
		}
	}
	return ""
}

func (w *WebCovers) download(u, file string) bool {
	resp, err := w.client.Get(u)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil || len(b) < 200 {
		return false
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(b)); err != nil {
		return false
	}
	_ = os.MkdirAll(filepath.Dir(file), 0o755)
	tmp := file + ".tmp"
	if os.WriteFile(tmp, b, 0o644) != nil {
		return false
	}
	return os.Rename(tmp, file) == nil
}

// ---------- black bars ----------

// trimBars cuts the black letterbox / pillarbox bars that video thumbnails
// have (YouTube's are 4:3 with black bars around a 16:9 picture, and a
// still-image music video adds more around the square album cover).
func trimBars(img image.Image) image.Rectangle {
	b := img.Bounds()
	dark := func(x0, y0, x1, y1 int) bool {
		n, bright := 0, 0
		stepX, stepY := max(1, (x1-x0)/48), max(1, (y1-y0)/48)
		for y := y0; y < y1; y += stepY {
			for x := x0; x < x1; x += stepX {
				r, g, bb, _ := img.At(x, y).RGBA()
				if (r+g+bb)/3 > 0x1c00 { // ~ 28 of 255
					bright++
				}
				n++
			}
		}
		return n > 0 && bright*50 <= n // at most 2 % non-dark pixels
	}
	r := b
	for r.Dy() > b.Dy()/4 && dark(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+1) {
		r.Min.Y++
	}
	for r.Dy() > b.Dy()/4 && dark(r.Min.X, r.Max.Y-1, r.Max.X, r.Max.Y) {
		r.Max.Y--
	}
	for r.Dx() > b.Dx()/4 && dark(r.Min.X, r.Min.Y, r.Min.X+1, r.Max.Y) {
		r.Min.X++
	}
	for r.Dx() > b.Dx()/4 && dark(r.Max.X-1, r.Min.Y, r.Max.X, r.Max.Y) {
		r.Max.X--
	}
	// a picture that is simply very dark everywhere: keep it as it is
	if r.Dx() <= b.Dx()/4 || r.Dy() <= b.Dy()/4 {
		return b
	}
	return r
}
