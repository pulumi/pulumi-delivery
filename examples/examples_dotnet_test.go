//go:build dotnet || all

package examples

import "testing"

// The C# program must build against the generated .NET SDK.
func TestDotnetCompile(t *testing.T) {
	run(t, "compile/dotnet", "dotnet", "build", "--nologo", "-v", "q")
}

func TestDotnetPreview(t *testing.T) {
	preview(t, "compile/dotnet")
}
