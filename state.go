package main

import (
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Mode is what the island is currently showing.
type Mode int

const (
	ModeIdle Mode = iota
	ModeWorking
	ModePermission
	ModeDone
	ModeWaiting
)

// Sound kinds the UI should play.
type Sound int

const (
	SoundDone Sound = iota
	SoundPermission
	SoundWaiting
)

// PermReq is one pending Claude Code permission request.
type PermReq struct {
	ID      int
	Session string
	Tool    string
	Detail  string
	Project string
	Created time.Time
	Result  chan string // "allow", "deny" or "" (answer in terminal)
	once    sync.Once
}

func (p *PermReq) answer(v string) {
	p.once.Do(func() { p.Result <- v })
}

// State is shared between the HTTP server goroutines and the UI thread.
type State struct {
	mu        sync.Mutex
	mode      Mode
	text      string
	detail    string
	project   string
	lastEvent time.Time
	modeSince time.Time
	pending   []*PermReq
	nextID    int
	sounds    []Sound
	notices   []Notice
	version   int
	sessions  map[string]*Session
}

// Session is one running Claude Code conversation.
type Session struct {
	ID      string
	Project string
	Text    string
	Detail  string
	Mode    Mode
	Last    time.Time
}

// Notice is a Windows notification to show.
type Notice struct{ Title, Body string }

// Snapshot is a copy of the state for one frame.
type Snapshot struct {
	Mode      Mode
	Text      string
	Detail    string
	Project   string
	ModeSince time.Time
	Perm      *PermReq
	PermCount int
	Version   int
	Sessions  []Session
}

func NewState() *State {
	now := time.Now()
	return &State{mode: ModeIdle, lastEvent: now, modeSince: now}
}

func (s *State) setMode(m Mode) {
	if s.mode != m {
		s.mode = m
		s.modeSince = time.Now()
	}
	s.version++
}

func (s *State) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{Mode: s.mode, Text: s.text, Detail: s.detail, Project: s.project,
		ModeSince: s.modeSince, PermCount: len(s.pending), Version: s.version}
	if len(s.pending) > 0 {
		snap.Mode = ModePermission
		snap.Perm = s.pending[0]
	}
	for _, se := range s.sessions {
		if time.Since(se.Last) < 30*time.Minute {
			snap.Sessions = append(snap.Sessions, *se)
		}
	}
	sort.Slice(snap.Sessions, func(i, j int) bool { return snap.Sessions[i].Last.After(snap.Sessions[j].Last) })
	return snap
}

// TakeSounds returns and clears queued sounds.
func (s *State) TakeSounds() []Sound {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.sounds
	s.sounds = nil
	return out
}

// TakeNotices returns and clears queued notifications.
func (s *State) TakeNotices() []Notice {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.notices
	s.notices = nil
	return out
}

func (s *State) notify(title, body string) {
	s.notices = append(s.notices, Notice{title, body})
}

// Tick handles time-based transitions. Returns true if something changed.
func (s *State) Tick(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.mode {
	case ModeDone:
		if now.Sub(s.modeSince) > 12*time.Second {
			s.setMode(ModeIdle)
			return true
		}
	case ModeWorking:
		// Claude Code was closed or crashed mid-task.
		if now.Sub(s.lastEvent) > 10*time.Minute {
			s.setMode(ModeIdle)
			return true
		}
	}
	return false
}

// Dismiss returns to idle (user clicked a done/waiting island).
func (s *State) Dismiss() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mode == ModeDone || s.mode == ModeWaiting {
		s.setMode(ModeIdle)
	}
}

// HandleEvent processes one hook event (everything except PermissionRequest).
func (s *State) HandleEvent(ev map[string]any) {
	name := str(ev["hook_event_name"])
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastEvent = time.Now()
	if cwd := str(ev["cwd"]); cwd != "" {
		s.project = baseName(cwd)
	}
	defer s.trackSession(str(ev["session_id"]))
	switch name {
	case "UserPromptSubmit", "PostToolUse", "PostToolUseFailure", "SubagentStart":
		s.text, s.detail = "Galvoja", ""
		s.setMode(ModeWorking)
	case "PreToolUse":
		tool := str(ev["tool_name"])
		input, _ := ev["tool_input"].(map[string]any)
		s.text = toolVerb(tool)
		s.detail = toolDetail(tool, input)
		s.setMode(ModeWorking)
	case "Stop":
		s.text, s.detail = "Baigta", ""
		s.setMode(ModeDone)
		s.sounds = append(s.sounds, SoundDone)
		s.notify(L("Claude baigė darbą"), projectLine(s.project, L("Užduotis atlikta")))
	case "StopFailure":
		s.text, s.detail = "Sustojo dėl klaidos", ""
		s.setMode(ModeWaiting)
		s.sounds = append(s.sounds, SoundWaiting)
		s.notify(L("Claude sustojo dėl klaidos"), projectLine(s.project, L("Pažiūrėk į terminalą")))
	case "Notification":
		switch str(ev["notification_type"]) {
		case "permission_prompt":
			// Only if the island isn't already handling it.
			if len(s.pending) == 0 {
				s.text, s.detail = "Reikia leidimo terminale", ""
				s.setMode(ModeWaiting)
				s.sounds = append(s.sounds, SoundPermission)
				s.notify(L("Claude prašo leidimo"), projectLine(s.project, L("Atsakyk terminale")))
			}
		case "elicitation_dialog", "elicitation_url_dialog", "agent_needs_input":
			s.text, s.detail = "Claude turi klausimą", str(ev["message"])
			s.setMode(ModeWaiting)
			s.sounds = append(s.sounds, SoundWaiting)
			s.notify("Claude turi klausimą", projectLine(s.project, str(ev["message"])))
		}
	}
}

// trackSession copies the current status into the session's entry.
func (s *State) trackSession(id string) {
	if id == "" {
		return
	}
	if s.sessions == nil {
		s.sessions = map[string]*Session{}
	}
	se := s.sessions[id]
	if se == nil {
		se = &Session{ID: id}
		s.sessions[id] = se
	}
	se.Project, se.Text, se.Detail, se.Mode, se.Last = s.project, s.text, s.detail, s.mode, time.Now()
}

// AddPermission queues a permission request and returns it.
func (s *State) AddPermission(ev map[string]any) *PermReq {
	tool := str(ev["tool_name"])
	input, _ := ev["tool_input"].(map[string]any)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	s.lastEvent = time.Now()
	p := &PermReq{
		ID:      s.nextID,
		Session: str(ev["session_id"]),
		Tool:    tool,
		Detail:  permDetail(tool, input),
		Project: baseName(str(ev["cwd"])),
		Created: time.Now(),
		Result:  make(chan string, 1),
	}
	s.pending = append(s.pending, p)
	s.sounds = append(s.sounds, SoundPermission)
	s.notify(L("Claude prašo leidimo"), strings.TrimSpace(p.Tool+": "+p.Detail))
	s.version++
	return p
}

// RemovePermission drops a request (answered, timed out or cancelled).
func (s *State) RemovePermission(p *PermReq) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, q := range s.pending {
		if q == p {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			s.version++
			break
		}
	}
	if len(s.pending) == 0 && s.mode == ModeIdle {
		s.text = ""
	}
}

// Answer answers the first pending request from the UI.
func (s *State) Answer(decision string) {
	s.mu.Lock()
	var p *PermReq
	if len(s.pending) > 0 {
		p = s.pending[0]
	}
	s.mu.Unlock()
	if p != nil {
		p.answer(decision)
		s.RemovePermission(p)
		s.mu.Lock()
		if decision == "allow" {
			s.text, s.detail = "Leista", p.Tool
		} else if decision == "deny" {
			s.text, s.detail = "Uždrausta", p.Tool
		}
		s.setMode(ModeWorking)
		s.mu.Unlock()
	}
}

// ---------- helpers ----------

func str(v any) string {
	s, _ := v.(string)
	return s
}

func baseName(p string) string {
	if p == "" {
		return ""
	}
	p = strings.TrimRight(strings.ReplaceAll(p, "\\", "/"), "/")
	return filepath.Base(p)
}

func projectLine(project, text string) string {
	if project == "" {
		return text
	}
	if text == "" {
		return project
	}
	return project + " · " + text
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

func toolVerb(tool string) string {
	switch tool {
	case "Read", "NotebookRead":
		return "Skaito"
	case "Glob", "Grep", "LS":
		return "Ieško failuose"
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		return "Redaguoja"
	case "Bash", "PowerShell", "BashOutput":
		return "Vykdo"
	case "WebFetch", "WebSearch":
		return "Ieško internete"
	case "Task", "Agent":
		return "Paleido pagalbininką"
	case "TodoWrite", "TaskCreate", "TaskUpdate":
		return "Planuoja"
	}
	if strings.HasPrefix(tool, "mcp__") {
		return "Naudoja įrankį"
	}
	if tool == "" {
		return "Dirba"
	}
	return "Naudoja " + tool
}

func toolDetail(tool string, in map[string]any) string {
	if in == nil {
		return ""
	}
	if c := str(in["command"]); c != "" {
		return firstLine(c)
	}
	if f := str(in["file_path"]); f != "" {
		return baseName(f)
	}
	if f := str(in["notebook_path"]); f != "" {
		return baseName(f)
	}
	if p := str(in["pattern"]); p != "" {
		return p
	}
	if u := str(in["url"]); u != "" {
		if pu, err := url.Parse(u); err == nil && pu.Host != "" {
			return pu.Host
		}
		return u
	}
	if q := str(in["query"]); q != "" {
		return q
	}
	if d := str(in["description"]); d != "" {
		return firstLine(d)
	}
	if strings.HasPrefix(tool, "mcp__") {
		parts := strings.Split(tool, "__")
		return parts[len(parts)-1]
	}
	return ""
}

func permDetail(tool string, in map[string]any) string {
	if in == nil {
		return ""
	}
	if c := str(in["command"]); c != "" {
		return firstLine(c)
	}
	if f := str(in["file_path"]); f != "" {
		return f
	}
	return toolDetail(tool, in)
}
