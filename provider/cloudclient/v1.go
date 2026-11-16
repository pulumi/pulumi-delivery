// Copyright 2026, Pulumi Corporation.  All rights reserved.

package cloudclient

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ProtocolVersion is the version of the declaration protocol that this client speaks.
const ProtocolVersion = 2

// Declaration is the body of POST {stack}/delivery/declarations.
type Declaration struct {
	ProtocolVersion int `json:"protocolVersion"`
	// Mode is "register" when the update is a registration of the pipeline and "local" when the provider asks for a
	// local run. It is omitted otherwise.
	Mode string `json:"mode,omitempty"`
	// Session identifies the provider process. RunNumber is the run of a local update. Both are omitted until
	// the service has named the run.
	Session   string `json:"session,omitempty"`
	RunNumber int64  `json:"runNumber,omitempty"`
	URN       string `json:"urn"`
	Type      string `json:"type"`
	// Inputs are the raw inputs in the Pulumi JSON property encoding.
	Inputs               map[string]any      `json:"inputs"`
	Dependencies         []string            `json:"dependencies"`
	PropertyDependencies map[string][]string `json:"propertyDependencies"`
}

// DeclarationResponse is what the service says about a declaration: an optional message for the user
// and warnings. Mode is the mode that the service chose: "local", "register" or "pass". An older service sends none.
type DeclarationResponse struct {
	Message       string   `json:"message,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
	Mode          string   `json:"mode,omitempty"`
	RunNumber     int64    `json:"runNumber,omitempty"`
	ReleaseNumber *int64   `json:"releaseNumber,omitempty"`
	ConsoleURL    string   `json:"consoleUrl,omitempty"`
}

// WithRun returns a client whose requests name the session and the run of a local update.
func (c *Client) WithRun(session string, runNumber int64) *Client {
	run := *c
	run.session, run.runNumber = session, runNumber
	return &run
}

// Declare records a declaration for the update in progress on the stack.
func (c *Client) Declare(ctx context.Context, d Declaration) (DeclarationResponse, error) {
	d.ProtocolVersion = ProtocolVersion
	if c.session != "" {
		d.Session, d.RunNumber = c.session, c.runNumber
	}
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
	Kind string `json:"kind"`
	// Integration is always sent. The service requires the field, and resolves an empty one from the repository.
	Integration string `json:"integration"`
	Repository  string `json:"repository"`
	Branch      string `json:"branch"`
}

// PipelineIdentity identifies the stage of a pipeline that a PipelineSource reads.
type PipelineIdentity struct {
	Pipeline string `json:"pipeline"`
	Stage    string `json:"stage"`
}

type runValueRequest struct {
	ProtocolVersion int    `json:"protocolVersion"`
	Session         string `json:"session,omitempty"`
	RunNumber       int64  `json:"runNumber,omitempty"`
	Kind            string `json:"kind"`
	Identity        any    `json:"identity"`
}

// RunValue returns the value that the run recorded for an identity of the given kind ("source" or "pipeline").
func (c *Client) RunValue(ctx context.Context, kind string, identity any) (map[string]any, error) {
	var out struct {
		Value map[string]any `json:"value"`
	}
	body := runValueRequest{ProtocolVersion: ProtocolVersion, Session: c.session, RunNumber: c.runNumber, Kind: kind, Identity: identity}
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
	Session         string `json:"session,omitempty"`
	RunNumber       int64  `json:"runNumber,omitempty"`
	URN             string `json:"urn"`
	// Entry is "" for a Job and the key of the entry for a Stacks resource.
	Entry    string `json:"entry"`
	ChildURN string `json:"childUrn"`
	// Ack tells the service that the provider received the final result of the member.
	Ack bool `json:"ack,omitempty"`
	// Unchanged reports a member that did not run because its inputs equal those of the last run that ran it.
	// LastReleaseNumber is the release of that run.
	Unchanged         bool   `json:"unchanged,omitempty"`
	LastReleaseNumber *int64 `json:"lastReleaseNumber,omitempty"`
}

// MemberStatus is the answer of the member start route.
type MemberStatus struct {
	// Status is waiting, running, succeeded or failed.
	Status string `json:"status"`
	Reason string `json:"reason"`
	// Link points to the logs of a failed member.
	Link string `json:"link,omitempty"`
	// Outputs are set for succeeded, in the Pulumi JSON property encoding.
	Outputs map[string]any `json:"outputs"`
}

func (c *Client) memberStart(start MemberStart) MemberStart {
	start.ProtocolVersion = ProtocolVersion
	start.Session, start.RunNumber = c.session, c.runNumber
	return start
}

// StartMember starts a member in the run, or reports where it is. It is idempotent: it is also the poll.
func (c *Client) StartMember(ctx context.Context, start MemberStart) (MemberStatus, error) {
	var out MemberStatus
	err := c.do(ctx, http.MethodPost, c.deliveryPath()+"/members/start", nil, c.memberStart(start), &out)
	return out, err
}

// PollMember is StartMember for a wait that lasts as long as the member runs. The request has its own timeout.
func (c *Client) PollMember(ctx context.Context, start MemberStart) (MemberStatus, error) {
	var out MemberStatus
	err := c.doWithin(ctx, PollTimeout, http.MethodPost, c.deliveryPath()+"/members/start", nil, c.memberStart(start), &out)
	return out, err
}

// PollTimeout bounds one request of a wait. The wait itself has no limit.
const PollTimeout = 30 * time.Second

// AckMember tells the service that the provider received the final result of the member.
func (c *Client) AckMember(ctx context.Context, start MemberStart) error {
	start.Ack = true
	return c.do(ctx, http.MethodPost, c.deliveryPath()+"/members/start", nil, c.memberStart(start), nil)
}

// ReportUnchanged tells the service that a member did not run. The service counts it as acknowledged.
func (c *Client) ReportUnchanged(ctx context.Context, start MemberStart) error {
	start.Unchanged = true
	return c.do(ctx, http.MethodPost, c.deliveryPath()+"/members/start", nil, c.memberStart(start), nil)
}

type admitRequest struct {
	ProtocolVersion int    `json:"protocolVersion"`
	RunNumber       int64  `json:"runNumber"`
	Session         string `json:"session"`
	URN             string `json:"urn"`
}

// Admission is the answer of the stage admit route. Status is waiting, admitted, failed, canceled or superseded.
type Admission struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
	Link   string `json:"link"`
}

// AdmitStage asks whether the stage may start. It is idempotent: it is also the poll.
func (c *Client) AdmitStage(ctx context.Context, stageURN string) (Admission, error) {
	var out Admission
	body := admitRequest{ProtocolVersion: ProtocolVersion, RunNumber: c.runNumber, Session: c.session, URN: stageURN}
	err := c.doWithin(ctx, PollTimeout, http.MethodPost, c.deliveryPath()+"/stages/admit", nil, body, &out)
	return out, err
}

type interruptRequest struct {
	ProtocolVersion int    `json:"protocolVersion"`
	RunNumber       int64  `json:"runNumber"`
	Session         string `json:"session"`
}

// Interrupt tells the service that the person stopped the update. It is idempotent.
func (c *Client) Interrupt(ctx context.Context) error {
	body := interruptRequest{ProtocolVersion: ProtocolVersion, RunNumber: c.runNumber, Session: c.session}
	return c.do(ctx, http.MethodPost, c.deliveryPath()+"/run/interrupt", nil, body, nil)
}
