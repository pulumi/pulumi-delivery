// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/urn"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/pulumi/pulumi-delivery/provider/cloudclient"
)

// reportTimeout bounds the wait for the reports of unchanged members at the end of a Construct.
const reportTimeout = 30 * time.Second

// memberExtras is what a member component adds to its children in a local run: the pinned commits of its sources,
// the last run of each dependency, and the digest of its group. It also reports an unchanged child to the service.
type memberExtras struct {
	ctx            context.Context
	sess           *session
	organization   string
	commits        map[string]string
	dependencyRuns map[string]int64
	digest         string

	pending sync.WaitGroup
	mu      sync.Mutex
	err     error
}

func newMemberExtras(ctx context.Context, sess *session, organization string) *memberExtras {
	return &memberExtras{ctx: ctx, sess: sess, organization: organization}
}

// apply adds the extra inputs to the inputs of a child. Nothing is added outside a local run.
func (e *memberExtras) apply(inputs pulumi.Map) pulumi.Map {
	if len(e.commits) > 0 {
		inputs["commits"] = pulumi.ToStringMap(e.commits)
	}
	if len(e.dependencyRuns) > 0 {
		runs := pulumi.IntMap{}
		for dependency, run := range e.dependencyRuns {
			runs[dependency] = pulumi.Int(int(run))
		}
		inputs["dependencyRuns"] = runs
	}
	if e.sess.local() {
		inputs["session"] = pulumi.String(e.sess.id)
		inputs["run"] = pulumi.Int(int(e.sess.runNumber()))
		if release := e.sess.releaseNumber(); release != nil {
			inputs["release"] = pulumi.Int(int(*release))
		}
	}
	if e.digest != "" {
		inputs["groupDigest"] = pulumi.ToSecret(pulumi.String(e.digest))
	}
	return inputs
}

// watch looks at the child once the engine has registered it. A child that did not run in this update is reported
// to the service as unchanged. Its last run is what the components that come after it record as the run they ran against.
func (e *memberExtras) watch(child *memberRunResource, member, entry string) {
	if !e.sess.local() {
		return
	}
	e.pending.Add(1)
	pulumi.All(child.URN(), child.LastRun, child.LastRelease).ApplyT(func(v []any) string {
		defer e.pending.Done()
		if err := e.report(string(v[0].(pulumi.URN)), member, entry, int64(v[1].(int)), int64(v[2].(int))); err != nil {
			e.mu.Lock()
			e.err = errors.Join(e.err, err)
			e.mu.Unlock()
		}
		return ""
	})
}

func (e *memberExtras) report(child, member, entry string, lastRun, lastRelease int64) error {
	e.sess.setLastRun(lastRun, member, child)
	if lastRun == e.sess.runNumber() {
		return nil
	}
	client, err := e.sess.client(e.organization, urn.URN(member))
	if err != nil {
		return err
	}
	start := cloudclient.MemberStart{URN: member, Entry: entry, ChildURN: child}
	if lastRelease > 0 {
		start.LastReleaseNumber = &lastRelease
	}
	return client.ReportUnchanged(e.ctx, start)
}

// wait blocks until every watched child is reported, and returns the failures.
func (e *memberExtras) wait() error {
	done := make(chan struct{})
	go func() {
		e.pending.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(reportTimeout):
		return errors.New("the provider could not report the unchanged members to the service")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err
}

// sourceURNs returns the URNs of the sources that a member component reads.
func sourceURNs(typ string, inputs property.Map) []urn.URN {
	reference := func(entry property.Map) []urn.URN {
		if v, ok := entry.GetOk("source"); ok && v.IsResourceReference() {
			return []urn.URN{v.AsResourceReference().URN}
		}
		return nil
	}
	switch typ {
	case typeJob:
		return reference(inputs)
	case typeStacks:
		var out []urn.URN
		for _, entry := range mapInput(inputs.Get("stacks")).AsMap() {
			out = append(out, reference(mapInput(entry))...)
		}
		return out
	}
	return nil
}
