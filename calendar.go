package main

import (
	"bufio"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Calendar events from an iCal (.ics) link – e.g. Google Calendar's
// "Secret address in iCal format". No sign-in needed.

type Event struct {
	Start  time.Time
	AllDay bool
	Title  string
}

type CalStore struct {
	mu      sync.Mutex
	url     string
	events  []Event
	err     string
	fetched time.Time
	OnReady func()
}

func (c *CalStore) SetURL(u string) {
	c.mu.Lock()
	changed := c.url != u
	c.url = u
	if changed {
		c.events, c.err, c.fetched = nil, "", time.Time{}
	}
	c.mu.Unlock()
}

// Upcoming returns events from now (or today, for all-day) for the next days.
func (c *CalStore) Upcoming(now time.Time, days int) ([]Event, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	end := today.AddDate(0, 0, days)
	var out []Event
	for _, e := range c.events {
		if e.AllDay && !e.Start.Before(today) && e.Start.Before(end) ||
			!e.AllDay && e.Start.After(now.Add(-10*time.Minute)) && e.Start.Before(end) {
			out = append(out, e)
		}
	}
	return out, c.err
}

func (c *CalStore) Start() {
	go func() {
		for {
			c.mu.Lock()
			u := c.url
			stale := time.Since(c.fetched) > 15*time.Minute
			c.mu.Unlock()
			if u != "" && stale {
				evs, err := fetchICS(u)
				c.mu.Lock()
				if c.url == u {
					c.fetched = time.Now()
					if err != nil {
						c.err = L("Nepavyko gauti kalendoriaus")
					} else {
						c.events, c.err = evs, ""
					}
				}
				c.mu.Unlock()
				if c.OnReady != nil {
					c.OnReady()
				}
			}
			time.Sleep(5 * time.Second)
		}
	}()
}

func fetchICS(u string) ([]Event, error) {
	u = strings.Replace(u, "webcal://", "https://", 1)
	cl := &http.Client{Timeout: 15 * time.Second}
	resp, err := cl.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, io.ErrUnexpectedEOF
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return ParseICS(string(body), time.Now()), nil
}

// ParseICS reads VEVENTs, expanding simple repeating rules for ~60 days.
func ParseICS(s string, now time.Time) []Event {
	// unfold continued lines
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n ", "")
	s = strings.ReplaceAll(s, "\n\t", "")
	var out []Event
	var ev map[string]string
	var params map[string]string
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	horizon := now.AddDate(0, 0, 60)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "BEGIN:VEVENT":
			ev, params = map[string]string{}, map[string]string{}
		case line == "END:VEVENT" && ev != nil:
			out = append(out, expandEvent(ev, params, now, horizon)...)
			ev = nil
		case ev != nil:
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			name, par, _ := strings.Cut(k, ";")
			if _, seen := ev[name]; !seen {
				ev[name] = v
				params[name] = par
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

func parseICSTime(v, par string) (time.Time, bool, bool) {
	allDay := strings.Contains(par, "VALUE=DATE") && !strings.Contains(par, "DATE-TIME") || len(v) == 8
	if allDay {
		t, err := time.ParseInLocation("20060102", v[:min(8, len(v))], time.Local)
		return t, true, err == nil
	}
	loc := time.Local
	if i := strings.Index(par, "TZID="); i >= 0 {
		tz := par[i+5:]
		if j := strings.IndexAny(tz, ";:"); j >= 0 {
			tz = tz[:j]
		}
		if l, err := time.LoadLocation(strings.Trim(tz, `"`)); err == nil {
			loc = l
		}
	}
	if strings.HasSuffix(v, "Z") {
		t, err := time.Parse("20060102T150405Z", v)
		return t.Local(), false, err == nil
	}
	t, err := time.ParseInLocation("20060102T150405", v, loc)
	return t.Local(), false, err == nil
}

func unescapeICS(s string) string {
	r := strings.NewReplacer(`\,`, ",", `\;`, ";", `\n`, " ", `\N`, " ", `\\`, `\`)
	return r.Replace(s)
}

var icsDays = map[string]time.Weekday{"SU": 0, "MO": 1, "TU": 2, "WE": 3, "TH": 4, "FR": 5, "SA": 6}

func expandEvent(ev, par map[string]string, now, horizon time.Time) []Event {
	start, allDay, ok := parseICSTime(ev["DTSTART"], par["DTSTART"])
	if !ok {
		return nil
	}
	title := unescapeICS(ev["SUMMARY"])
	if title == "" {
		title = "(be pavadinimo)"
	}
	rule := ev["RRULE"]
	if rule == "" {
		return []Event{{start, allDay, title}}
	}
	r := map[string]string{}
	for _, p := range strings.Split(rule, ";") {
		k, v, _ := strings.Cut(p, "=")
		r[k] = v
	}
	interval, _ := strconv.Atoi(r["INTERVAL"])
	if interval < 1 {
		interval = 1
	}
	count, _ := strconv.Atoi(r["COUNT"])
	until := horizon
	if u := r["UNTIL"]; u != "" {
		if t, _, ok := parseICSTime(u, ""); ok && t.Before(until) {
			until = t.Add(24 * time.Hour)
		}
	}
	var byDay []time.Weekday
	for _, d := range strings.Split(r["BYDAY"], ",") {
		if len(d) >= 2 {
			if wd, ok := icsDays[d[len(d)-2:]]; ok {
				byDay = append(byDay, wd)
			}
		}
	}
	var out []Event
	n := 0
	add := func(t time.Time) bool {
		if !t.Before(until) || (count > 0 && n >= count) {
			return false
		}
		n++
		if !t.Before(now.AddDate(0, 0, -1)) {
			out = append(out, Event{t, allDay, title})
		}
		return true
	}
	cur := start
	for i := 0; i < 2000; i++ {
		switch r["FREQ"] {
		case "DAILY":
			if !add(cur) {
				return out
			}
			cur = cur.AddDate(0, 0, interval)
		case "WEEKLY":
			if len(byDay) == 0 {
				if !add(cur) {
					return out
				}
			} else {
				weekStart := cur.AddDate(0, 0, -int(cur.Weekday()))
				for _, wd := range byDay {
					t := weekStart.AddDate(0, 0, int(wd))
					if t.Before(start) {
						continue
					}
					if !add(t) {
						return out
					}
				}
			}
			cur = cur.AddDate(0, 0, 7*interval)
		case "MONTHLY":
			if !add(cur) {
				return out
			}
			cur = cur.AddDate(0, interval, 0)
		case "YEARLY":
			if !add(cur) {
				return out
			}
			cur = cur.AddDate(interval, 0, 0)
		default:
			return []Event{{start, allDay, title}}
		}
		if cur.After(until) {
			break
		}
	}
	return out
}
