// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file run a real `pulumi up` against a fake of the Delivery routes of a local run
// (local_service_test.go). They need the same CLI as the U1 tests: set DELIVERY_U1_PULUMI, and for the Node.js
// programs DELIVERY_U1_NODEJS_SDK.

var (
	buildOnce sync.Once
	buildDir  string
	buildErr  error
	nodeOnce  sync.Once
	nodeDir   string
	nodeErr   error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

// builds builds the plugins and the Go program once for all tests.
func builds(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		buildDir, buildErr = os.MkdirTemp("", "delivery-local")
		if buildErr != nil {
			return
		}
		for name, pkg := range map[string]string{
			"plugin/delivery/pulumi-resource-delivery": ".",
			"plugin/marker/pulumi-resource-marker":     "./testdata/marker",
			"localgo":                                  "testdata/localgo",
		} {
			build := exec.Command("go", "build", "-o", filepath.Join(buildDir, name), ".")
			build.Dir = pkg
			if out, err := build.CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("building %s: %w\n%s", pkg, err, out)
				return
			}
		}
	})
	require.NoError(t, buildErr)
	return buildDir
}

// nodeProgramDir installs the Node.js SDK next to the program once.
func nodeProgramDir(t *testing.T) string {
	t.Helper()
	sdk := os.Getenv("DELIVERY_U1_NODEJS_SDK")
	if sdk == "" {
		t.Skip("set DELIVERY_U1_NODEJS_SDK to the tarball of the Node.js SDK of the same commit as DELIVERY_U1_PULUMI.")
	}
	dir := builds(t)
	nodeOnce.Do(func() {
		program := filepath.Join(dir, "localnode")
		if nodeErr = os.Mkdir(program, 0o755); nodeErr != nil {
			return
		}
		for _, name := range []string{"index.js", "package.json"} {
			content, err := os.ReadFile(filepath.Join("testdata", "localnode", name))
			if err != nil {
				nodeErr = err
				return
			}
			if nodeErr = os.WriteFile(filepath.Join(program, name), content, 0o600); nodeErr != nil {
				return
			}
		}
		install := exec.Command("npm", "install", "--no-audit", "--no-fund", sdk)
		install.Dir = program
		if out, err := install.CombinedOutput(); err != nil {
			nodeErr = fmt.Errorf("npm install: %w\n%s", err, out)
			return
		}
		nodeDir = program
	})
	require.NoError(t, nodeErr)
	return nodeDir
}

// localEnv is a project and a stack with the fake service.
type localEnv struct {
	t        *testing.T
	svc      *localService
	cli      string
	project  string
	home     string
	state    string
	observed string
	extra    []string
}

func setupLocal(t *testing.T, lang string, svc *localService, extra ...string) *localEnv {
	t.Helper()
	cli := os.Getenv("DELIVERY_U1_PULUMI")
	if cli == "" {
		t.Skip("set DELIVERY_U1_PULUMI to a Pulumi CLI v3.272.0 or later.")
	}
	plugins := builds(t)
	dir := t.TempDir()
	runtime := "name: go\n  options:\n    binary: " + filepath.Join(plugins, "localgo")
	main := ""
	if lang == "nodejs" {
		runtime = "name: nodejs"
		main = "main: " + nodeProgramDir(t) + "\n"
	}
	project := filepath.Join(dir, "project")
	require.NoError(t, os.Mkdir(project, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(project, "Pulumi.yaml"), fmt.Appendf(nil, `name: local
runtime:
  %s
%splugins:
  providers:
    - name: delivery
      path: %s
    - name: marker
      path: %s
`, runtime, main, filepath.Join(plugins, "plugin", "delivery"), filepath.Join(plugins, "plugin", "marker")), 0o600))
	e := &localEnv{
		t: t, svc: svc, cli: cli, project: project, home: filepath.Join(dir, "home"), state: filepath.Join(dir, "state"),
		observed: filepath.Join(dir, "observed"), extra: extra,
	}
	require.NoError(t, os.MkdirAll(e.state, 0o755))
	code, out := e.pulumi(nil, "login")
	require.Zero(t, code, out)
	code, out = e.pulumi(nil, "stack", "init", "test", "--secrets-provider", "passphrase")
	require.Zero(t, code, out)
	return e
}

func (e *localEnv) command(env []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(e.t.Context(), e.cli, args...)
	cmd.Dir = e.project
	cmd.Env = append(os.Environ(),
		"PULUMI_HOME="+e.home, "PULUMI_BACKEND_URL=file://"+e.state, "PULUMI_CONFIG_PASSPHRASE=",
		"PULUMI_SKIP_UPDATE_CHECK=true", "PULUMI_API="+e.svc.URL, "PULUMI_ACCESS_TOKEN=token",
		"MARKER_FILE="+filepath.Join(filepath.Dir(e.project), "consumer"), "LOCAL_OBSERVED="+e.observed)
	cmd.Env = append(cmd.Env, "PATH="+filepath.Dir(e.cli)+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Env = append(cmd.Env, e.extra...)
	cmd.Env = append(cmd.Env, env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

func exitCode(t *testing.T, err error) int {
	if err == nil {
		return 0
	}
	exitErr, ok := err.(*exec.ExitError)
	require.True(t, ok, "unexpected error %v", err)
	return exitErr.ExitCode()
}

func (e *localEnv) pulumi(env []string, args ...string) (int, string) {
	out, err := e.command(env, args...).CombinedOutput()
	return exitCode(e.t, err), string(out)
}

// up runs `pulumi up` without the preview. The environment sets the program: see testdata/localgo.
func (e *localEnv) up(parallel string, env ...string) (int, string) {
	args := []string{"up", "--yes", "--skip-preview"}
	if parallel != "" {
		args = append(args, "--parallel", parallel)
	}
	return e.pulumi(env, args...)
}

func (e *localEnv) requireClean() {
	e.t.Helper()
	assert.Empty(e.t, e.svc.violationList(), "calls with a wrong session or run")
}

// diagnostics is the part of the output of an update that lists each message once.
func diagnostics(out string) string {
	_, after, _ := strings.Cut(out, "Diagnostics:")
	return after
}

var localLangs = []string{"go", "nodejs"}

// eachLocal runs f for each language and parallelism of a core case.
func eachLocal(t *testing.T, f func(t *testing.T, lang, parallel string)) {
	for _, lang := range localLangs {
		for _, parallel := range []string{"1", "2", ""} {
			name := lang + "/parallel-" + parallel
			if parallel == "" {
				name = lang + "/parallel-default"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				f(t, lang, parallel)
			})
		}
	}
}

func TestLocalTwoStagesTheSecondWaitsForTheFirst(t *testing.T) {
	eachLocal(t, func(t *testing.T, lang, parallel string) {
		svc := newLocalService(t)
		svc.admit["prod"] = func(polls int) (string, string) {
			if slices.Contains(svc.events, "ack a") {
				return "admitted", ""
			}
			return "waiting", "Waiting for stage dev."
		}
		e := setupLocal(t, lang, svc, "LOCAL_TWO_STAGES=1")

		code, out := e.up(parallel)
		require.Zero(t, code, out)
		events := svc.take()

		assert.Contains(t, out, "Run 7 (release 3): https://example.test/run/7")
		assert.Equal(t, 1, strings.Count(diagnostics(out), "Run 7 (release 3)"), "the run link shows once")
		assert.ElementsMatch(t, []string{"a:succeeded", "b:succeeded"}, finals(events))
		assert.Less(t, indexOf(events, "ack a"), indexOf(events, "admit prod:admitted"), "prod waits for the ack of a")
		assert.Less(t, indexOf(events, "admit prod:admitted"), indexOf(events, "final b:succeeded"), "b starts after prod is admitted")
		assert.Equal(t, []string{"a", "b"}, eventsWith(events, "ack "))
		assert.Zero(t, svc.interruptCount())
		assert.Zero(t, svc.runLookups, "a local run never asks for the run of a run stack")
		e.requireClean()

		first := svc.declared()[0]
		assert.Equal(t, "local", first["mode"])
		assert.NotContains(t, first, "runNumber")
		for _, d := range svc.declared() {
			assert.Equal(t, first["session"], d["session"])
		}
		observed, err := os.ReadFile(e.observed)
		require.NoError(t, err)
		assert.Contains(t, string(observed), "a-1")
		assert.Contains(t, string(observed), "b-1")
	})
}

func TestLocalGateAdmitsLater(t *testing.T) {
	for _, lang := range localLangs {
		t.Run(lang, func(t *testing.T) {
			t.Parallel()
			svc := newLocalService(t)
			svc.admit["prod"] = func(polls int) (string, string) {
				if polls < 3 {
					return "waiting", "Waiting for approval."
				}
				return "admitted", ""
			}
			e := setupLocal(t, lang, svc, "LOCAL_TWO_STAGES=1")

			code, out := e.up("")
			require.Zero(t, code, out)
			events := svc.take()

			assert.Equal(t, 1, strings.Count(diagnostics(out), "Stage prod waits: Waiting for approval. (https://example.test/stage/prod)"), "the reason shows once:\n%s", out)
			assert.Less(t, indexOf(events, "admit prod:admitted"), indexOf(events, "final b:succeeded"))
			assert.Equal(t, 3, svc.admitPolls["prod"])
			e.requireClean()
		})
	}
}

func TestLocalAStageThatEndsWithoutAdmissionFailsTheUp(t *testing.T) {
	for status, reason := range map[string]string{
		"superseded": "Superseded at prod by release 9.", "canceled": "The run was canceled.", "failed": "The run failed.",
	} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			svc := newLocalService(t)
			svc.admit["prod"] = func(int) (string, string) { return status, reason }
			e := setupLocal(t, "go", svc, "LOCAL_TWO_STAGES=1")

			code, out := e.up("")
			assert.Equal(t, 1, code, out)
			assert.Contains(t, out, reason+" (https://example.test/stage/prod)")
			assert.NotContains(t, out, "awaiting")
			assert.NotContains(t, finals(svc.take()), "b:succeeded")
			assert.Zero(t, svc.interruptCount())
		})
	}
}

func TestLocalFailingMemberIsAPlainError(t *testing.T) {
	for _, lang := range localLangs {
		t.Run(lang, func(t *testing.T) {
			t.Parallel()
			svc := newLocalService(t)
			svc.members["a"] = &memberScript{fail: true, reason: "exit status 2", link: "https://example.test/logs/a"}
			e := setupLocal(t, lang, svc)

			code, out := e.up("")
			assert.Equal(t, 1, code, "a plain error exits with 1, not with the exit code of an awaiting update:\n%s", out)
			assert.Contains(t, out, "a failed: exit status 2 (logs: https://example.test/logs/a)")
			assert.NotContains(t, out, "awaiting")
			assert.Contains(t, svc.take(), "ack a", "the failure is acknowledged")
			assert.Zero(t, svc.interruptCount(), "a failed update sends no interrupt")
			e.requireClean()
		})
	}
}

func TestLocalSecondUpWithoutChangesStartsNoMember(t *testing.T) {
	eachLocal(t, func(t *testing.T, lang, parallel string) {
		svc := newLocalService(t)
		e := setupLocal(t, lang, svc)

		code, out := e.up(parallel)
		require.Zero(t, code, out)
		assert.ElementsMatch(t, []string{"a:succeeded", "b:succeeded"}, finals(svc.take()))

		svc.snapshot(func() { svc.release = 4 })
		code, out = e.up(parallel)
		require.Zero(t, code, out)
		events := svc.take()
		assert.Empty(t, finals(events), "no member starts")
		assert.NotContains(t, events, "ack a")
		assert.ElementsMatch(t, []string{"a", "b"}, eventsWith(events, "unchanged "))
		svc.snapshot(func() {
			require.NotNil(t, svc.unchanged["a"])
			assert.EqualValues(t, 3, *svc.unchanged["a"], "the release that last ran the member")
			assert.EqualValues(t, 3, *svc.unchanged["b"])
		})
		observed, err := os.ReadFile(e.observed)
		require.NoError(t, err)
		assert.Contains(t, string(observed), "a-1", "the program still reads the outputs of the last run")
		assert.Zero(t, svc.interruptCount())
		e.requireClean()
	})
}

func TestLocalChangedSourceCommitRunsItsMembers(t *testing.T) {
	eachLocal(t, func(t *testing.T, lang, parallel string) {
		svc := newLocalService(t)
		e := setupLocal(t, lang, svc)
		code, out := e.up(parallel)
		require.Zero(t, code, out)
		svc.take()

		svc.snapshot(func() { svc.commits["acme/lib"] = "c2" })
		code, out = e.up(parallel)
		require.Zero(t, code, out)
		events := svc.take()
		assert.Equal(t, []string{"b:succeeded"}, finals(events), "only the member of the changed source runs")
		assert.Equal(t, []string{"a"}, eventsWith(events, "unchanged "))
		e.requireClean()
	})
}

func TestLocalDependentOfAMemberThatRanRuns(t *testing.T) {
	for _, mode := range []string{"output", "depends"} {
		for _, lang := range localLangs {
			for _, parallel := range []string{"1", "2", ""} {
				t.Run(mode+"/"+lang+"/parallel-"+parallel, func(t *testing.T) {
					t.Parallel()
					svc := newLocalService(t)
					e := setupLocal(t, lang, svc, "LOCAL_B_MODE="+mode)
					code, out := e.up(parallel)
					require.Zero(t, code, out)
					svc.take()

					code, out = e.up(parallel, "LOCAL_A_CMD=a2")
					require.Zero(t, code, out)
					events := svc.take()
					assert.ElementsMatch(t, []string{"a:succeeded", "b:succeeded"}, finals(events), "b runs because a ran")
					assert.Empty(t, eventsWith(events, "unchanged "))

					code, out = e.up(parallel, "LOCAL_A_CMD=a2")
					require.Zero(t, code, out)
					events = svc.take()
					assert.Empty(t, finals(events), "the next up changes nothing")
					assert.ElementsMatch(t, []string{"a", "b"}, eventsWith(events, "unchanged "))
					e.requireClean()
				})
			}
		}
	}
}

func TestLocalDependentThatFailedRunsAgain(t *testing.T) {
	eachLocal(t, func(t *testing.T, lang, parallel string) {
		svc := newLocalService(t)
		e := setupLocal(t, lang, svc, "LOCAL_B_MODE=depends")
		code, out := e.up(parallel)
		require.Zero(t, code, out)
		svc.take()

		svc.snapshot(func() { svc.members["b"] = &memberScript{fail: true, reason: "exit status 2"} })
		code, out = e.up(parallel, "LOCAL_A_CMD=a2")
		assert.NotZero(t, code, out)
		assert.Contains(t, finals(svc.take()), "a:succeeded")

		svc.snapshot(func() { delete(svc.members, "b") })
		code, out = e.up(parallel, "LOCAL_A_CMD=a2")
		require.Zero(t, code, out)
		events := svc.take()
		assert.Equal(t, []string{"b:succeeded"}, finals(events), "b runs again, a does not")
		assert.Equal(t, []string{"a"}, eventsWith(events, "unchanged "))
		e.requireClean()
	})
}

func TestLocalOneChangedEntryRunsTheWholeGroup(t *testing.T) {
	eachLocal(t, func(t *testing.T, lang, parallel string) {
		svc := newLocalService(t)
		e := setupLocal(t, lang, svc, "LOCAL_CASE=stacks")
		code, out := e.up(parallel)
		require.Zero(t, code, out)
		assert.ElementsMatch(t, []string{"svc#x:succeeded", "svc#y:succeeded"}, finals(svc.take()))

		code, out = e.up(parallel)
		require.Zero(t, code, out)
		events := svc.take()
		assert.Empty(t, finals(events))
		assert.ElementsMatch(t, []string{"svc#x", "svc#y"}, eventsWith(events, "unchanged "))

		code, out = e.up(parallel, "LOCAL_X_CMD=x2")
		require.Zero(t, code, out)
		events = svc.take()
		assert.ElementsMatch(t, []string{"svc#x:succeeded", "svc#y:succeeded"}, finals(events), "the group runs as one unit")
		assert.Empty(t, eventsWith(events, "unchanged "))
		e.requireClean()
	})
}

func TestLocalInterruptDuringAMemberWaitPostsOneInterrupt(t *testing.T) {
	for _, lang := range localLangs {
		for _, parallel := range []string{"1", ""} {
			t.Run(lang+"/parallel-"+parallel, func(t *testing.T) {
				svc := newLocalService(t)
				svc.members["a"] = &memberScript{hang: true}
				e := setupLocal(t, lang, svc)
				args := []string{"up", "--yes", "--skip-preview"}
				if parallel != "" {
					args = append(args, "--parallel", parallel)
				}
				cmd := e.command(nil, args...)
				var output strings.Builder
				cmd.Stdout, cmd.Stderr = &output, &output
				require.NoError(t, cmd.Start())
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()

				svc.waitFor(t, "two polls of the member", func() bool { return svc.polls["a"] >= 2 })
				signalled := time.Now()
				require.NoError(t, syscall.Kill(-cmd.Process.Pid, syscall.SIGINT))
				select {
				case err := <-done:
					assert.NotZero(t, exitCode(t, err), output.String())
					assert.Less(t, time.Since(signalled), 10*time.Second, "the up exits within seconds")
				case <-time.After(30 * time.Second):
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					t.Fatalf("the up did not exit after the interrupt:\n%s", output.String())
				}
				assert.Equal(t, 1, svc.interruptCount(), "exactly one interrupt:\n%s", output.String())
				e.requireClean()
			})
		}
	}
}

func TestLocalInterruptDuringAStageWaitPostsOneInterrupt(t *testing.T) {
	svc := newLocalService(t)
	svc.admit["prod"] = func(int) (string, string) { return "waiting", "Waiting for approval." }
	e := setupLocal(t, "go", svc, "LOCAL_TWO_STAGES=1")
	cmd := e.command(nil, "up", "--yes", "--skip-preview")
	var output strings.Builder
	cmd.Stdout, cmd.Stderr = &output, &output
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	svc.waitFor(t, "two polls of the stage", func() bool { return svc.admitPolls["prod"] >= 2 })
	signalled := time.Now()
	require.NoError(t, syscall.Kill(-cmd.Process.Pid, syscall.SIGINT))
	select {
	case err := <-done:
		assert.NotZero(t, exitCode(t, err), output.String())
		assert.Less(t, time.Since(signalled), 10*time.Second)
	case <-time.After(30 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		t.Fatalf("the up did not exit after the interrupt:\n%s", output.String())
	}
	assert.Equal(t, 1, svc.interruptCount())
	e.requireClean()
}

func TestLocalPreviewAndNormalUpPostNoInterrupt(t *testing.T) {
	for _, lang := range localLangs {
		t.Run(lang, func(t *testing.T) {
			t.Parallel()
			svc := newLocalService(t)
			e := setupLocal(t, lang, svc)

			code, out := e.pulumi(nil, "preview")
			require.Zero(t, code, out)
			assert.Empty(t, svc.declared(), "a preview sends nothing")
			assert.Empty(t, svc.take())

			code, out = e.pulumi(nil, "up", "--yes")
			require.Zero(t, code, out)
			assert.Zero(t, svc.interruptCount(), "an up with a preview posts no interrupt")
			e.requireClean()
		})
	}
}

func TestLocalOldServiceKeepsTheEarlierBehaviour(t *testing.T) {
	for _, lang := range localLangs {
		t.Run(lang, func(t *testing.T) {
			t.Parallel()
			svc := newLocalService(t)
			svc.mode = ""
			e := setupLocal(t, lang, svc)

			code, out := e.up("")
			assert.Equal(t, 10, code, "the members wait, as in a registration:\n%s", out)
			assert.Empty(t, svc.polls, "no member is started")
			assert.Empty(t, svc.admitPolls)
			assert.Zero(t, svc.interruptCount())
			for _, d := range svc.declared()[1:] {
				assert.NotContains(t, d, "runNumber")
			}

			svc.snapshot(func() { svc.cloudRun = true })
			svc.members["a"] = &memberScript{fail: true, reason: "boom"}
			code, out = e.up("")
			assert.Equal(t, 10, code, "a failed member is an await error in a run stack:\n%s", out)
			assert.Contains(t, out, "awaiting")
			assert.Zero(t, svc.interruptCount())
		})
	}
}

func TestLocalRegisterModeIsUnchanged(t *testing.T) {
	svc := newLocalService(t)
	svc.mode = "register"
	e := setupLocal(t, "go", svc, "PULUMI_DELIVERY_REGISTER=true")

	code, out := e.up("")
	assert.Equal(t, 10, code, out)
	assert.Empty(t, svc.polls)
	assert.Empty(t, svc.admitPolls)
	assert.Zero(t, svc.interruptCount())
	for _, d := range svc.declared() {
		assert.Equal(t, "register", d["mode"])
		assert.NotContains(t, d, "session")
		assert.NotContains(t, d, "runNumber")
	}
}

func TestLocalPassModeKeepsThePassBehaviour(t *testing.T) {
	svc := newLocalService(t)
	svc.mode = "pass"
	svc.cloudRun = true
	svc.members["a"] = &memberScript{}
	e := setupLocal(t, "go", svc)

	code, out := e.up("")
	assert.Zero(t, code, out)
	events := svc.take()
	assert.ElementsMatch(t, []string{"a:succeeded", "b:succeeded"}, finals(events))
	assert.Empty(t, eventsWith(events, "ack "))
	assert.Empty(t, svc.admitPolls, "a run stack has no admission")
	assert.NotContains(t, out, "Run 7")
}
