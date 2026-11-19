package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Lyrics translation into Lithuanian through Google Translate's free web
// endpoint (no account). It is unofficial, so it can stop working one day;
// the island then simply shows the original lyrics.

var translateURL = "https://translate.googleapis.com/translate_a/single?client=gtx&sl=auto&dt=t&tl="

// parseGtx turns the endpoint's nested-array answer into text + source language.
func parseGtx(body []byte) (string, string, error) {
	var raw []any
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", "", err
	}
	if len(raw) == 0 {
		return "", "", errors.New("tuščias atsakymas")
	}
	segs, _ := raw[0].([]any)
	var b strings.Builder
	for _, sg := range segs {
		if arr, ok := sg.([]any); ok && len(arr) > 0 {
			if t, ok := arr[0].(string); ok {
				b.WriteString(t)
			}
		}
	}
	src := ""
	if len(raw) > 2 {
		src, _ = raw[2].(string)
	}
	return b.String(), src, nil
}

// TranslateLines translates lyric lines, keeping one output line per input.
// Returns nil (no error) if the song is already in Lithuanian.
func TranslateLines(client *http.Client, lines []string, tl string) ([]string, error) {
	// translate only non-empty lines, joined by newlines
	var idx []int
	var in []string
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			idx = append(idx, i)
			in = append(in, strings.TrimSpace(l))
		}
	}
	if len(in) == 0 {
		return nil, nil
	}
	text, src, err := TranslateText(client, strings.Join(in, "\n"), tl)
	if err != nil {
		return nil, err
	}
	if src == tl {
		return nil, nil
	}
	outLines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(outLines) != len(in) {
		return nil, errors.New("vertimo eilutės nesutampa")
	}
	out := make([]string, len(lines))
	for k, i := range idx {
		out[i] = strings.TrimSpace(outLines[k])
	}
	return out, nil
}

// TranslateText translates text into language tl ("lt", "en", "ru").
func TranslateText(client *http.Client, text, tl string) (string, string, error) {
	form := url.Values{"q": {text}}
	req, _ := http.NewRequest("POST", translateURL+tl, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	req.Header.Set("User-Agent", "Mozilla/5.0 Islet")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", errors.New(resp.Status)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return parseGtx(body)
}

// EnsureTranslation starts translating the lyrics of m once (background).
// The cached *Lyrics is replaced by a copy with Trans filled in.
func (s *LyricsStore) EnsureTranslation(m *Media) {
	if m == nil {
		return
	}
	key := m.Key()
	s.mu.Lock()
	l, ok := s.cache[key]
	tl := curLang
	if !ok || l.State != lyrSynced || (l.Trans != nil && l.TransLang == tl) || s.transTried[key+tl] {
		s.mu.Unlock()
		return
	}
	s.transTried[key+tl] = true
	s.mu.Unlock()
	go func() {
		lines := make([]string, len(l.Lines))
		for i, ln := range l.Lines {
			lines[i] = ln.Text
		}
		c := &http.Client{Timeout: 15 * time.Second}
		tr, err := TranslateLines(c, lines, tl)
		if err != nil || tr == nil {
			return
		}
		cp := *l
		cp.Trans, cp.TransLang = tr, tl
		s.mu.Lock()
		if s.cache[key] == l {
			s.cache[key] = &cp
		}
		s.mu.Unlock()
		if s.OnReady != nil {
			s.OnReady()
		}
	}()
}
