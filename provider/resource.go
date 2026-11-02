// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
)

type resourceState[T any] struct {
	Inputs T      `pulumi:"inputs"`
	Ref    string `pulumi:"ref"`
}

func createResource[T any](id, kind string, inputs T, validate func(T) error) (infer.CreateResponse[resourceState[T]], error) {
	if err := validate(inputs); err != nil {
		return infer.CreateResponse[resourceState[T]]{}, err
	}
	return infer.CreateResponse[resourceState[T]]{ID: id, Output: resourceState[T]{Inputs: inputs, Ref: kind + ":" + id}}, nil
}

func updateResource[T any](id, kind string, inputs T, validate func(T) error) (infer.UpdateResponse[resourceState[T]], error) {
	if err := validate(inputs); err != nil {
		return infer.UpdateResponse[resourceState[T]]{}, err
	}
	return infer.UpdateResponse[resourceState[T]]{Output: resourceState[T]{Inputs: inputs, Ref: kind + ":" + id}}, nil
}

// wireRefKnown marks the ref output as known: it is a pure function of the kind and the name.
func wireRefKnown[T any](f infer.FieldSelector, state *resourceState[T]) {
	f.OutputField(&state.Ref).AlwaysKnown()
}

func required(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}

// unknown reports whether the input at path is unknown.
func unknown(inputs property.Map, path ...string) bool {
	value, ok := inputs.GetOk(path[0])
	for i := 1; ok; i++ {
		if value.IsComputed() {
			return true
		}
		if i == len(path) || !value.IsMap() {
			return false
		}
		value, ok = value.AsMap().GetOk(path[i])
	}
	return false
}

// requiredUnlessUnknown is required, except for an input that is still unknown (a preview).
func requiredUnlessUnknown(inputs property.Map, value string, path ...string) error {
	if unknown(inputs, path...) {
		return nil
	}
	return required(strings.Join(path, "."), value)
}
