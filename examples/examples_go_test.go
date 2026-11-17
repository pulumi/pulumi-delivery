//go:build go || all

package examples

import "testing"

// The Go program must compile against the generated Go SDK (see compile/go/go.mod).
func TestGoCompile(t *testing.T) {
	run(t, "compile/go", "go", "vet", "./...")
}
