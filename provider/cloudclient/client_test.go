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

// TestRequestsCarryTheProtocolVersion proves that every route of the protocol sends version 1.
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
	if !strings.Contains(bodies[0], `"protocolVersion":1`) {
		t.Fatalf("declaration body = %s", bodies[0])
	}
	if want := "GET /api/stacks/org/proj/stack/delivery/run?protocolVersion=1"; paths[1] != want {
		t.Fatalf("path = %q, want %q", paths[1], want)
	}
	if want := "POST /api/stacks/org/proj/stack/delivery/run/values"; paths[2] != want {
		t.Fatalf("path = %q, want %q", paths[2], want)
	}
	if !strings.Contains(bodies[2], `"protocolVersion":1`) || !strings.Contains(bodies[2], `"kind":"source"`) {
		t.Fatalf("run value body = %s", bodies[2])
	}
}

// TestWebhookValueAsksForTheKindWebhook proves that the value of a webhook source is read by name under the kind
// "webhook" of the run value route.
func TestWebhookValueAsksForTheKindWebhook(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(`{"value":{"subject":"issue-1","version":"2026-01-01T00:00:00Z","data":{"title":"Fix it"}}}`))
	}))
	defer srv.Close()

	value, err := New(srv.URL, "org", "proj", "stack", "token").WebhookValue(context.Background(), "issues")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"protocolVersion":1`, `"kind":"webhook"`, `"identity":{"name":"issues"}`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want it to contain %s", body, want)
		}
	}
	if value["subject"] != "issue-1" || value["data"].(map[string]any)["title"] != "Fix it" {
		t.Fatalf("value = %v", value)
	}
}

// TestWebhookURLNamesThePipelineStack proves that the url of a webhook source is under the pipeline stack: a run
// stack loses its -r<run number> suffix, and a stack whose name only looks like one keeps it.
func TestWebhookURLNamesThePipelineStack(t *testing.T) {
	const want = "https://api.example.com/api/preview/delivery/webhooks/acme/shop-delivery/main/issues"
	if got := New("https://api.example.com/", "acme", "shop-delivery", "main", "token").WebhookURL("issues", nil); got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
	run := &RunInfo{Number: 12}
	if got := New("https://api.example.com", "acme", "shop-delivery", "main-r12", "token").WebhookURL("issues", run); got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
	if got := New("https://api.example.com", "acme", "shop-delivery", "main-r12", "token").WebhookURL("issues", &RunInfo{Number: 3}); !strings.HasSuffix(got, "/shop-delivery/main-r12/issues") {
		t.Fatalf("url = %q, want the stack kept", got)
	}
	if got := New("", "acme", "my project", "main", "token").WebhookURL("a b", nil); got != "https://api.pulumi.com/api/preview/delivery/webhooks/acme/my%20project/main/a%20b" {
		t.Fatalf("url = %q", got)
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
