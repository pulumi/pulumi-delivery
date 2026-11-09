// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"context"
	"fmt"
	"slices"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi/pkg/v3/resource/stack"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/config"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/urn"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/pulumi/pulumi-delivery/provider/cloudclient"
)

// declaration is what a component tells the service. It has one shape in two places: the request to the
// service and the inputs of the child resource, which the preview shows.
type declaration struct {
	urn, typ             string
	dependencies         []string
	propertyDependencies map[string][]string
	// entries are the names of the entries of a Stacks resource.
	entries []string
	// secretEntries are the entries that hold a secret.
	secretEntries map[string]bool
}

func newDeclaration(req p.ConstructRequest) declaration {
	d := declaration{
		urn: string(req.Urn), typ: string(req.Urn.Type()),
		dependencies: []string{}, propertyDependencies: map[string][]string{}, secretEntries: map[string]bool{},
	}
	for _, dependency := range req.Dependencies {
		d.dependencies = append(d.dependencies, string(dependency))
	}
	for key, value := range req.Inputs.AsMap() {
		if d.typ == typeStacks && key == "stacks" && value.IsMap() {
			// The service resolves a member by component and entry, so each entry has its own key. An entry
			// gets the dependencies inside it. A dependency of the whole map that no entry shows goes to every
			// entry.
			found := map[urn.URN]bool{}
			for entry, v := range value.AsMap().AsMap() {
				d.entries = append(d.entries, entry)
				d.secretEntries[entry] = value.Secret() || hasSecret(v)
				d.add("stacks."+entry, v, nil)
				collectDependencies(v, func(dependency urn.URN) { found[dependency] = true })
			}
			var unattributed []urn.URN
			for _, dependency := range value.Dependencies() {
				if !found[dependency] {
					unattributed = append(unattributed, dependency)
				}
			}
			for _, entry := range d.entries {
				d.add("stacks."+entry, property.Value{}, unattributed)
			}
			slices.Sort(d.entries)
			continue
		}
		d.add(key, value, nil)
	}
	return d
}

// add records the dependencies inside value, and the extra ones, under key, next to those already there.
func (d declaration) add(key string, value property.Value, extra []urn.URN) {
	found := slices.Clone(d.propertyDependencies[key])
	for _, dependency := range extra {
		found = append(found, string(dependency))
	}
	collectDependencies(value, func(dependency urn.URN) { found = append(found, string(dependency)) })
	if slices.Sort(found); len(found) > 0 {
		d.propertyDependencies[key] = slices.Compact(found)
	}
}

// collectDependencies visits the dependencies of a value and of all values inside it. go-provider keeps
// the dependencies of a nested value on that value.
func collectDependencies(value property.Value, add func(urn.URN)) {
	for _, dependency := range value.Dependencies() {
		add(dependency)
	}
	switch {
	case value.IsMap():
		for _, child := range value.AsMap().AsMap() {
			collectDependencies(child, add)
		}
	case value.IsArray():
		for _, child := range value.AsArray().AsSlice() {
			collectDependencies(child, add)
		}
	}
}

// request is the body of the declaration route: the raw inputs in the Pulumi JSON property encoding, with
// secrets in plaintext and unknowns as the unknown marker.
func (d declaration) request(ctx context.Context, inputs property.Map) (cloudclient.Declaration, error) {
	encoded, err := stack.SerializeProperties(ctx, resource.ToResourcePropertyMap(inputs), config.NopEncrypter, true)
	if err != nil {
		return cloudclient.Declaration{}, fmt.Errorf("encoding the inputs of %s: %w", d.urn, err)
	}
	return cloudclient.Declaration{
		ProtocolVersion: cloudclient.ProtocolVersion, URN: d.urn, Type: d.typ, Inputs: encoded,
		Dependencies: d.dependencies, PropertyDependencies: d.propertyDependencies,
	}, nil
}

// forChild is the same declaration as inputs of the child resource. The inputs keep their secrets, so the
// events of a preview hide them.
func (d declaration) forChild(inputs pulumi.Map) pulumi.Map {
	propertyDependencies := pulumi.Map{}
	for key, dependencies := range d.propertyDependencies {
		propertyDependencies[key] = pulumi.ToStringArray(dependencies)
	}
	return pulumi.Map{
		"protocolVersion":      pulumi.Int(cloudclient.ProtocolVersion),
		"urn":                  pulumi.String(d.urn),
		"type":                 pulumi.String(d.typ),
		"inputs":               inputs,
		"dependencies":         pulumi.ToStringArray(d.dependencies),
		"propertyDependencies": propertyDependencies,
	}
}
