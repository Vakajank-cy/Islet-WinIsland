package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"
)

const (
	listenAddr = "127.0.0.1:47821"
	hookURL    = "http://127.0.0.1:47821/hook"
	// How long the island waits for a click before handing the question
	// back to the normal Claude Code prompt in the terminal.
	permWait = 90 * time.Second
)

// onChange is called (from any goroutine) when the UI should redraw.
var onChange = func() {}

func newServer(st *State) http.Handler {
	mux := http.NewServeMux()
	registerWeb(mux)
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("islet"))
	})
	mux.HandleFunc("/hook", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			w.WriteHeader(http.StatusOK)
			return
		}
		var ev map[string]any
		if json.Unmarshal(body, &ev) != nil {
			w.WriteHeader(http.StatusOK)
			return
		}

		if str(ev["hook_event_name"]) != "PermissionRequest" {
			st.HandleEvent(ev)
			onChange()
			w.WriteHeader(http.StatusOK) // empty body = no decision
			return
		}

		p := st.AddPermission(ev)
		onChange()
		defer func() { st.RemovePermission(p); onChange() }()

		var decision string
		select {
		case decision = <-p.Result:
		case <-time.After(permWait):
		case <-r.Context().Done(): // Claude Code cancelled (e.g. Esc)
			return
		}
		if decision != "allow" && decision != "deny" {
			w.WriteHeader(http.StatusOK) // fall back to the terminal prompt
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName": "PermissionRequest",
				"decision":      map[string]any{"behavior": decision},
			},
		})
	})
	return mux
}

// startServer listens on localhost. It fails if another island is running.
func startServer(st *State) (net.Listener, error) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, err
	}
	go http.Serve(ln, newServer(st))
	return ln, nil
}
