// Copyright 2026, Pulumi Corporation. All rights reserved.

// Command pulumi-resource-marker is a tiny provider for the U1 test (u1_test.go). Its File resource writes
// its value to the file in MARKER_FILE, so that the test can see whether the resource was created.
package main

import (
	"context"
	"fmt"
	"os"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

type File struct{}
type FileArgs struct {
	Value string `pulumi:"value"`
}
type FileState struct {
	FileArgs
}

func (*File) Create(_ context.Context, req infer.CreateRequest[FileArgs]) (infer.CreateResponse[FileState], error) {
	if !req.DryRun {
		if err := os.WriteFile(os.Getenv("MARKER_FILE"), []byte(req.Inputs.Value), 0o600); err != nil {
			return infer.CreateResponse[FileState]{}, err
		}
	}
	return infer.CreateResponse[FileState]{ID: req.Name, Output: FileState{FileArgs: req.Inputs}}, nil
}

func main() {
	provider, err := infer.NewProviderBuilder().
		WithNamespace("marker").
		WithModuleMap(map[tokens.ModuleName]tokens.ModuleName{"marker": "index"}).
		WithResources(infer.Resource(&File{})).
		Build()
	if err == nil {
		err = p.RunProvider(context.Background(), "marker", "0.0.1", provider)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
