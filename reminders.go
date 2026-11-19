package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------- reminders ----------

type Reminder struct {
	ID   int       `json:"id"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

type Reminders struct {
	mu    sync.Mutex
	items []Reminder
	next  int
	path  string
}

func LoadReminders(path string) *Reminders {
	r := &Reminders{path: path, next: 1}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &r.items)
	}
	for _, it := range r.items {
		if it.ID >= r.next {
			r.next = it.ID + 1
		}
	}
	r.sort()
	return r
}

func (r *Reminders) sort() {
	sort.Slice(r.items, func(i, j int) bool { return r.items[i].At.Before(r.items[j].At) })
}

func (r *Reminders) save() {
	if r.path == "" {
		return
	}
	os.MkdirAll(filepath.Dir(r.path), 0o755)
	b, _ := json.MarshalIndent(r.items, "", "  ")
	os.WriteFile(r.path, b, 0o644)
}

func (r *Reminders) Add(text string, at time.Time) Reminder {
	r.mu.Lock()
	defer r.mu.Unlock()
	text = strings.TrimSpace(text)
	if text == "" {
		text = L("Priminimas")
	}
	it := Reminder{ID: r.next, Text: text, At: at}
	r.next++
	r.items = append(r.items, it)
	r.sort()
	r.save()
	return it
}

func (r *Reminders) Delete(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, it := range r.items {
		if it.ID == id {
			r.items = append(r.items[:i], r.items[i+1:]...)
			break
		}
	}
	r.save()
}

func (r *Reminders) List() []Reminder {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Reminder(nil), r.items...)
}

// Due removes and returns reminders whose time has come.
func (r *Reminders) Due(now time.Time) []Reminder {
	r.mu.Lock()
	defer r.mu.Unlock()
	var due, keep []Reminder
	for _, it := range r.items {
		if !now.Before(it.At) {
			due = append(due, it)
		} else {
			keep = append(keep, it)
		}
	}
	if len(due) > 0 {
		r.items = keep
		r.save()
	}
	return due
}

// untilText: "po 12 min", "po 1 h 05 min", "rytoj 08:30".
func untilText(at, now time.Time) string {
	d := at.Sub(now)
	if d < time.Minute {
		return L("dabar")
	}
	if d < time.Hour {
		return fmt.Sprintf(L("po %d min"), int(d.Minutes()+0.5))
	}
	if d < 12*time.Hour {
		return fmt.Sprintf(L("po %d h %02d min"), int(d.Hours()), int(d.Minutes())%60)
	}
	y1, m1, d1 := now.Date()
	y2, m2, d2 := at.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return L("šiandien ") + at.Format("15:04")
	case y1 == y2 && m1 == m2 && d2 == d1+1:
		return L("rytoj ") + at.Format("15:04")
	}
	return at.Format("01-02 15:04")
}

// ---------- file shelf (only paths are kept, files are not copied) ----------

type Shelf struct {
	Paths []string
	path  string
}

const shelfMax = 6

func LoadShelf(path string) *Shelf {
	s := &Shelf{path: path}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &s.Paths)
	}
	// forget files that no longer exist
	var keep []string
	for _, p := range s.Paths {
		if _, err := os.Stat(p); err == nil {
			keep = append(keep, p)
		}
	}
	s.Paths = keep
	return s
}

func (s *Shelf) save() {
	if s.path == "" {
		return
	}
	os.MkdirAll(filepath.Dir(s.path), 0o755)
	b, _ := json.Marshal(s.Paths)
	os.WriteFile(s.path, b, 0o644)
}

func (s *Shelf) Add(paths ...string) int {
	n := 0
	for _, p := range paths {
		dup := false
		for _, q := range s.Paths {
			if strings.EqualFold(p, q) {
				dup = true
			}
		}
		if !dup && p != "" {
			s.Paths = append([]string{p}, s.Paths...)
			n++
		}
	}
	if len(s.Paths) > shelfMax {
		s.Paths = s.Paths[:shelfMax]
	}
	s.save()
	return n
}

func (s *Shelf) Remove(i int) {
	if i >= 0 && i < len(s.Paths) {
		s.Paths = append(s.Paths[:i], s.Paths[i+1:]...)
		s.save()
	}
}

func (s *Shelf) Clear() {
	s.Paths = nil
	s.save()
}

func fileLabel(p string) (name, ext string) {
	p = strings.ReplaceAll(p, "\\", "/")
	name = p[strings.LastIndex(p, "/")+1:]
	if name == "" {
		name = p
	}
	if i := strings.LastIndex(name, "."); i > 0 && len(name)-i <= 6 {
		ext = strings.ToUpper(name[i+1:])
	}
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		ext = "DIR"
	}
	return
}
