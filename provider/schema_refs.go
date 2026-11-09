// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	p "github.com/pulumi/pulumi-go-provider"
)

func ref(token string) map[string]any { return map[string]any{"$ref": "#/resources/" + token} }

func array(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

// sourceRef is a reference to one of the source resources.
func sourceRef() map[string]any {
	oneOf := make([]map[string]any, len(sourceTypes))
	for i, token := range sourceTypes {
		oneOf[i] = ref(token)
	}
	return map[string]any{"oneOf": oneOf}
}

// referencePatches replaces the schema type of each input that refers to a resource. The Go field has
// the type any, because the Go SDK decodes a reference into it (components.go). The path leads from
// the top of the schema to the property.
var referencePatches = []struct {
	path   []string
	target func() map[string]any
}{
	{[]string{"types", "delivery:index:ProgramSource", "properties", "source"}, sourceRef},
	{[]string{"resources", typeStage, "inputProperties", "pipeline"}, func() map[string]any { return ref(typePipeline) }},
	{[]string{"resources", typeStage, "inputProperties", "from"}, func() map[string]any { return array(ref(typeStage)) }},
	{[]string{"resources", typeJob, "inputProperties", "stage"}, func() map[string]any { return ref(typeStage) }},
	{[]string{"resources", typeJob, "inputProperties", "source"}, sourceRef},
	{[]string{"resources", typeStacks, "inputProperties", "stage"}, func() map[string]any { return ref(typeStage) }},
	{[]string{"resources", typeArtifact, "inputProperties", "stage"}, func() map[string]any { return ref(typeStage) }},
	{[]string{"types", "delivery:index:StacksEntry", "properties", "source"}, sourceRef},
	{[]string{"resources", "delivery:index:GitHubSource", "inputProperties", "trigger"}, sourceTrigger},
	{[]string{"resources", "delivery:index:GitLabSource", "inputProperties", "trigger"}, sourceTrigger},
	{[]string{"resources", "delivery:index:BitbucketSource", "inputProperties", "trigger"}, sourceTrigger},
	{[]string{"resources", "delivery:index:AzureDevOpsSource", "inputProperties", "trigger"}, sourceTrigger},
	{[]string{"resources", typeJob, "inputProperties", "runtime"}, func() map[string]any { return ref("delivery:index:WorkflowsRuntime") }},
	{[]string{"types", "delivery:index:StacksEntry", "properties", "runtime"}, func() map[string]any { return ref("delivery:index:WorkflowsRuntime") }},
}

// sourceTrigger is the schema type of the trigger of a source: true, or the paths that limit it.
func sourceTrigger() map[string]any {
	return map[string]any{"oneOf": []map[string]any{{"type": "boolean"}, {"$ref": "#/types/delivery:index:SourceTrigger"}}}
}

// withReferences rewrites the schema that infer derives from the Go types, so that the inputs which
// refer to resources have the type of the resource.
func withReferences(
	getSchema func(context.Context, p.GetSchemaRequest) (p.GetSchemaResponse, error),
) func(context.Context, p.GetSchemaRequest) (p.GetSchemaResponse, error) {
	return func(ctx context.Context, req p.GetSchemaRequest) (p.GetSchemaResponse, error) {
		resp, err := getSchema(ctx, req)
		if err != nil || resp.Schema == "" {
			return resp, err
		}
		dec := json.NewDecoder(bytes.NewReader([]byte(resp.Schema)))
		dec.UseNumber()
		var doc map[string]any
		if err := dec.Decode(&doc); err != nil {
			return p.GetSchemaResponse{}, fmt.Errorf("decoding the schema: %w", err)
		}
		for _, patch := range referencePatches {
			if err := applyReferencePatch(doc, patch.path, patch.target()); err != nil {
				return p.GetSchemaResponse{}, err
			}
		}
		unplainInputs(doc)
		hideInternals(doc)
		out, err := json.Marshal(doc)
		if err != nil {
			return p.GetSchemaResponse{}, err
		}
		resp.Schema = string(out)
		return resp, nil
	}
}

func applyReferencePatch(doc map[string]any, path []string, target map[string]any) error {
	node := doc
	for i, key := range path[:len(path)-1] {
		next, ok := node[key].(map[string]any)
		if !ok {
			return fmt.Errorf("the schema has no %v", path[:i+1])
		}
		node = next
	}
	last := path[len(path)-1]
	old, ok := node[last].(map[string]any)
	if !ok {
		return fmt.Errorf("the schema has no %v", path)
	}
	if description, ok := old["description"]; ok {
		target["description"] = description
	}
	node[last] = target
	return nil
}

// unplainInputs makes the inputs of the components, and the types they use, accept outputs. infer marks
// the properties of the plain Go types as plain.
func unplainInputs(doc map[string]any) {
	var strip func(node any)
	strip = func(node any) {
		switch node := node.(type) {
		case map[string]any:
			delete(node, "plain")
			for _, child := range node {
				strip(child)
			}
		case []any:
			for _, child := range node {
				strip(child)
			}
		}
	}
	strip(doc["types"])
	resources, _ := doc["resources"].(map[string]any)
	for _, resource := range resources {
		if resource, ok := resource.(map[string]any); ok && resource["isComponent"] == true {
			strip(resource["inputProperties"])
		}
	}
}

// hideInternals removes what is not a public contract: the internal child resource, which only the Construct
// of a component registers, and the outputs that carry its URN.
func hideInternals(doc map[string]any) {
	resources, _ := doc["resources"].(map[string]any)
	delete(resources, typeMemberRun)
	for typ, name := range map[string]string{typeJob: "childUrn", typeArtifact: "childUrn", typeStacks: "childUrns"} {
		component, _ := resources[typ].(map[string]any)
		if properties, ok := component["properties"].(map[string]any); ok {
			delete(properties, name)
		}
		if required, ok := component["required"].([]any); ok {
			component["required"] = slices.DeleteFunc(required, func(r any) bool { return r == name })
		}
	}
}
