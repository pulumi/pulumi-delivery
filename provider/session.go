// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/urn"

	"github.com/pulumi/pulumi-delivery/provider/cloudclient"
)

const (
	modeLocal = "local"

	// pollInterval is the time between two polls of a wait.
	pollInterval = 2 * time.Second
	// interruptTimeout bounds the interrupt request. It has its own context, because the context of the update
	// ends only at a second Ctrl-C.
	interruptTimeout = 3 * time.Second
	// maxPollFailures is the number of failed polls in a row that end a wait.
	maxPollFailures = 5
)

var errInterrupted = errors.New("the update was interrupted")

// session is the state of one provider process in an update. The mode and the run come from the answer to the
// first declaration. A service that answers without a mode is an older one, and the session then stays out of the way.
type session struct {
	id string

	// first serialises the declarations until the first one is answered, because the later ones carry its run.
	first sync.Mutex

	mu         sync.Mutex
	base       *cloudclient.Client
	declared   bool
	mode       string
	run        int64
	release    *int64
	consoleURL string
	commits    map[string]string
	lastRuns   map[string]int64

	announced sync.Once

	// waits and declaring count the operations in flight that an interrupt must end: the waits, and the declarations of a
	// local run.
	waits       int
	declaring   int
	interrupted chan struct{}
	interrupt   sync.Once
	posted      sync.Once
}

func newSession() *session {
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &session{
		id: hex.EncodeToString(id), commits: map[string]string{}, lastRuns: map[string]int64{},
		interrupted: make(chan struct{}),
	}
}

// local reports whether the service runs this update as a local run.
func (s *session) local() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode == modeLocal
}

// adopt makes a process that declared nothing, the one that runs the MemberRun resources, part of the local run of
// the process that declared. The engine runs the components and their children in different provider processes.
func (s *session) adopt(id string, run int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.declared {
		s.id, s.mode, s.run, s.declared = id, modeLocal, run, true
	}
}

func (s *session) runNumber() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.run
}

func (s *session) releaseNumber() *int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.release
}

// client returns the client of the stack of a resource. In a local run it names the session and the run.
func (s *session) client(organization string, resourceURN urn.URN) (*cloudclient.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.base == nil {
		client, err := clientFor(organization, resourceURN)
		if err != nil {
			return nil, err
		}
		s.base = client
	}
	if s.mode == modeLocal {
		return s.base.WithRun(s.id, s.run), nil
	}
	return s.base, nil
}

// declare sends a declaration. A request for a local run carries the session. Until the first answer, the
// declarations go one at a time.
func (s *session) declare(ctx context.Context, organization string, resourceURN urn.URN, d cloudclient.Declaration) (cloudclient.DeclarationResponse, error) {
	if d.Mode != modeLocal {
		client, err := s.client(organization, resourceURN)
		if err != nil {
			return cloudclient.DeclarationResponse{}, err
		}
		return client.Declare(ctx, d)
	}
	s.mu.Lock()
	s.declaring++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.declaring--
		s.mu.Unlock()
	}()
	s.first.Lock()
	s.mu.Lock()
	known := s.declared
	s.mu.Unlock()
	if known {
		s.first.Unlock()
		client, err := s.client(organization, resourceURN)
		if err != nil {
			return cloudclient.DeclarationResponse{}, err
		}
		return client.Declare(ctx, d)
	}
	defer s.first.Unlock()
	client, err := s.client(organization, resourceURN)
	if err != nil {
		return cloudclient.DeclarationResponse{}, err
	}
	d.Session = s.id
	resp, err := client.Declare(ctx, d)
	if err != nil {
		return resp, err
	}
	s.mu.Lock()
	s.declared, s.mode, s.run, s.release, s.consoleURL = true, resp.Mode, resp.RunNumber, resp.ReleaseNumber, resp.ConsoleURL
	s.mu.Unlock()
	select {
	case <-s.interrupted:
		s.postInterrupt()
	default:
	}
	return resp, nil
}

// announce shows the run link once.
func (s *session) announce(log userLog) {
	if !s.local() {
		return
	}
	s.announced.Do(func() {
		s.mu.Lock()
		run, release, link := s.run, s.release, s.consoleURL
		s.mu.Unlock()
		line := fmt.Sprintf("Run %d", run)
		if release != nil {
			line += fmt.Sprintf(" (release %d)", *release)
		}
		if link != "" {
			line += ": " + link
		}
		log.Info(line)
	})
}

func (s *session) setCommit(source urn.URN, commit string) {
	if commit == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commits[string(source)] = commit
}

// commitsOf returns the pinned commits of the sources that the run has a commit for.
func (s *session) commitsOf(sources []urn.URN) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for _, source := range sources {
		if commit, ok := s.commits[string(source)]; ok {
			out[string(source)] = commit
		}
	}
	return out
}

// setLastRun records the run that last ran a member. The keys are the URNs that name it.
func (s *session) setLastRun(lastRun int64, keys ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range keys {
		s.lastRuns[key] = max(s.lastRuns[key], lastRun)
	}
}

// lastRunsOf returns the last run of each of the URNs that a MemberRun reported in this update.
func (s *session) lastRunsOf(urns []string) map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int64{}
	for _, u := range urns {
		if run, ok := s.lastRuns[u]; ok {
			out[u] = run
		}
	}
	return out
}

// permanent is an error that ends a wait at once.
type permanent struct{ err error }

func (e permanent) Error() string { return e.err.Error() }
func (e permanent) Unwrap() error { return e.err }

// wait runs poll every pollInterval until it says done or fails. The wait counts as in flight for Cancel, and it
// ends when the session is interrupted. The context that poll gets ends then too.
func (s *session) wait(ctx context.Context, poll func(context.Context) (bool, error)) error {
	s.mu.Lock()
	s.waits++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.waits--
		s.mu.Unlock()
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-s.interrupted:
			cancel()
		case <-ctx.Done():
		}
	}()
	stopped := func() error {
		select {
		case <-s.interrupted:
			return errInterrupted
		default:
			return ctx.Err()
		}
	}

	failures := 0
	for {
		if err := stopped(); err != nil {
			return err
		}
		done, err := poll(ctx)
		var stop permanent
		var apiErr *cloudclient.APIError
		switch {
		case err == nil:
			failures = 0
			if done {
				return nil
			}
		case errors.As(err, &stop):
			return stop.err
		case errors.As(err, &apiErr) && apiErr.StatusCode < 500:
			return err
		default:
			if stopErr := stopped(); stopErr != nil {
				return stopErr
			}
			if failures++; failures >= maxPollFailures {
				return err
			}
		}
		select {
		case <-time.After(pollInterval):
		case <-ctx.Done():
			return stopped()
		}
	}
}

// cancel is the Cancel call of the engine. The engine calls it at every end of a provider process, so it only
// means an interrupt when a wait or a declaration of a local run is in flight: the person stopped the update, and the
// run must end. An interrupt during the first declaration is posted when that declaration is answered.
func (s *session) cancel() {
	s.mu.Lock()
	busy := s.waits > 0 || s.declaring > 0
	local := s.mode == modeLocal || (!s.declared && s.declaring > 0)
	s.mu.Unlock()
	if !busy || !local {
		return
	}
	s.interrupt.Do(func() { close(s.interrupted) })
	s.postInterrupt()
}

// postInterrupt tells the service about the interrupt, once, as soon as the run is known.
func (s *session) postInterrupt() {
	s.mu.Lock()
	client := s.base
	if client != nil {
		client = client.WithRun(s.id, s.run)
	}
	known := s.declared && s.mode == modeLocal
	s.mu.Unlock()
	if client == nil || !known {
		return
	}
	s.posted.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), interruptTimeout)
		defer cancel()
		_ = client.Interrupt(ctx)
	})
}

// admit waits until the service admits the stage. It shows the reason of a wait when the reason changes.
func (s *session) admit(ctx context.Context, client *cloudclient.Client, log userLog, stage urn.URN) error {
	last := ""
	return s.wait(ctx, func(ctx context.Context) (bool, error) {
		admission, err := client.AdmitStage(ctx, string(stage))
		if err != nil {
			return false, err
		}
		switch admission.Status {
		case "admitted":
			return true, nil
		case "waiting":
			line := fmt.Sprintf("Stage %s waits.", stage.Name())
			if admission.Reason != "" {
				line = fmt.Sprintf("Stage %s waits: %s", stage.Name(), admission.Reason)
			}
			if line = withLink(line, admission.Link); line != last {
				last = line
				log.Info(line)
			}
			return false, nil
		case "failed", "canceled", "superseded":
			reason := admission.Reason
			if reason == "" {
				reason = fmt.Sprintf("Stage %s ended with the status %s.", stage.Name(), admission.Status)
			}
			return false, permanent{errors.New(withLink(reason, admission.Link))}
		}
		return false, permanent{fmt.Errorf("the service answered an unknown stage status %q", admission.Status)}
	})
}

func withLink(text, link string) string {
	if link == "" {
		return text
	}
	return text + " (" + link + ")"
}
