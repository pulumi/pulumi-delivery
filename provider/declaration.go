// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"time"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi/pkg/v3/resource/stack"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/config"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/urn"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/pulumi/pulumi-delivery/provider/cloudclient"
)

// declarationMode is "register" when PULUMI_DELIVERY_REGISTER is true, and "local" otherwise. The
// service decides the mode of the update: this is the mode that the provider asks for. It is read once per provider process.
var declarationMode = modeFromEnvironment()

func modeFromEnvironment() string {
	if mode := registerModeFromEnvironment(); mode != "" {
		return mode
	}
	return modeLocal
}

func registerModeFromEnvironment() string {
	if register, err := strconv.ParseBool(os.Getenv("PULUMI_DELIVERY_REGISTER")); err == nil && register {
		return "register"
	}
	return ""
}

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
	encoded, err := stack.SerializeProperties(ctx, resource.ToResourcePropertyMap(timeoutsInSeconds(d.typ, inputs)), config.NopEncrypter, true)
	if err != nil {
		return cloudclient.Declaration{}, fmt.Errorf("encoding the inputs of %s: %w", d.urn, err)
	}
	return cloudclient.Declaration{
		ProtocolVersion: cloudclient.ProtocolVersion, Mode: declarationMode, URN: d.urn, Type: d.typ, Inputs: encoded,
		Dependencies: d.dependencies, PropertyDependencies: d.propertyDependencies,
	}, nil
}

// timeoutsInSeconds replaces the timeout of a Job, and of each entry of a Stacks resource, with the number of seconds
// that the service reads. Only the request changes: the component keeps the duration the user wrote.
func timeoutsInSeconds(typ string, inputs property.Map) property.Map {
	switch typ {
	case typeJob:
		return withSeconds(inputs)
	case typeStacks:
		stacks, ok := inputs.GetOk("stacks")
		if !ok || !stacks.IsMap() {
			return inputs
		}
		entries := map[string]property.Value{}
		for name, entry := range stacks.AsMap().AsMap() {
			if entry.IsMap() {
				entry = property.New(withSeconds(entry.AsMap())).WithSecret(entry.Secret()).WithDependencies(entry.Dependencies())
			}
			entries[name] = entry
		}
		return inputs.Set("stacks", property.New(property.NewMap(entries)).WithSecret(stacks.Secret()).WithDependencies(stacks.Dependencies()))
	}
	return inputs
}

func withSeconds(member property.Map) property.Map {
	timeout, ok := member.GetOk("timeout")
	if !ok || !timeout.IsString() || timeout.AsString() == "" {
		return member
	}
	d, err := time.ParseDuration(timeout.AsString())
	if err != nil {
		return member
	}
	return member.Set("timeout", property.New(d.Seconds()).WithSecret(timeout.Secret()).WithDependencies(timeout.Dependencies()))
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

// digest identifies the inputs of all entries of a Stacks resource. A group is one unit, so every entry gets it as an
// input, and all entries change together.
func (d declaration) digest(ctx context.Context, inputs property.Map) (string, error) {
	request, err := d.request(ctx, inputs)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(request.Inputs)
	if err != nil {
		return "", fmt.Errorf("encoding the inputs of %s: %w", d.urn, err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
