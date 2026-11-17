// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

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
type MemberRun struct {
	sess *session
}

type MemberRunArgs struct {
	Organization string `pulumi:"organization"`
	Member       string `pulumi:"member"`
	// Entry is the entry of a Stacks resource that this child runs. A Job has none.
	Entry       string         `pulumi:"entry,optional"`
	Declaration map[string]any `pulumi:"declaration"`
	// The inputs below are set in a local run only. Session, Run and Release name the local run: the engine runs a
	// MemberRun in another provider process than its component, so the run travels with the child. They never count
	// as a change.
	Session string `pulumi:"session,optional"`
	Run     int64  `pulumi:"run,optional"`
	Release int64  `pulumi:"release,optional"`
	// Commits are the pinned commits of the sources of the member, by source URN. DependencyRuns is the last run of
	// each dependency that the member ran against, by URN: a dependency that ran makes the member run, and a member that
	// failed after it keeps the old runs and runs again. GroupDigest is the digest of all entries of a Stacks resource.
	Commits        map[string]string `pulumi:"commits,optional"`
	DependencyRuns map[string]int64  `pulumi:"dependencyRuns,optional"`
	GroupDigest    string            `pulumi:"groupDigest,optional" provider:"secret"`
}

type MemberRunState struct {
	MemberRunArgs
	Outputs map[string]any `pulumi:"outputs"`
	// LastRun and LastRelease are the run and the release of a local run that ran the member.
	LastRun     int64 `pulumi:"lastRun,optional"`
	LastRelease int64 `pulumi:"lastRelease,optional"`
}

// memberRunResource is the Go SDK handle of a MemberRun that a component registers.
type memberRunResource struct {
	pulumi.CustomResourceState
	Outputs pulumi.MapOutput `pulumi:"outputs"`
	// LastRun and LastRelease are the run and the release that last ran the member in a local run.
	LastRun     pulumi.IntOutput `pulumi:"lastRun"`
	LastRelease pulumi.IntOutput `pulumi:"lastRelease"`
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
		return decodeOutputs(a.Member, status.Outputs)
	case "waiting", "running", "failed":
		reason := status.Reason
		if reason == "" {
			reason = "the member is " + status.Status
		}
		return nil, awaitError(reason)
	}
	return nil, fmt.Errorf("the service answered an unknown member status %q", status.Status)
}

// decodeOutputs turns the outputs of a succeeded member into the outputs of the child.
func decodeOutputs(member string, outputs map[string]any) (map[string]any, error) {
	decoded, err := stack.DeserializeProperties(outputs, config.NewErrorCrypter("no ciphertext"))
	if err != nil {
		return nil, fmt.Errorf("decoding the outputs of %s: %w", member, err)
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
}

// awaitLocal waits for the result of the member in a local run, and acknowledges it. A failed member is a plain
// error: the update fails, and the engine has no resource to wait for.
func awaitLocal(ctx context.Context, s *session, a MemberRunArgs) (map[string]any, error) {
	s.adopt(a.Session, a.Run)
	client, err := s.client(a.Organization, urn.URN(a.Member))
	if err != nil {
		return nil, err
	}
	start := cloudclient.MemberStart{URN: a.Member, Entry: a.Entry, ChildURN: string(memberChildURN(a))}
	var outputs map[string]any
	err = s.wait(ctx, func(ctx context.Context) (bool, error) {
		status, err := client.PollMember(ctx, start)
		if err != nil {
			return false, err
		}
		var result error
		switch status.Status {
		case "waiting", "running":
			return false, nil
		case "succeeded":
			outputs, result = decodeOutputs(a.Member, status.Outputs)
		case "failed":
			reason := status.Reason
			if reason == "" {
				reason = "the member failed"
			}
			result = fmt.Errorf("%s failed: %s", memberLabel(a), reason)
			if status.Link != "" {
				result = fmt.Errorf("%w (logs: %s)", result, status.Link)
			}
		default:
			return false, permanent{fmt.Errorf("the service answered an unknown member status %q", status.Status)}
		}
		if err := client.AckMember(ctx, start); err != nil && result == nil {
			result = err
		}
		if result != nil {
			return false, permanent{result}
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return outputs, nil
}

// memberLabel is the name of the member in a message: the name of the component, and the entry of a Stacks resource.
func memberLabel(a MemberRunArgs) string {
	name := urn.URN(a.Member).Name()
	if a.Entry != "" {
		name += "#" + a.Entry
	}
	return name
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

// Diff always reports a change in a registration and in a run, so that every pass goes through Update, where the
// member is started or polled. In a local run it reports a change when the inputs differ from those of the last
// run. The runs of the dependencies are inputs, so a dependency that ran in this update counts as a difference.
func (*MemberRun) Diff(_ context.Context, req infer.DiffRequest[MemberRunArgs, MemberRunState]) (infer.DiffResponse, error) {
	if req.Inputs.Session == "" {
		return infer.DiffResponse{HasChanges: true}, nil
	}
	// A member that no local run ran has no last run to compare with.
	changed := req.State.LastRun == 0 || !sameArgs(req.Inputs, req.State.MemberRunArgs)
	return infer.DiffResponse{HasChanges: changed}, nil
}

// sameArgs compares the inputs of a MemberRun with the saved ones. The engine lists the dependencies of a
// resource in no fixed order, so those lists compare as sets. Without commits the saved commits do not count.
// The session and the run never count here.
func sameArgs(a, b MemberRunArgs) bool {
	a, b = normalized(a), normalized(b)
	if len(a.Commits) == 0 {
		b.Commits = nil
	}
	return reflect.DeepEqual(a, b)
}

func normalized(a MemberRunArgs) MemberRunArgs {
	a.Session, a.Run, a.Release = "", 0, 0
	if len(a.DependencyRuns) == 0 {
		a.DependencyRuns = nil
	}
	if len(a.Commits) == 0 {
		a.Commits = nil
	}
	declaration := make(map[string]any, len(a.Declaration))
	for key, value := range a.Declaration {
		switch key {
		case "dependencies":
			declaration[key] = sortedSet(value)
		case "propertyDependencies":
			sets := map[string]any{}
			if lists, ok := value.(map[string]any); ok {
				for name, list := range lists {
					sets[name] = sortedSet(list)
				}
			}
			declaration[key] = sets
		default:
			declaration[key] = value
		}
	}
	a.Declaration = declaration
	return a
}

func sortedSet(value any) any {
	list, ok := value.([]any)
	if !ok {
		return value
	}
	out := slices.Clone(list)
	slices.SortFunc(out, func(a, b any) int { return strings.Compare(fmt.Sprint(a), fmt.Sprint(b)) })
	return slices.CompactFunc(out, func(a, b any) bool { return fmt.Sprint(a) == fmt.Sprint(b) })
}

// run executes the member: in a registration or a run stack it asks the service, in a local run it waits for the result.
func (m *MemberRun) run(ctx context.Context, a MemberRunArgs, state *MemberRunState) error {
	var err error
	if a.Session != "" {
		state.Outputs, err = awaitLocal(ctx, m.sess, a)
		if err == nil {
			state.LastRun, state.LastRelease = a.Run, a.Release
		}
		return err
	}
	state.Outputs, err = await(ctx, a)
	return err
}

func (m *MemberRun) Create(ctx context.Context, req infer.CreateRequest[MemberRunArgs]) (infer.CreateResponse[MemberRunState], error) {
	out := infer.CreateResponse[MemberRunState]{ID: req.Name, Output: MemberRunState{MemberRunArgs: req.Inputs}}
	if req.DryRun {
		return out, nil
	}
	return out, m.run(ctx, req.Inputs, &out.Output)
}

func (m *MemberRun) Update(ctx context.Context, req infer.UpdateRequest[MemberRunArgs, MemberRunState]) (infer.UpdateResponse[MemberRunState], error) {
	out := infer.UpdateResponse[MemberRunState]{Output: MemberRunState{MemberRunArgs: req.Inputs}}
	if req.DryRun {
		return out, nil
	}
	return out, m.run(ctx, req.Inputs, &out.Output)
}
