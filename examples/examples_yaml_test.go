//go:build go || all

package examples

import "testing"

// YAML and HCL need no SDK, so these tests run with the Go tests; the CI matrix has no entry for them.

func TestYamlPreview(t *testing.T) {
	preview(t, "yaml")
}

func TestHclPreview(t *testing.T) {
	// 0.20.1 is the first release that passes a resource in the stacks map as a resource reference.
	run(t, "hcl", "pulumi", "plugin", "install", "language", "hcl", "0.20.1")
	preview(t, "hcl")
}
