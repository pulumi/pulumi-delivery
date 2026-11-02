// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWaitingJobSkipsItsConsumers is the gate for turning on the Delivery flag (bug U1).
//
// A real update registers a pipeline with a Job that waits. A resource of another provider (testdata/marker) uses an output of the Job
// inside an apply. The engine must skip the resource, and the apply must not run. With a CLI that has
// bug U1, the output of the remote component becomes a known empty value, the apply runs with it, and
// the test fails.
//
// The fix is upstream pulumi/pulumi#25291 (with #25278), released in v3.272.0. It changes the engine, the Go SDK
// of the calling program (testdata/u1/go.mod) and the Go SDK of the provider (provider/go.mod). The test needs a
// CLI v3.272.0 or later. Set DELIVERY_U1_PULUMI to the path of that CLI to run it. The skip can go now that the
// fix is released.
//
// Every case asserts the exact behavior of the fix: the Construct response of the component holds an unknown
// output that depends on the children, the program never runs the apply, the engine never sees the consumer, and
// the summary counts the waiting child as awaiting.
func TestWaitingJobSkipsItsConsumers(t *testing.T) {
	runU1(t, "", "+ 5 created", "Job::build", "Job$delivery:index:MemberRun::build")
}

// TestWaitingJobSkipsItsConsumersInNodejs is the same gate for a program in Node.js. Its SDK resolved an unknown
// output as undefined during an update. Set DELIVERY_U1_NODEJS_SDK to the tarball of the Node.js SDK built from the
// same commit (npm pack of sdk/nodejs/bin after make build_package), next to DELIVERY_U1_PULUMI.
func TestWaitingJobSkipsItsConsumersInNodejs(t *testing.T) {
	runU1(t, "nodejs", "+ 5 created", "Job::build", "Job$delivery:index:MemberRun::build")
}

// TestWaitingStacksEntrySkipsItsConsumers is the same gate for a Stacks resource whose only entry waits.
func TestWaitingStacksEntrySkipsItsConsumers(t *testing.T) {
	runU1(t, "single", "+ 5 created", "Stacks::svc", "Stacks$delivery:index:MemberRun::svc#api")
}

// TestStacksGroupWaitsForEveryEntry runs a two-entry group in a run, where the entry "api" succeeded and "worker"
// waits, and a consumer of the output of entry "api". The consumer waits only because "worker" waits: the entry "api"
// is created and its output has a value, but the output of the group is unknown and depends on both children, so
// the program never runs the apply and the engine never sees the consumer.
func TestStacksGroupWaitsForEveryEntry(t *testing.T) {
	u := setupU1(t, "group")
	u.service.run = true
	answer(u.service, "api", http.StatusOK, `{"status":"succeeded","outputs":{"pool":"blue"}}`)
	out := u.up(t, 10)
	u.requireSkipped(t)
	u.requireAwaiting(t, out, "+ 6 created")
	u.requireUnknownOutputs(t, "Stacks::svc", "Stacks$delivery:index:MemberRun::svc#api", "Stacks$delivery:index:MemberRun::svc#worker")
	assert.Contains(t, out, "MemberRun svc#api created", "the entry api is created")
	assert.Len(t, u.service.memberStarts, 2, "both entries are started")
}

// TestSucceededMemberKeepsGoingThroughUpdate runs a second pass after the last entry succeeded. The entry "api"
// succeeded in the first pass and is not skipped in the second: it goes through Update again, and the consumer is
// created once the group is done.
func TestSucceededMemberKeepsGoingThroughUpdate(t *testing.T) {
	u := setupU1(t, "group")
	u.service.run = true
	answer(u.service, "api", http.StatusOK, `{"status":"succeeded","outputs":{"pool":"blue"}}`)
	u.up(t, 10)
	u.requireSkipped(t)

	answer(u.service, "worker", http.StatusOK, `{"status":"succeeded","outputs":{}}`)
	u.service.memberStarts = nil
	u.up(t, 0)
	got, err := os.ReadFile(u.created)
	require.NoError(t, err)
	assert.Equal(t, "blue", string(got))
	var entries []string
	for _, start := range u.service.memberStarts {
		entries = append(entries, start["entry"].(string))
	}
	assert.ElementsMatch(t, []string{"api", "worker"}, entries, "the second pass starts the succeeded entry again")
}

// TestWaitingArtifact runs an Artifact with a known secret value in a registration, in a preview, in a run where the
// artifact waits, and in a later pass where it succeeded. Every case must pass without "malformed secret": the
// SDK marks the outputs of a waiting member secret, and the engine rejects a secret without a value.
//
// The input and the output of an Artifact are both named value. Without the fix, the engine stripped the unknown
// output from the registration response, and the Go SDK of the program resolved a missing output from the input
// with the same name (resourceState.resolve in sdk/go/pulumi/context.go). The consumer then ran with the known
// input value of the waiting artifact. With the fix the engine sends the unknown output and the SDK keeps it, so
// the consumer waits. No rename of the output is needed, and the checks below fail with the old CLI and SDK.
func TestWaitingArtifact(t *testing.T) {
	u := setupU1(t, "artifact")
	code, out := u.pulumi("preview")
	assert.Zero(t, code, out)
	assert.NotContains(t, out, "malformed secret")

	out = u.up(t, 10)
	u.requireSkipped(t)
	u.requireAwaiting(t, out, "+ 5 created")
	u.requireUnknownOutputs(t, "Artifact::endpoint", "Artifact$delivery:index:MemberRun::endpoint")

	u.service.run = true
	out = u.up(t, 10)
	u.requireSkipped(t)
	u.requireAwaiting(t, out, "5 unchanged")
	require.NotEmpty(t, u.service.memberStarts)
	assert.Equal(t, "", u.service.memberStarts[0]["entry"])

	answer(u.service, "", http.StatusOK, `{"status":"succeeded","outputs":{"value":"https://example.com"}}`)
	u.up(t, 0)
	got, err := os.ReadFile(u.created)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com", string(got))
}

// runU1 runs the program of testdata/u1 in a registration. The case "" is the Job of the original test.
func runU1(t *testing.T, kase string, summary string, component string, children ...string) {
	u := setupU1(t, kase)
	out := u.up(t, 10)
	u.requireSkipped(t)
	u.requireAwaiting(t, out, summary)
	u.requireUnknownOutputs(t, component, children...)
}

// u1 is a project, a stack and a fake service for the engine tests.
type u1 struct {
	service         *fakeService
	pulumi          func(args ...string) (int, string)
	marker, created string
	constructLog    string
}

func (u u1) up(t *testing.T, wantCode int) string {
	t.Helper()
	code, out := u.pulumi("up", "--yes", "--skip-preview")
	logged, _ := os.ReadFile(u.constructLog)
	t.Logf("Construct responses of the provider:\n%s", logged)
	assert.Equal(t, wantCode, code, "unexpected exit code; output:\n%s", out)
	assert.NotContains(t, out, "malformed secret")
	return out
}

// requireAwaiting checks the summary of an update with one awaiting child: the engine never registered the
// consumer, so it is absent, and summary is the line for the other resources.
func (u u1) requireAwaiting(t *testing.T, out, summary string) {
	t.Helper()
	assert.NotContains(t, out, "marker:index:File", "the engine must not see the consumer")
	assert.Contains(t, out, "warning: update has awaiting resources")
	assert.Contains(t, out, summary)
	assert.Contains(t, out, "1 awaiting")
}

// requireUnknownOutputs checks that the last Construct response of the component is an unknown output that depends
// on every child, whichever way the program uses it.
func (u u1) requireUnknownOutputs(t *testing.T, component string, children ...string) {
	t.Helper()
	logged, err := os.ReadFile(u.constructLog)
	require.NoError(t, err)
	lines := strings.Split(string(logged), "\n")
	var state string
	for i, line := range lines {
		if strings.HasSuffix(line, "delivery:index:"+component) && i+1 < len(lines) {
			state = lines[i+1]
		}
	}
	require.NotEmpty(t, state, "no Construct response for %s in:\n%s", component, logged)
	assert.Contains(t, state, "property.Computed")
	for _, child := range children {
		assert.Contains(t, state, "delivery:index:"+child)
	}
}

// requireSkipped checks that the update did not run the apply and did not create the consumer.
func (u u1) requireSkipped(t *testing.T) {
	t.Helper()
	got, err := os.ReadFile(u.marker)
	assert.ErrorIs(t, err, os.ErrNotExist, "the apply ran with value %q", got)
	got, err = os.ReadFile(u.created)
	assert.ErrorIs(t, err, os.ErrNotExist, "the consumer was created with value %q", got)
}

// nodejsProgram copies testdata/u1node into dir and installs the Node.js SDK of DELIVERY_U1_NODEJS_SDK in it.
func nodejsProgram(t *testing.T, dir string) string {
	sdk := os.Getenv("DELIVERY_U1_NODEJS_SDK")
	if sdk == "" {
		t.Skip("U1: set DELIVERY_U1_NODEJS_SDK to the tarball of the Node.js SDK of the same upstream commit as DELIVERY_U1_PULUMI.")
	}
	program := filepath.Join(dir, "nodejs-program")
	require.NoError(t, os.Mkdir(program, 0o755))
	for _, name := range []string{"index.js", "package.json"} {
		content, err := os.ReadFile(filepath.Join("testdata", "u1node", name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(program, name), content, 0o600))
	}
	install := exec.CommandContext(t.Context(), "npm", "install", "--no-audit", "--no-fund", sdk)
	install.Dir = program
	installed, err := install.CombinedOutput()
	require.NoError(t, err, string(installed))
	return program
}

func setupU1(t *testing.T, kase string) u1 {
	cli := os.Getenv("DELIVERY_U1_PULUMI")
	if cli == "" {
		t.Skip("U1: remote component outputs of a waiting child become known empty values. " +
			"Set DELIVERY_U1_PULUMI to a Pulumi CLI v3.272.0 or later (pulumi/pulumi#25291).")
	}
	service := newFakeService(t) // sets PULUMI_API and PULUMI_ACCESS_TOKEN, and answers 404 to GET run

	dir := t.TempDir()
	plugin := filepath.Join(dir, "plugin")
	for name, pkg := range map[string]string{"delivery": ".", "marker": "./testdata/marker"} {
		build := exec.Command("go", "build", "-o", filepath.Join(plugin, name, "pulumi-resource-"+name), pkg)
		built, err := build.CombinedOutput()
		require.NoError(t, err, string(built))
	}

	program, err := filepath.Abs(filepath.Join("testdata", "u1"))
	require.NoError(t, err)
	runtime := "go"
	if kase == "nodejs" {
		program = nodejsProgram(t, dir)
		runtime = "nodejs"
		kase = ""
	}
	project := filepath.Join(dir, "project")
	require.NoError(t, os.Mkdir(project, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(project, "Pulumi.yaml"), fmt.Appendf(nil, `name: u1
runtime: %s
main: %s
plugins:
  providers:
    - name: delivery
      path: %s
    - name: marker
      path: %s
`, runtime, program, filepath.Join(plugin, "delivery"), filepath.Join(plugin, "marker")), 0o600))

	marker := filepath.Join(dir, "apply-marker")
	created := filepath.Join(dir, "consumer-created")
	pulumi := func(args ...string) (int, string) {
		cmd := exec.CommandContext(t.Context(), cli, args...)
		cmd.Dir = project
		cmd.Env = append(os.Environ(),
			"PULUMI_HOME="+filepath.Join(dir, "home"),
			"PULUMI_BACKEND_URL=file://"+filepath.Join(dir, "state"),
			"PULUMI_CONFIG_PASSPHRASE=",
			"PULUMI_SKIP_UPDATE_CHECK=true",
			"DELIVERY_CONSTRUCT_LOG="+filepath.Join(dir, "construct.log"),
			"U1_CASE="+kase, "U1_APPLY_MARKER="+marker, "MARKER_FILE="+created)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return 0, string(out)
		}
		exitErr, ok := err.(*exec.ExitError)
		require.True(t, ok, "unexpected error %v", err)
		return exitErr.ExitCode(), string(out)
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "state"), 0o755))
	code, out := pulumi("login")
	require.Zero(t, code, out)
	code, out = pulumi("stack", "init", "test", "--secrets-provider", "passphrase")
	require.Zero(t, code, out)

	return u1{service: service, pulumi: pulumi, marker: marker, created: created, constructLog: filepath.Join(dir, "construct.log")}
}
