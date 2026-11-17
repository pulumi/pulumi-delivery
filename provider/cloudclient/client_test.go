// Copyright 2026, Pulumi Corporation.  All rights reserved.

package cloudclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAPIErrorIncludesMethodAndPath proves APIError's message names the HTTP method and path
// that failed, not just the status and body -- without them, every delivery API failure reads
// identically regardless of which route caused it, which is next to useless when debugging a
// provider run against a real (or fake) backend.
func TestAPIErrorIncludesMethodAndPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":404,"message":"Not Found"}`))
	}))
	defer srv.Close()

	client := New(srv.URL, "org", "proj", "stack", "token")
	err := client.do(context.Background(), http.MethodGet, "/api/preview/some/path", nil, nil, nil)
	if err == nil {
		t.Fatalf("expected an error")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Method != http.MethodGet {
		t.Fatalf("Method = %q, want %q", apiErr.Method, http.MethodGet)
	}
	if apiErr.Path != "/api/preview/some/path" {
		t.Fatalf("Path = %q, want %q", apiErr.Path, "/api/preview/some/path")
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusNotFound)
	}

	msg := apiErr.Error()
	for _, want := range []string{http.MethodGet, "/api/preview/some/path", "404", "Not Found"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("Error() = %q, want it to contain %q", msg, want)
		}
	}
}

// TestRequestsCarryTheProtocolVersion proves that every route of the protocol sends version 2.
func TestRequestsCarryTheProtocolVersion(t *testing.T) {
	var paths []string
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		bodies = append(bodies, string(body))
		_, _ = w.Write([]byte(`{"number":1,"value":{"commit":"abc"}}`))
	}))
	defer srv.Close()

	client := New(srv.URL, "org", "proj", "stack", "token")
	if _, err := client.Declare(context.Background(), Declaration{URN: "urn", Type: "delivery:index:Stage"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SourceValue(context.Background(), SourceIdentity{Kind: "github"}); err != nil {
		t.Fatal(err)
	}

	if want := "POST /api/stacks/org/proj/stack/delivery/declarations"; paths[0] != want {
		t.Fatalf("path = %q, want %q", paths[0], want)
	}
	if !strings.Contains(bodies[0], `"protocolVersion":2`) {
		t.Fatalf("declaration body = %s", bodies[0])
	}
	if want := "GET /api/stacks/org/proj/stack/delivery/run?protocolVersion=2"; paths[1] != want {
		t.Fatalf("path = %q, want %q", paths[1], want)
	}
	if want := "POST /api/stacks/org/proj/stack/delivery/run/values"; paths[2] != want {
		t.Fatalf("path = %q, want %q", paths[2], want)
	}
	if !strings.Contains(bodies[2], `"protocolVersion":2`) || !strings.Contains(bodies[2], `"kind":"source"`) {
		t.Fatalf("run value body = %s", bodies[2])
	}
}

func TestDeclareReturnsTheMessageAndWarnings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"message":"Registered.","warnings":["too big"]}`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "org", "proj", "stack", "token").Declare(context.Background(), Declaration{URN: "urn"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Message != "Registered." || len(got.Warnings) != 1 || got.Warnings[0] != "too big" {
		t.Fatalf("response = %+v", got)
	}
}

// TestLocalRunRequests proves the wire shape of the calls of a local run: the session and the run number, the
// acknowledgement, the unchanged report, the admission and the interrupt.
func TestLocalRunRequests(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+string(body))
		if strings.HasSuffix(r.URL.Path, "/run/interrupt") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`{"status":"waiting","reason":"r","link":"l","mode":"local","runNumber":7,"releaseNumber":3,"consoleUrl":"c"}`))
	}))
	defer srv.Close()

	client := New(srv.URL, "org", "proj", "stack", "token")
	resp, err := client.Declare(context.Background(), Declaration{URN: "u", Mode: "local", Session: "s"})
	if err != nil || resp.Mode != "local" || resp.RunNumber != 7 || *resp.ReleaseNumber != 3 || resp.ConsoleURL != "c" {
		t.Fatalf("declare = %+v, %v", resp, err)
	}
	run := client.WithRun("s", 7)
	last := int64(2)
	steps := []func() error{
		func() error {
			_, err := run.Declare(context.Background(), Declaration{URN: "u", Mode: "local"})
			return err
		},
		func() error {
			_, err := run.AdmitStage(context.Background(), "stage")
			return err
		},
		func() error {
			_, err := run.PollMember(context.Background(), MemberStart{URN: "m", Entry: "e", ChildURN: "c"})
			return err
		},
		func() error { return run.AckMember(context.Background(), MemberStart{URN: "m", ChildURN: "c"}) },
		func() error {
			return run.ReportUnchanged(context.Background(), MemberStart{URN: "m", ChildURN: "c", LastReleaseNumber: &last})
		},
		func() error { return run.Interrupt(context.Background()) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}

	const base = "POST /api/stacks/org/proj/stack/delivery/"
	want := []string{
		base + `declarations {"protocolVersion":2,"mode":"local","session":"s","urn":"u","type":"","inputs":null,"dependencies":null,"propertyDependencies":null}`,
		base + `declarations {"protocolVersion":2,"mode":"local","session":"s","runNumber":7,"urn":"u","type":"","inputs":null,"dependencies":null,"propertyDependencies":null}`,
		base + `stages/admit {"protocolVersion":2,"runNumber":7,"session":"s","urn":"stage"}`,
		base + `members/start {"protocolVersion":2,"session":"s","runNumber":7,"urn":"m","entry":"e","childUrn":"c"}`,
		base + `members/start {"protocolVersion":2,"session":"s","runNumber":7,"urn":"m","entry":"","childUrn":"c","ack":true}`,
		base + `members/start {"protocolVersion":2,"session":"s","runNumber":7,"urn":"m","entry":"","childUrn":"c","unchanged":true,"lastReleaseNumber":2}`,
		base + `run/interrupt {"protocolVersion":2,"runNumber":7,"session":"s"}`,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d requests, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d:\n got %s\nwant %s", i, got[i], want[i])
		}
	}
}

// TestAPollIsNotLimitedByTheTimeoutOfOtherCalls proves that a poll request has its own timeout, and that the
// client has no timeout of its own that would end a long wait.
func TestAPollIsNotLimitedByTheTimeoutOfOtherCalls(t *testing.T) {
	client := New("http://example.test", "org", "proj", "stack", "token")
	if client.http.Timeout != 0 {
		t.Fatalf("the http client has a timeout of %s", client.http.Timeout)
	}
	if PollTimeout <= requestTimeout {
		t.Fatalf("PollTimeout %s must exceed the %s of a plain request", PollTimeout, requestTimeout)
	}
}
