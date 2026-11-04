// Copyright 2026, Pulumi Corporation.  All rights reserved.

package cloudclient

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// ProtocolVersion is the version of the declaration protocol that this client speaks.
const ProtocolVersion = 1

// Declaration is the body of POST {stack}/delivery/declarations.
type Declaration struct {
	ProtocolVersion int    `json:"protocolVersion"`
	URN             string `json:"urn"`
	Type            string `json:"type"`
	// Inputs are the raw inputs in the Pulumi JSON property encoding.
	Inputs               map[string]any      `json:"inputs"`
	Dependencies         []string            `json:"dependencies"`
	PropertyDependencies map[string][]string `json:"propertyDependencies"`
}

// DeclarationResponse is what the service says about a declaration: an optional message for the user
// and warnings.
type DeclarationResponse struct {
	Message  string   `json:"message,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// Declare records a declaration for the update in progress on the stack.
func (c *Client) Declare(ctx context.Context, d Declaration) (DeclarationResponse, error) {
	d.ProtocolVersion = ProtocolVersion
	var out DeclarationResponse
	err := c.do(ctx, http.MethodPost, c.deliveryPath()+"/declarations", nil, d, &out)
	return out, err
}

// RunInfo is the run that a run stack belongs to.
type RunInfo struct {
	Number            int64  `json:"number"`
	Kind              string `json:"kind"`
	Status            string `json:"status"`
	ObservationNumber int64  `json:"observationNumber"`
	// ReleaseNumber is the release of the run. It is nil while the run has none.
	ReleaseNumber *int64 `json:"releaseNumber"`
}

// GetRun returns the run of the stack. A stack that is not a run stack (a registration) gives a nil run.
func (c *Client) GetRun(ctx context.Context) (*RunInfo, error) {
	var out RunInfo
	query := url.Values{"protocolVersion": {strconv.Itoa(ProtocolVersion)}}
	err := c.do(ctx, http.MethodGet, c.deliveryPath()+"/run", query, nil, &out)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// SourceIdentity identifies a source. The service canonicalizes it.
type SourceIdentity struct {
	Kind       string `json:"kind"`
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
}

// PipelineIdentity identifies the stage of a pipeline that a PipelineSource reads.
type PipelineIdentity struct {
	Pipeline string `json:"pipeline"`
	Stage    string `json:"stage"`
}

type runValueRequest struct {
	ProtocolVersion int    `json:"protocolVersion"`
	Kind            string `json:"kind"`
	Identity        any    `json:"identity"`
}

// RunValue returns the value that the run recorded for an identity of the given kind ("source" or "pipeline").
func (c *Client) RunValue(ctx context.Context, kind string, identity any) (map[string]any, error) {
	var out struct {
		Value map[string]any `json:"value"`
	}
	body := runValueRequest{ProtocolVersion: ProtocolVersion, Kind: kind, Identity: identity}
	if err := c.do(ctx, http.MethodPost, c.deliveryPath()+"/run/values", nil, body, &out); err != nil {
		return nil, err
	}
	return out.Value, nil
}

// SourceValue returns the value that the run recorded for a source, for example its commit.
func (c *Client) SourceValue(ctx context.Context, identity SourceIdentity) (map[string]any, error) {
	return c.RunValue(ctx, "source", identity)
}

// MemberStart is the body of POST {stack}/delivery/members/start.
type MemberStart struct {
	ProtocolVersion int    `json:"protocolVersion"`
	URN             string `json:"urn"`
	// Entry is "" for a Job and the key of the entry for a Stacks resource.
	Entry    string `json:"entry"`
	ChildURN string `json:"childUrn"`
}

// MemberStatus is the answer of the member start route.
type MemberStatus struct {
	// Status is waiting, running, succeeded or failed.
	Status string `json:"status"`
	Reason string `json:"reason"`
	// Outputs are set for succeeded, in the Pulumi JSON property encoding.
	Outputs map[string]any `json:"outputs"`
}

// StartMember starts a member in the run, or reports where it is. It is idempotent: it is also the poll.
func (c *Client) StartMember(ctx context.Context, start MemberStart) (MemberStatus, error) {
	start.ProtocolVersion = ProtocolVersion
	var out MemberStatus
	err := c.do(ctx, http.MethodPost, c.deliveryPath()+"/members/start", nil, start, &out)
	return out, err
}
