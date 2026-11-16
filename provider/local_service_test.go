// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// localService is a fake of the Delivery routes for a local run. It checks the session and the run number of every
// call after the first declaration, and records what the provider sent, in order.
type localService struct {
	*httptest.Server

	mu sync.Mutex
	// mode is the mode of the answer to a declaration. An empty one is an older service.
	mode            string
	run, release    int64
	consoleURL      string
	commits         map[string]string
	members         map[string]*memberScript
	admit           map[string]func(polls int) (status, reason string)
	cloudRun        bool
	rejectDeclareIn string

	session      string
	declarations []map[string]any
	events       []string
	polls        map[string]int
	admitPolls   map[string]int
	execs        map[string]int
	unchanged    map[string]*int64
	interrupts   int
	runLookups   int
	violations   []string
}

// memberScript says how the service answers the polls of one member. The key is the member name, with #entry for
// an entry of a Stacks resource.
type memberScript struct {
	waitPolls int
	hang      bool
	fail      bool
	reason    string
	link      string
}

func newLocalService(t *testing.T) *localService {
	s := &localService{
		mode: "local", run: 6, release: 3, consoleURL: "https://example.test/run/7",
		commits: map[string]string{}, members: map[string]*memberScript{}, admit: map[string]func(int) (string, string){},
		polls: map[string]int{}, admitPolls: map[string]int{}, execs: map[string]int{}, unchanged: map[string]*int64{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

func memberKey(urn, entry string) string {
	name := urn[strings.LastIndex(urn, "::")+2:]
	if entry != "" {
		name += "#" + entry
	}
	return name
}

func (s *localService) violate(format string, args ...any) {
	s.violations = append(s.violations, fmt.Sprintf(format, args...))
}

// checkRun records a violation when a call does not name the session and the run of the first declaration.
func (s *localService) checkRun(path string, body map[string]any) {
	if s.mode != "local" {
		return
	}
	if body["session"] != s.session || s.session == "" {
		s.violate("%s: session %v, want %q", path, body["session"], s.session)
	}
	if body["runNumber"] != float64(s.run) {
		s.violate("%s: runNumber %v, want %d", path, body["runNumber"], s.run)
	}
}

func (s *localService) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	w.Header().Set("Content-Type", "application/json")
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	path := r.URL.Path[strings.Index(r.URL.Path, "/delivery/")+len("/delivery/"):]
	switch {
	case r.Method == http.MethodGet && path == "run":
		s.runLookups++
		if !s.cloudRun {
			w.WriteHeader(http.StatusNotFound)
			reply(map[string]any{"code": 404, "message": "This stack is not a Delivery run stack."})
			return
		}
		reply(map[string]any{"number": 1, "kind": "normal", "status": "in_progress", "observationNumber": 1})
	case path == "declarations":
		urn, _ := body["urn"].(string)
		s.declarations = append(s.declarations, body)
		s.events = append(s.events, "declare "+memberKey(urn, ""))
		if s.mode == "local" && body["mode"] == "local" {
			if fmt.Sprint(body["session"]) != s.session && body["runNumber"] == nil {
				s.session = fmt.Sprint(body["session"])
				s.run++
				if s.session == "" {
					s.violate("first declaration without a session")
				}
			} else {
				s.checkRun(path, body)
			}
		}
		response := map[string]any{"warnings": []string{}}
		if s.mode != "" {
			response["mode"] = s.mode
		}
		if s.mode == "local" {
			response["runNumber"], response["releaseNumber"], response["consoleUrl"] = s.run, s.release, s.consoleURL
		}
		reply(response)
	case path == "stages/admit":
		s.checkRun(path, body)
		name := memberKey(fmt.Sprint(body["urn"]), "")
		s.admitPolls[name]++
		status, reason := "admitted", ""
		if script, ok := s.admit[name]; ok {
			status, reason = script(s.admitPolls[name])
		}
		s.events = append(s.events, fmt.Sprintf("admit %s:%s", name, status))
		reply(map[string]any{"status": status, "reason": reason, "link": "https://example.test/stage/" + name})
	case path == "members/start":
		s.memberStart(body, reply)
	case path == "run/values":
		s.checkRun(path, body)
		repository := ""
		if identity, ok := body["identity"].(map[string]any); ok {
			repository, _ = identity["repository"].(string)
		}
		commit := s.commits[repository]
		if commit == "" {
			commit = "c1"
		}
		reply(map[string]any{"value": map[string]any{"commit": commit}})
	case path == "run/interrupt":
		s.checkRun(path, body)
		s.interrupts++
		s.events = append(s.events, "interrupt")
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *localService) memberStart(body map[string]any, reply func(any)) {
	if s.mode == "local" {
		s.checkRun("members/start", body)
	}
	entry, _ := body["entry"].(string)
	name := memberKey(fmt.Sprint(body["urn"]), entry)
	switch {
	case body["ack"] == true:
		s.events = append(s.events, "ack "+name)
		reply(map[string]any{"status": "succeeded"})
		return
	case body["unchanged"] == true:
		s.events = append(s.events, "unchanged "+name)
		if last, ok := body["lastReleaseNumber"].(float64); ok {
			n := int64(last)
			s.unchanged[name] = &n
		} else {
			s.unchanged[name] = nil
		}
		reply(map[string]any{"status": "unchanged"})
		return
	}
	script := s.members[name]
	if script == nil {
		script = &memberScript{}
	}
	s.polls[name]++
	switch {
	case script.hang || s.polls[name] <= script.waitPolls:
		reply(map[string]any{"status": "waiting", "reason": "The member is running."})
	case script.fail:
		s.events = append(s.events, "final "+name+":failed")
		reply(map[string]any{"status": "failed", "reason": script.reason, "link": script.link})
		s.polls[name] = 0
	default:
		s.execs[name]++
		s.events = append(s.events, "final "+name+":succeeded")
		reply(map[string]any{"status": "succeeded", "outputs": map[string]any{"pool": "blue", "exec": fmt.Sprintf("%s-%d", name, s.execs[name])}})
		s.polls[name] = 0
	}
}

func (s *localService) snapshot(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f()
}

// take returns the events since the last take.
func (s *localService) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.events
	s.events = nil
	return out
}

func (s *localService) violationList() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.violations)
}

func (s *localService) interruptCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.interrupts
}

func (s *localService) declared() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.declarations)
}

func (s *localService) waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		ok := condition()
		s.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// finals returns the names of the members that got a final answer, in order.
func finals(events []string) []string {
	var out []string
	for _, e := range events {
		if rest, ok := strings.CutPrefix(e, "final "); ok {
			out = append(out, rest)
		}
	}
	return out
}

func eventsWith(events []string, prefix string) []string {
	var out []string
	for _, e := range events {
		if strings.HasPrefix(e, prefix) {
			out = append(out, strings.TrimPrefix(e, prefix))
		}
	}
	return out
}

func indexOf(events []string, event string) int { return slices.Index(events, event) }
