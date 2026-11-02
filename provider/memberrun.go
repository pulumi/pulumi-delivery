// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"context"
	"fmt"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/pkg/v3/resource/stack"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/config"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/urn"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/rpcutil/rpcerror"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
	"google.golang.org/grpc/codes"

	"github.com/pulumi/pulumi-delivery/provider/cloudclient"
)

// MemberRun is the internal child resource of a member. In a preview it shows the declaration of the
// member in the graph. In an update it waits: it returns an AwaitError until the run has executed the
// member, so that the engine skips the resources that use the outputs of the member.
type MemberRun struct{}

type MemberRunArgs struct {
	Organization string `pulumi:"organization"`
	Member       string `pulumi:"member"`
	// Entry is the entry of a Stacks resource that this child runs. A Job has none.
	Entry       string         `pulumi:"entry,optional"`
	Declaration map[string]any `pulumi:"declaration"`
}

type MemberRunState struct {
	MemberRunArgs
	Outputs map[string]any `pulumi:"outputs"`
}

// memberRunResource is the Go SDK handle of a MemberRun that a component registers.
type memberRunResource struct {
	pulumi.CustomResourceState
	Outputs pulumi.MapOutput `pulumi:"outputs"`
}

// awaitError tells the engine that the resource is not done yet. The message is for people. The code
// is Aborted, because the engine treats Unknown, Internal and DataLoss as possible corruption. The
// error is returned as is: wrapping it would hide the detail.
func awaitError(reason string) error {
	return rpcerror.WithDetails(rpcerror.New(codes.Aborted, reason), &pulumirpc.ErrorResourceAwaitFailed{})
}

// memberChildURN is the URN of the child, which the engine derives from the member: the child is registered
// under the component, named like it, with the entry after '#' for a Stacks entry.
func memberChildURN(a MemberRunArgs) urn.URN {
	member := urn.URN(a.Member)
	name := member.Name()
	if a.Entry != "" {
		name += "#" + a.Entry
	}
	return urn.New(member.Stack(), member.Project(), member.QualifiedType(), typeMemberRun, name)
}

// await asks the service to start the member and turns its answer into the outputs of the child, or into an
// AwaitError while the member is not done.
func await(ctx context.Context, a MemberRunArgs) (map[string]any, error) {
	client, err := clientFor(a.Organization, urn.URN(a.Member))
	if err != nil {
		return nil, err
	}
	run, err := client.GetRun(ctx)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, awaitError("registered; a run executes this member")
	}
	status, err := client.StartMember(ctx, cloudclient.MemberStart{URN: a.Member, Entry: a.Entry, ChildURN: string(memberChildURN(a))})
	if err != nil {
		return nil, err
	}
	switch status.Status {
	case "succeeded":
		decoded, err := stack.DeserializeProperties(status.Outputs, config.NewErrorCrypter("no ciphertext"))
		if err != nil {
			return nil, fmt.Errorf("decoding the outputs of %s: %w", a.Member, err)
		}
		// infer cannot carry a secret inside an any value, so a secret becomes a marker object here, and
		// revealSecrets turns it back into a secret value in the response.
		var mark func(resource.PropertyValue) (any, bool)
		mark = func(v resource.PropertyValue) (any, bool) {
			if v.IsSecret() {
				return map[string]any{resource.SigKey: resource.SecretSig, "value": v.SecretValue().Element.MapRepl(nil, mark)}, true
			}
			return nil, false
		}
		return decoded.MapRepl(nil, mark), nil
	case "waiting", "running", "failed":
		reason := status.Reason
		if reason == "" {
			reason = "the member is " + status.Status
		}
		return nil, awaitError(reason)
	}
	return nil, fmt.Errorf("the service answered an unknown member status %q", status.Status)
}

// revealSecrets turns the secret markers of await back into secret values. Only the marked outputs are secret.
func revealSecrets(v property.Value) property.Value {
	switch {
	case v.IsMap():
		m := v.AsMap().AsMap()
		if sig, ok := m[resource.SigKey]; ok && sig.IsString() && sig.AsString() == resource.SecretSig {
			return revealSecrets(m["value"]).WithSecret(true)
		}
		out := make(map[string]property.Value, len(m))
		for k, e := range m {
			out[k] = revealSecrets(e)
		}
		return property.New(property.NewMap(out))
	case v.IsArray():
		var out []property.Value
		for _, e := range v.AsArray().AsSlice() {
			out = append(out, revealSecrets(e))
		}
		return property.New(property.NewArray(out))
	}
	return v
}

func revealOutputs(props property.Map) property.Map {
	if outputs, ok := props.GetOk("outputs"); ok {
		return props.Set("outputs", revealSecrets(outputs))
	}
	return props
}

// unknownOutputsInPreview makes the outputs of a child unknown in a preview, for Create and Update: the
// outcome of a member is never known before the run executes it.
func unknownOutputsInPreview(provider p.Provider) p.Provider {
	unknown := func(props property.Map) property.Map {
		return props.Set("outputs", property.New(property.Computed))
	}
	create, update := provider.Create, provider.Update
	provider.Create = func(ctx context.Context, req p.CreateRequest) (p.CreateResponse, error) {
		resp, err := create(ctx, req)
		if err == nil && req.Urn.Type() == typeMemberRun {
			if req.DryRun {
				resp.Properties = unknown(resp.Properties)
			} else {
				resp.Properties = revealOutputs(resp.Properties)
			}
		}
		return resp, err
	}
	provider.Update = func(ctx context.Context, req p.UpdateRequest) (p.UpdateResponse, error) {
		resp, err := update(ctx, req)
		if err == nil && req.Urn.Type() == typeMemberRun {
			if req.DryRun {
				resp.Properties = unknown(resp.Properties)
			} else {
				resp.Properties = revealOutputs(resp.Properties)
			}
		}
		return resp, err
	}
	return provider
}

// Diff always reports a change, so that every pass goes through Update, where the member is started or polled.
func (*MemberRun) Diff(context.Context, infer.DiffRequest[MemberRunArgs, MemberRunState]) (infer.DiffResponse, error) {
	return infer.DiffResponse{HasChanges: true}, nil
}

func (*MemberRun) Create(ctx context.Context, req infer.CreateRequest[MemberRunArgs]) (infer.CreateResponse[MemberRunState], error) {
	out := infer.CreateResponse[MemberRunState]{ID: req.Name, Output: MemberRunState{MemberRunArgs: req.Inputs}}
	if req.DryRun {
		return out, nil
	}
	outputs, err := await(ctx, req.Inputs)
	out.Output.Outputs = outputs
	return out, err
}

func (*MemberRun) Update(ctx context.Context, req infer.UpdateRequest[MemberRunArgs, MemberRunState]) (infer.UpdateResponse[MemberRunState], error) {
	out := infer.UpdateResponse[MemberRunState]{Output: MemberRunState{MemberRunArgs: req.Inputs}}
	if req.DryRun {
		return out, nil
	}
	outputs, err := await(ctx, req.Inputs)
	out.Output.Outputs = outputs
	return out, err
}
