// Copyright 2026, Pulumi Corporation.  All rights reserved.

// Package cloudclient is a small REST client (net/http only) for the Delivery routes of
// Pulumi Cloud: stacks/{orgName}/{projectName}/{stackName}/delivery/...
package cloudclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.pulumi.com"

// requestTimeout bounds a single HTTP attempt; do() allows one retry on top
// of it, so a fully-retried call can take up to ~2x this.
const requestTimeout = 15 * time.Second

// Client is a minimal REST client for the delivery API, scoped to one Pulumi stack (the
// delivery program's own org/project/stack identity -- see provider/reporter.go's
// getReporter for how that's derived). One Client per provider process is enough -- it's
// stateless beyond its http.Client.
type Client struct {
	baseURL             string
	org, project, stack string
	token               string
	http                *http.Client
}

// New builds a Client scoped to org/project/stack (the delivery program's own stack
// identity). baseURL defaults to api.pulumi.com when empty (mirroring PULUMI_BACKEND_URL's
// own default). token is sent as `Authorization: token <token>` on every request.
func New(baseURL, org, project, stack, token string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		org:     org,
		project: project,
		stack:   stack,
		token:   token,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// APIError is returned for any non-2xx response.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

// Error returns the message of the service when the body has one.
func (e *APIError) Error() string {
	var body struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(e.Body), &body) == nil && body.Message != "" {
		return fmt.Sprintf("%s (%s %s returned %d)", body.Message, e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("delivery API %s %s returned %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// do sends one request and decodes a JSON response into out (if non-nil).
// It retries exactly once, on a 5xx response or a transport-level error --
// per the task's "timeouts + one retry on 5xx" -- and otherwise returns
// immediately, including on 4xx (retrying a client error can't help).
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
	}

	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		var bodyReader io.Reader
		if payload != nil {
			bodyReader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
		if err != nil {
			return fmt.Errorf("building %s %s: %w", method, path, err)
		}
		req.Header.Set("Authorization", "token "+c.token)
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("%s %s: %w", method, path, err)
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("%s %s: reading response body: %w", method, path, readErr)
			continue
		}

		if resp.StatusCode >= 500 {
			lastErr = &APIError{Method: method, Path: path, StatusCode: resp.StatusCode, Body: string(respBody)}
			continue
		}
		if resp.StatusCode >= 400 {
			return &APIError{Method: method, Path: path, StatusCode: resp.StatusCode, Body: string(respBody)}
		}

		if out != nil && len(respBody) > 0 {
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("%s %s: decoding response: %w", method, path, err)
			}
		}
		return nil
	}
	return lastErr
}

// deliveryPath is the base path of the Delivery routes of the stack.
func (c *Client) deliveryPath() string {
	return "/api/stacks/" + url.PathEscape(c.org) + "/" + url.PathEscape(c.project) +
		"/" + url.PathEscape(c.stack) + "/delivery"
}
