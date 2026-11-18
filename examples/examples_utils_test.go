package examples

import (
	"os/exec"
	"testing"
)

// run executes a command in dir and fails the test with its output if it exits non-zero.
func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v in %s: %v\n%s", name, args, dir, err, out)
	}
}

// preview runs `pulumi preview` on the program in dir, against a file backend in a temporary directory.
// A preview sends nothing to Pulumi Cloud, so it needs no credentials. The provider binary must be on
// PATH; `make test` puts bin/ there.
func preview(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PULUMI_BACKEND_URL", "file://"+t.TempDir())
	t.Setenv("PULUMI_CONFIG_PASSPHRASE", "")
	run(t, dir, "pulumi", "stack", "select", "--create", "test")
	run(t, dir, "pulumi", "preview", "--non-interactive")
}
