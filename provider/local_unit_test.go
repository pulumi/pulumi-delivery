// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi-go-provider/integration"
	"github.com/pulumi/pulumi/sdk/v3/go/property"

	"github.com/pulumi/pulumi-delivery/provider/cloudclient"
)

func useLocalService(t *testing.T, svc *localService) {
	t.Setenv("PULUMI_API", svc.URL)
	t.Setenv("PULUMI_ACCESS_TOKEN", "token")
}

func TestModeFromTheEnvironment(t *testing.T) {
	for value, want := range map[string]string{
		"": "local", "true": "register", "1": "register", "TRUE": "register", "false": "local", "0": "local", "yes": "local",
	} {
		t.Setenv("PULUMI_DELIVERY_REGISTER", value)
		assert.Equal(t, want, modeFromEnvironment(), "PULUMI_DELIVERY_REGISTER=%q", value)
	}
}

func TestTheServiceDecidesTheMode(t *testing.T) {
	setMode(t, modeLocal)
	for mode, want := range map[string]bool{"local": true, "register": false, "pass": false, "": false} {
		svc := newLocalService(t)
		svc.mode = mode
		useLocalService(t, svc)
		server := newServer(t)

		_, err := server.Construct(pipelineRequest(false))
		require.NoError(t, err)
		sent := svc.declared()[0]
		assert.Equal(t, "local", sent["mode"], "the provider asks for a local run")
		assert.NotEmpty(t, sent["session"])
		assert.NotContains(t, sent, "runNumber")

		_, err = server.Construct(pipelineRequest(false))
		require.NoError(t, err)
		_, hasRun := svc.declared()[1]["runNumber"]
		assert.Equal(t, want, hasRun, "mode %q", mode)
	}
}

func TestALocalPipelineRunsAsItsTeam(t *testing.T) {
	setMode(t, modeLocal)
	svc := newLocalService(t)
	useLocalService(t, svc)
	req := pipelineRequest(false)
	req.Inputs = property.NewMap(map[string]property.Value{"servicePrincipal": property.New(property.NewMap(
		map[string]property.Value{"teamName": property.New("platform")}))})

	_, err := newServer(t).Construct(req)
	require.NoError(t, err)
	sent := svc.declared()[0]
	assert.Equal(t, "local", sent["mode"])
	inputs := sent["inputs"].(map[string]any)
	assert.Equal(t, "platform", inputs["team"])
	assert.NotContains(t, inputs, "servicePrincipal")
}

func TestOneSessionUnderConcurrentConstructs(t *testing.T) {
	setMode(t, modeLocal)
	svc := newLocalService(t)
	useLocalService(t, svc)
	server := newServer(t)

	const constructs = 24
	var wg sync.WaitGroup
	errs := make(chan error, constructs)
	for i := range constructs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			request := pipelineRequest(false)
			if i%2 == 1 {
				request = stageRequest(property.Value{})
				request.DryRun = false
			}
			_, err := server.Construct(request)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	declared := svc.declared()
	require.Len(t, declared, constructs)
	withoutRun := 0
	for _, d := range declared {
		assert.Equal(t, declared[0]["session"], d["session"], "one session for the process")
		if _, ok := d["runNumber"]; !ok {
			withoutRun++
		} else {
			assert.EqualValues(t, 7, d["runNumber"])
		}
	}
	assert.Equal(t, 1, withoutRun, "only the first declaration lacks the run")
	assert.Empty(t, svc.violationList())
	assert.Equal(t, constructs/2, svc.admitPolls["prod"], "each Stage asks for its admission")
}

func diffOf(inputs, state MemberRunArgs, lastRun int64) infer.DiffRequest[MemberRunArgs, MemberRunState] {
	return infer.DiffRequest[MemberRunArgs, MemberRunState]{
		Inputs: inputs, State: MemberRunState{MemberRunArgs: state, LastRun: lastRun},
	}
}

func TestMemberRunDiff(t *testing.T) {
	declaration := func(command string, dependencies ...any) map[string]any {
		return map[string]any{
			"urn": "u", "inputs": map[string]any{"command": command},
			"dependencies": dependencies, "propertyDependencies": map[string]any{"environment": dependencies},
		}
	}
	local := func(a MemberRunArgs) MemberRunArgs { a.Session, a.Run, a.Release = "s", 8, 4; return a }
	base := MemberRunArgs{
		Member: "m", Declaration: declaration("make", "x", "y"), Commits: map[string]string{"src": "c1"},
		DependencyRuns: map[string]int64{"x": 5},
	}
	withRuns := func(a MemberRunArgs, runs map[string]int64) MemberRunArgs { a.DependencyRuns = runs; return a }

	for name, c := range map[string]struct {
		inputs, state MemberRunArgs
		lastRun       int64
		changes       bool
	}{
		"equal":                                  {local(base), base, 5, false},
		"the run and the release are not inputs": {local(base), func() MemberRunArgs { a := base; a.Session, a.Run, a.Release = "old", 3, 2; return a }(), 5, false},
		"dependency lists are sets": {
			func() MemberRunArgs { a := local(base); a.Declaration = declaration("make", "y", "x"); return a }(), base, 5, false,
		},
		"another command":    {func() MemberRunArgs { a := local(base); a.Declaration = declaration("test", "x", "y"); return a }(), base, 5, true},
		"another dependency": {func() MemberRunArgs { a := local(base); a.Declaration = declaration("make", "x", "z"); return a }(), base, 5, true},
		"another commit":     {func() MemberRunArgs { a := local(base); a.Commits = map[string]string{"src": "c2"}; return a }(), base, 5, true},
		"a new source": {func() MemberRunArgs {
			a := local(base)
			a.Commits = map[string]string{"src": "c1", "lib": "c9"}
			return a
		}(), base, 5, true},
		"no commits in the inputs":       {func() MemberRunArgs { a := local(base); a.Commits = nil; return a }(), base, 5, false},
		"a dependency ran":               {withRuns(local(base), map[string]int64{"x": 8}), base, 5, true},
		"the dependency did not":         {withRuns(local(base), map[string]int64{"x": 5}), base, 5, false},
		"a failed run kept the old runs": {withRuns(local(base), map[string]int64{"x": 7}), withRuns(base, map[string]int64{"x": 4}), 4, true},
		"no runs saved before":           {local(base), withRuns(base, nil), 5, true},
		"no dependencies":                {withRuns(local(base), nil), withRuns(base, map[string]int64{}), 5, false},
		"another group digest":           {func() MemberRunArgs { a := local(base); a.GroupDigest = "d2"; return a }(), func() MemberRunArgs { a := base; a.GroupDigest = "d1"; return a }(), 5, true},
		"the same group digest":          {func() MemberRunArgs { a := local(base); a.GroupDigest = "d1"; return a }(), func() MemberRunArgs { a := base; a.GroupDigest = "d1"; return a }(), 5, false},
		"no local run ran it":            {local(base), base, 0, true},
		"a registration":                 {base, base, 5, true},
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := (&MemberRun{}).Diff(t.Context(), diffOf(c.inputs, c.state, c.lastRun))
			require.NoError(t, err)
			assert.Equal(t, c.changes, resp.HasChanges)
		})
	}
}

// childrenServer is a server whose children answer like a MemberRun of a local run that ran in an earlier run.
func childrenServer(t *testing.T, children *[]property.Map) integration.Server {
	t.Helper()
	provider, err := newProvider()
	require.NoError(t, err)
	var mu sync.Mutex
	server, err := integration.NewServer(t.Context(), "delivery", semver.MustParse("0.0.1"),
		integration.WithProvider(provider),
		integration.WithMocks(&integration.MockResourceMonitor{
			NewResourceF: func(args integration.MockResourceArgs) (string, property.Map, error) {
				if args.TypeToken == typeMemberRun {
					mu.Lock()
					*children = append(*children, args.Inputs)
					mu.Unlock()
				}
				return args.Name, property.NewMap(map[string]property.Value{
					"outputs":     property.New(property.NewMap(nil)),
					"lastRun":     property.New(2.0),
					"lastRelease": property.New(1.0),
				}), nil
			},
		}))
	require.NoError(t, err)
	return server
}

func groupDigests(children []property.Map) []string {
	var out []string
	for _, child := range children {
		out = append(out, child.Get("groupDigest").AsString())
	}
	return out
}

func TestAGroupHasOneDigestForAllEntries(t *testing.T) {
	setMode(t, modeLocal)
	svc := newLocalService(t)
	useLocalService(t, svc)
	var children []property.Map
	server := childrenServer(t, &children)

	construct := func(entries map[string]property.Value) []string {
		children = nil
		request := stacksRequest("app", false, entries)
		_, err := server.Construct(request)
		require.NoError(t, err)
		require.Len(t, children, len(entries))
		digests := groupDigests(children)
		for _, child := range children {
			assert.True(t, child.Get("groupDigest").Secret(), "the digest is secret, so it cannot leak a low-entropy secret")
			assert.NotEmpty(t, child.Get("session").AsString())
			assert.EqualValues(t, 7, child.Get("run").AsNumber())
		}
		return digests
	}
	env := func(value string) map[string]property.Value {
		return map[string]property.Value{"environment": property.New(property.NewMap(map[string]property.Value{"CMD": property.New(value)}))}
	}
	entries := func(apiCommand string) map[string]property.Value {
		return map[string]property.Value{
			"api":    stacksEntry("org/shop/api", env(apiCommand)),
			"worker": stacksEntry("org/shop/worker", env("w")),
		}
	}

	first := construct(entries("a1"))
	assert.Equal(t, first[0], first[1], "every entry gets the digest of the group")
	again := construct(entries("a1"))
	assert.Equal(t, first[0], again[0], "the same entries give the same digest")
	changed := construct(entries("a2"))
	assert.Equal(t, changed[0], changed[1])
	assert.NotEqual(t, first[0], changed[0], "one changed entry changes the digest of all")
	require.Len(t, svc.unchanged, 2, "a child that did not run in this run is reported as unchanged")
	require.NotNil(t, svc.unchanged["app#api"])
	assert.EqualValues(t, 1, *svc.unchanged["app#api"], "with the release that last ran it")
}

func TestAnOlderServiceGetsNoLocalInputsOnTheChildren(t *testing.T) {
	setMode(t, modeLocal)
	svc := newLocalService(t)
	svc.mode = ""
	useLocalService(t, svc)
	var children []property.Map
	server := childrenServer(t, &children)

	_, err := server.Construct(stacksRequest("app", false, twoEntries()))
	require.NoError(t, err)
	require.Len(t, children, 2)
	for _, child := range children {
		for _, key := range []string{"session", "run", "release", "commits", "dependencyRuns", "groupDigest"} {
			assert.NotContains(t, child.AsMap(), key)
		}
	}
	assert.Empty(t, svc.unchanged)
}

func TestCancelPostsTheInterruptOnlyWhileAWaitIsInFlight(t *testing.T) {
	svc := newLocalService(t)
	useLocalService(t, svc)
	svc.session, svc.run = "session-1", 7
	sess := newSession()
	sess.adopt("session-1", 7)
	_, err := sess.client("org", urnOf(typeStage, "dev"))
	require.NoError(t, err)

	sess.cancel()
	assert.Zero(t, svc.interruptCount(), "the end of a process is not an interrupt")

	waiting := make(chan struct{})
	ended := make(chan error, 1)
	go func() {
		first := true
		ended <- sess.wait(context.Background(), func(context.Context) (bool, error) {
			if first {
				first = false
				close(waiting)
			}
			return false, nil
		})
	}()
	<-waiting
	start := time.Now()
	sess.cancel()
	assert.ErrorIs(t, <-ended, errInterrupted)
	assert.Less(t, time.Since(start), 2*time.Second, "the wait ends at once")
	assert.Equal(t, 1, svc.interruptCount())

	sess.cancel()
	assert.Equal(t, 1, svc.interruptCount(), "a second Cancel posts no second interrupt")
	assert.Empty(t, svc.violationList())

	err = sess.wait(context.Background(), func(context.Context) (bool, error) { return true, nil })
	assert.ErrorIs(t, err, errInterrupted, "no wait starts after an interrupt")
}

func TestCancelOfTheProviderPostsNothingWithoutWaits(t *testing.T) {
	setMode(t, modeLocal)
	svc := newLocalService(t)
	useLocalService(t, svc)
	provider, err := newProvider()
	require.NoError(t, err)
	server, err := integration.NewServer(t.Context(), "delivery", semver.MustParse("0.0.1"), integration.WithProvider(provider))
	require.NoError(t, err)

	_, err = server.Construct(pipelineRequest(false))
	require.NoError(t, err)
	require.NoError(t, provider.Cancel(t.Context()))
	assert.Zero(t, svc.interruptCount())
}

func TestAWaitStopsOnAClientError(t *testing.T) {
	sess := newSession()
	calls := 0
	err := sess.wait(context.Background(), func(context.Context) (bool, error) {
		calls++
		return false, &cloudclient.APIError{Method: "POST", Path: "/p", StatusCode: 409, Body: `{"message":"Stage S is already closed."}`}
	})
	assert.ErrorContains(t, err, "Stage S is already closed.")
	assert.Equal(t, 1, calls)
}

func TestCancelDuringTheFirstDeclarationIsNotLost(t *testing.T) {
	svc := newLocalService(t)
	useLocalService(t, svc)
	release := make(chan struct{})
	entered := make(chan struct{})
	svc.beforeDeclare = func() { close(entered); <-release }
	sess := newSession()

	done := make(chan error, 1)
	go func() {
		_, err := sess.declare(t.Context(), "org", urnOf(typeStage, "dev"), cloudclient.Declaration{Mode: modeLocal, URN: string(urnOf(typeStage, "dev"))})
		done <- err
	}()
	<-entered
	sess.cancel()
	assert.Zero(t, svc.interruptCount(), "the run is not known yet")
	close(release)
	require.NoError(t, <-done)
	assert.Equal(t, 1, svc.interruptCount(), "the interrupt goes out once the declaration is answered")
	err := sess.wait(context.Background(), func(context.Context) (bool, error) { return false, nil })
	assert.ErrorIs(t, err, errInterrupted, "a wait that starts after the interrupt stops at once")
	assert.Empty(t, svc.violationList())
}

func TestCancelDuringALaterDeclarationPostsTheInterrupt(t *testing.T) {
	svc := newLocalService(t)
	useLocalService(t, svc)
	sess := newSession()
	_, err := sess.declare(t.Context(), "org", urnOf(typeStage, "dev"), cloudclient.Declaration{Mode: modeLocal, URN: string(urnOf(typeStage, "dev"))})
	require.NoError(t, err)

	release := make(chan struct{})
	entered := make(chan struct{})
	svc.beforeDeclare = func() { close(entered); <-release }
	done := make(chan error, 1)
	go func() {
		_, err := sess.declare(t.Context(), "org", urnOf(typeStage, "prod"), cloudclient.Declaration{Mode: modeLocal, URN: string(urnOf(typeStage, "prod"))})
		done <- err
	}()
	<-entered
	sess.cancel()
	assert.Equal(t, 1, svc.interruptCount())
	close(release)
	require.NoError(t, <-done)
	assert.Equal(t, 1, svc.interruptCount())
	assert.Empty(t, svc.violationList())
}

func TestCancelWithNothingInFlightPostsNothingAfterADeclaration(t *testing.T) {
	svc := newLocalService(t)
	useLocalService(t, svc)
	sess := newSession()
	_, err := sess.declare(t.Context(), "org", urnOf(typeStage, "dev"), cloudclient.Declaration{Mode: modeLocal, URN: string(urnOf(typeStage, "dev"))})
	require.NoError(t, err)
	sess.cancel()
	assert.Zero(t, svc.interruptCount())
}
