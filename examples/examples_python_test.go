//go:build python || all

package examples

import "testing"

// Every generated Python module must byte-compile.
func TestPythonCompile(t *testing.T) {
	run(t, "..", "python3", "-c",
		"import pathlib; [compile(f.read_text(), str(f), 'exec') for f in pathlib.Path('sdk/python/pulumi_delivery').rglob('*.py')]")
}
