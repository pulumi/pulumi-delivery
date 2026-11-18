//go:build python || all

package examples

import "testing"

// Every generated Python module must byte-compile.
func TestPythonCompile(t *testing.T) {
	run(t, "..", "python3", "-c",
		"import pathlib; [compile(f.read_text(), str(f), 'exec') for f in pathlib.Path('sdk/python/pulumi_delivery').rglob('*.py')]")
}

// The program installs the built Python SDK (sdk/python/bin) into its virtualenv.
func TestPythonPreview(t *testing.T) {
	// Not pulumi install: it also tries to download the provider plugin at the SDK's version, which is not published.
	run(t, "python", "python3", "-m", "venv", "venv")
	run(t, "python", "venv/bin/python", "-m", "pip", "install", "-q", "-r", "requirements.txt")
	preview(t, "python")
}
