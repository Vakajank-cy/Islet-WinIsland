package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Media is what Windows says is playing right now.
type Media struct {
	App     string  `json:"app"`
	Title   string  `json:"title"`
	Artist  string  `json:"artist"`
	Album   string  `json:"album"`
	Status  int     `json:"status"` // 4 = playing, 5 = paused
	Pos     float64 `json:"pos"`    // seconds at Updated
	End     float64 `json:"end"`    // track length in seconds
	Updated int64   `json:"updated"`
	Seen    time.Time
	// Estimated: the app (e.g. YouTube in a browser) gives no song time,
	// so the island counts it itself from when the song started.
	Estimated bool `json:"-"`
}

// TimelineOK reports whether Windows gave a usable song position.
func (m *Media) TimelineOK() bool { return m.End > 0 && m.Updated > 0 }

// clockTracker counts playback time itself for apps without a timeline.
type clockTracker struct {
	key     string
	base    float64
	since   time.Time
	playing bool
}

// Update follows the song and play/pause state.
func (c *clockTracker) Update(m *Media, now time.Time) {
	playing := m.Status == 4
	if m.Key() != c.key {
		*c = clockTracker{key: m.Key(), since: now, playing: playing}
		return
	}
	if playing != c.playing {
		if c.playing {
			c.base += now.Sub(c.since).Seconds()
		}
		c.since = now
		c.playing = playing
	}
}

func (c *clockTracker) Pos(now time.Time) float64 {
	if c.playing {
		return c.base + now.Sub(c.since).Seconds()
	}
	return c.base
}

// Set says "the song is at sec now" (user clicked the line being sung).
func (c *clockTracker) Set(sec float64, now time.Time) {
	c.base, c.since = sec, now
}

func (m *Media) Playing() bool { return m != nil && m.Status == 4 && m.Title != "" }

// Position estimates the current playback position.
func (m *Media) Position(now time.Time) float64 {
	if m == nil {
		return 0
	}
	p := m.Pos
	if m.Status == 4 && m.Updated > 0 {
		p += now.Sub(time.UnixMilli(m.Updated)).Seconds()
	}
	if m.End > 0 && p > m.End {
		p = m.End
	}
	return math.Max(0, p)
}

func (m *Media) Key() string { return strings.ToLower(m.Artist + "\x00" + m.Title) }

// AppName turns an app id like "Spotify.exe" into a short name.
func (m *Media) AppName() string {
	a := strings.ToLower(m.App)
	switch {
	case strings.Contains(a, "spotify"):
		return "Spotify"
	case strings.Contains(a, "chrome"):
		return "Chrome"
	case strings.Contains(a, "msedge"):
		return "Edge"
	case strings.Contains(a, "firefox"):
		return "Firefox"
	case strings.Contains(a, "zune"), strings.Contains(a, "media"):
		return "Medija"
	case strings.Contains(a, "opera"):
		return "Opera"
	case strings.Contains(a, "applemusic"), strings.Contains(a, "itunes"):
		return "Apple Music"
	}
	return ""
}

// ---------- LRC lyrics ----------

type LyricLine struct {
	T    float64
	Text string
}

type Lyrics struct {
	State     int // 0 none, 1 loading, 2 synced, 3 plain only, 4 not found, 5 instrumental, 6 error
	Lines     []LyricLine
	Plain     string
	Source    string
	Match     string // "Artist – Title" of the lyrics that were found
	Trans     []string
	TransLang string
}

const (
	lyrNone = iota
	lyrLoading
	lyrSynced
	lyrPlain
	lyrNotFound
	lyrInstrumental
	lyrError
)

var lrcTag = regexp.MustCompile(`\[(\d+):(\d+(?:[.:]\d+)?)\]`)

// ParseLRC parses "[mm:ss.xx] text" lines (several tags per line allowed).
func ParseLRC(s string) []LyricLine {
	var out []LyricLine
	for _, raw := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		tags := lrcTag.FindAllStringSubmatchIndex(raw, -1)
		if len(tags) == 0 {
			continue
		}
		text := strings.TrimSpace(raw[tags[len(tags)-1][1]:])
		for _, t := range tags {
			min, _ := strconv.Atoi(raw[t[2]:t[3]])
			sec, _ := strconv.ParseFloat(strings.Replace(raw[t[4]:t[5]], ":", ".", 1), 64)
			out = append(out, LyricLine{T: float64(min)*60 + sec, Text: text})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out
}

// LineAt returns the index of the line being sung at pos (-1 before the first).
func (l *Lyrics) LineAt(pos float64) int {
	i := sort.Search(len(l.Lines), func(i int) bool { return l.Lines[i].T > pos }) - 1
	return i
}

var (
	reBrackets = regexp.MustCompile(`(?i)\s*[\(\[][^\)\]]*\b(official|video|audio|lyrics?|visualizer|remaster(ed)?|hd|4k|mv|live|prod)\b[^\)\]]*[\)\]]`)
	reFeat     = regexp.MustCompile(`(?i)\s+(feat\.?|ft\.?|featuring)\s.*$`)
	reTopic    = regexp.MustCompile(`(?i)\s*-\s*topic$|vevo$`)
)

// cleanTrack tidies YouTube-style titles: "Artist - Song (Official Video)".
func cleanTrack(artist, title string) (string, string) {
	t := reBrackets.ReplaceAllString(title, "")
	t = strings.TrimSpace(strings.Trim(t, `"'“”`))
	a := strings.TrimSpace(reTopic.ReplaceAllString(artist, ""))
	if i := strings.Index(t, " - "); i > 0 {
		left, right := strings.TrimSpace(t[:i]), strings.TrimSpace(t[i+3:])
		if a == "" || strings.EqualFold(left, a) || !strings.Contains(strings.ToLower(right), strings.ToLower(a)) {
			a, t = left, right
		}
	}
	t = strings.TrimSpace(reFeat.ReplaceAllString(t, ""))
	if i := strings.IndexAny(a, ",&"); i > 0 {
		a = strings.TrimSpace(a[:i])
	}
	return a, t
}

// ---------- fetching (LRCLIB, free and open) ----------

type LyricsStore struct {
	mu         sync.Mutex
	cache      map[string]*Lyrics
	transTried map[string]bool
	client     *http.Client
	base       string
	OnReady    func()
}

func NewLyricsStore() *LyricsStore {
	return &LyricsStore{cache: map[string]*Lyrics{}, transTried: map[string]bool{}, client: &http.Client{Timeout: 10 * time.Second}, base: "https://lrclib.net"}
}

// Get returns lyrics for the track, starting a background fetch if needed.
func (s *LyricsStore) Get(m *Media) *Lyrics {
	if m == nil || m.Title == "" {
		return &Lyrics{}
	}
	key := m.Key()
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.cache[key]; ok {
		return l
	}
	l := &Lyrics{State: lyrLoading}
	s.cache[key] = l
	mm := *m
	go s.fetch(key, &mm)
	return l
}

type lrclibRecord struct {
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	PlainLyrics  *string `json:"plainLyrics"`
	SyncedLyrics *string `json:"syncedLyrics"`
}

func (s *LyricsStore) request(path string, q url.Values, out any) (int, error) {
	req, _ := http.NewRequest("GET", s.base+path+"?"+q.Encode(), nil)
	req.Header.Set("User-Agent", "Islet/1.0 (Windows desktop app)")
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return resp.StatusCode, nil
	}
	return 200, json.NewDecoder(resp.Body).Decode(out)
}

func (s *LyricsStore) fetch(key string, m *Media) {
	artist, title := cleanTrack(m.Artist, m.Title)
	res := &Lyrics{State: lyrNotFound, Source: "LRCLIB"}
	defer func() {
		s.mu.Lock()
		s.cache[key] = res
		s.mu.Unlock()
		if res.State == lyrError { // network problem: try again later
			time.AfterFunc(30*time.Second, func() {
				s.mu.Lock()
				if s.cache[key] == res {
					delete(s.cache, key)
				}
				s.mu.Unlock()
			})
		}
		if s.OnReady != nil {
			s.OnReady()
		}
	}()

	var rec lrclibRecord
	found := false
	if artist != "" {
		q := url.Values{"track_name": {title}, "artist_name": {artist}}
		if m.Album != "" {
			q.Set("album_name", m.Album)
		}
		if m.End >= 1 && m.End <= 3600 {
			q.Set("duration", fmt.Sprint(int(math.Round(m.End))))
		}
		code, err := s.request("/api/get", q, &rec)
		if err != nil {
			res.State = lyrError
			return
		}
		found = code == 200
	}
	if !found {
		var list []lrclibRecord
		q := url.Values{"track_name": {title}}
		if artist != "" {
			q.Set("artist_name", artist)
		}
		if _, err := s.request("/api/search", q, &list); err != nil {
			res.State = lyrError
			return
		}
		best, bestScore := -1, 0
		for i, r := range list {
			if sc := matchScore(r, artist, title, m.End); sc > bestScore {
				best, bestScore = i, sc
			}
		}
		if best < 0 {
			return
		}
		rec, found = list[best], true
	}
	res.Match = rec.ArtistName + " – " + rec.TrackName
	switch {
	case rec.Instrumental:
		res.State = lyrInstrumental
	case rec.SyncedLyrics != nil && strings.TrimSpace(*rec.SyncedLyrics) != "":
		res.Lines = ParseLRC(*rec.SyncedLyrics)
		res.State = lyrSynced
		if len(res.Lines) == 0 {
			res.State = lyrNotFound
		}
	case rec.PlainLyrics != nil && strings.TrimSpace(*rec.PlainLyrics) != "":
		res.Plain = *rec.PlainLyrics
		res.State = lyrPlain
	}
}

// normName lowercases and drops punctuation for loose title comparison.
func normName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r == ' ' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// matchScore rates a search result; only results scoring > 0 are used.
func matchScore(r lrclibRecord, artist, title string, dur float64) int {
	sc := 0
	rt, t := normName(r.TrackName), normName(title)
	switch {
	case rt == t:
		sc += 4
	case strings.Contains(rt, t) || strings.Contains(t, rt):
		sc++
	default:
		sc -= 3
	}
	if a := normName(artist); a != "" {
		ra := normName(r.ArtistName)
		if strings.Contains(ra, a) || strings.Contains(a, ra) {
			sc += 3
		} else {
			sc -= 3
		}
	}
	switch {
	case r.SyncedLyrics != nil:
		sc += 2
	case r.PlainLyrics != nil:
		sc++
	default:
		sc -= 10
	}
	if dur > 0 {
		if d := math.Abs(r.Duration - dur); d <= 3 {
			sc += 3
		} else if d > 10 {
			sc -= 2
		}
	}
	return sc
}

// lyricGap reports an intro or a long instrumental part: the time from
// which nothing is sung and when the next sung line starts. ok is false
// when a line is being sung or the next one is only moments away.
func lyricGap(l *Lyrics, i int, lp float64) (from, to float64, ok bool) {
	if l == nil || len(l.Lines) == 0 {
		return 0, 0, false
	}
	if i >= 0 && i < len(l.Lines) && strings.TrimSpace(l.Lines[i].Text) != "" {
		return 0, 0, false
	}
	n := i + 1
	for n < len(l.Lines) && strings.TrimSpace(l.Lines[n].Text) == "" {
		n++
	}
	if n >= len(l.Lines) {
		return 0, 0, false
	}
	if i >= 0 {
		from = l.Lines[i].T
	}
	to = l.Lines[n].T
	return from, to, to-lp > 2.5
}
