// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"context"

	"github.com/pulumi/pulumi-go-provider/infer"
)

type WorkflowsRuntimeArgs struct {
	// AgentPool is also the ID of this resource, so a change replaces the resource.
	AgentPool string `pulumi:"agentPool" provider:"replaceOnChanges"`
}
type WorkflowsRuntime struct{}

func validateRuntime(a WorkflowsRuntimeArgs) error { return required("agentPool", a.AgentPool) }
func (*WorkflowsRuntime) WireDependencies(f infer.FieldSelector, _ *WorkflowsRuntimeArgs, state *resourceState[WorkflowsRuntimeArgs]) {
	wireRefKnown(f, state)
}
func (*WorkflowsRuntime) Create(_ context.Context, r infer.CreateRequest[WorkflowsRuntimeArgs]) (infer.CreateResponse[resourceState[WorkflowsRuntimeArgs]], error) {
	out, err := createResource(r.Inputs.AgentPool, "workflows-runtime", r.Inputs, validateRuntime)
	out.Output.Ref = r.Inputs.AgentPool
	return out, err
}
func (*WorkflowsRuntime) Update(_ context.Context, r infer.UpdateRequest[WorkflowsRuntimeArgs, resourceState[WorkflowsRuntimeArgs]]) (infer.UpdateResponse[resourceState[WorkflowsRuntimeArgs]], error) {
	out, err := updateResource(r.Inputs.AgentPool, "workflows-runtime", r.Inputs, validateRuntime)
	out.Output.Ref = r.Inputs.AgentPool
	return out, err
}
