// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi/pkg/v3/resource/stack"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/config"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/urn"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	comProvider "github.com/pulumi/pulumi/sdk/v3/go/pulumi/provider"

	"github.com/pulumi/pulumi-delivery/provider/cloudclient"
)

var errNeedsCloud = errors.New("Delivery needs Pulumi Cloud. Run `pulumi login` to a Pulumi Cloud backend.")

// clientFor returns the client for the stack of a resource. The CLI sets PULUMI_API and
// PULUMI_ACCESS_TOKEN for its plugins. The pair is the only credential source.
func clientFor(organization string, resourceURN urn.URN) (*cloudclient.Client, error) {
	api, token := os.Getenv("PULUMI_API"), os.Getenv("PULUMI_ACCESS_TOKEN")
	if api == "" || token == "" {
		return nil, errNeedsCloud
	}
	return cloudclient.New(api, organization, string(resourceURN.Project()), string(resourceURN.Stack()), token), nil
}

func isDeliveryComponent(typ string) bool {
	_, source := sourceKinds[typ]
	return source || typ == typePipeline || typ == typeStage || typ == typeJob || typ == typeStacks ||
		typ == typeArtifact || typ == typePipelineSource
}

// withDeliveryConstruct wraps the Construct of the provider. For a Delivery component it
//   - rejects error hooks on members,
//   - sends the declaration to the service in a real update, before the component code runs,
//   - reads the value of a source from the service in a run, and
//   - makes the outputs of a member depend on its child resource.
func withDeliveryConstruct(config *ProviderConfig, inner func(context.Context, p.ConstructRequest) (p.ConstructResponse, error),
) func(context.Context, p.ConstructRequest) (p.ConstructResponse, error) {
	return func(ctx context.Context, req p.ConstructRequest) (p.ConstructResponse, error) {
		typ := string(req.Urn.Type())
		if !isDeliveryComponent(typ) {
			return inner(ctx, req)
		}
		if (typ == typeJob || typ == typeStacks || typ == typeArtifact) && req.ResourceHooks != nil && len(req.ResourceHooks.OnError) > 0 {
			return p.ConstructResponse{}, errors.New("Delivery resources do not support error hooks. Use a retry or a new run.")
		}
		req.Inputs = qualifyStackNames(typ, req.Inputs, req.Organization)
		req.Inputs = secretRegistryPassword(typ, req.Inputs)
		if err := validateInputs(typ, req.Inputs); err != nil {
			return p.ConstructResponse{}, err
		}

		decl := newDeclaration(req)
		commit := ""
		var release *int64
		var upstream *pipelineValue
		if !req.DryRun {
			client, err := clientFor(req.Organization, req.Urn)
			if err != nil {
				return p.ConstructResponse{}, err
			}
			inputs := req.Inputs
			if typ == typePipeline && config.Source != nil {
				inputs = inputs.Set("programSource", property.New(config.Source.value()))
			}
			request, err := decl.request(ctx, inputs)
			if err != nil {
				return p.ConstructResponse{}, err
			}
			response, err := client.Declare(ctx, request)
			if err != nil {
				return p.ConstructResponse{}, err
			}
			reportDeclaration(p.GetLogger(ctx), response)
			if commit, err = sourceCommit(ctx, client, req); err != nil {
				return p.ConstructResponse{}, err
			}
			if release, err = runRelease(ctx, client, req); err != nil {
				return p.ConstructResponse{}, err
			}
			if upstream, err = readPipelineSource(ctx, client, req); err != nil {
				return p.ConstructResponse{}, err
			}
		}

		resp, err := p.ProgramConstruct(ctx, req, func(pctx *pulumi.Context, typ, name string,
			inputs comProvider.ConstructInputs, opts pulumi.ResourceOption,
		) (*comProvider.ConstructResult, error) {
			return buildComponent(pctx, typ, name, inputs, opts, decl, req.Organization, commit)
		})
		if err != nil {
			return resp, err
		}
		resp.State = finishState(typ, resp.State, commit, release, upstream, req.Inputs)
		logConstruct(req.Urn, resp)
		return resp, nil
	}
}

// finishState makes the outputs of a Job or an Artifact depend on its child (and drops the child URN), gives a
// source without a value an unknown commit, sets the release number of a Pipeline, and sets the outputs of a
// PipelineSource. The Go SDK does not carry any of them in its response.
func finishState(typ string, state property.Map, commit string, release *int64, upstream *pipelineValue, inputs property.Map) property.Map {
	if typ == typePipeline {
		if release == nil {
			return state.Set("releaseNumber", property.New(property.Computed))
		}
		return state.Set("releaseNumber", property.New(float64(*release)))
	}
	if typ == typePipelineSource {
		return upstream.state(state)
	}
	if _, isSource := sourceKinds[typ]; isSource {
		if commit == "" {
			return state.Set("commit", property.New(property.Computed))
		}
		return state.Set("commit", property.New(commit))
	}
	// The child URNs are not public outputs: they only carry the dependencies.
	if children, ok := state.GetOk("childUrns"); ok && children.IsMap() {
		return finishStacks(state, children.AsMap())
	}
	child, ok := state.GetOk("childUrn")
	if !ok || !child.IsString() {
		return state
	}
	childURN := urn.URN(child.AsString())
	out := map[string]property.Value{}
	for key, value := range state.Delete("childUrn").AsMap() {
		out[key] = value.WithDependencies(append(value.Dependencies(), childURN))
	}
	// The SDK leaves an unknown value out of the state, so a missing value is unknown too.
	key := "outputs"
	if typ == typeArtifact {
		key = "value"
	}
	if _, ok := out[key]; !ok {
		out[key] = property.New(property.Computed).WithDependencies([]urn.URN{childURN})
	} else if typ == typeArtifact && out[key].IsComputed() {
		// An unknown value is not secret: the engine rejects a secret without a value.
		out[key] = out[key].WithSecret(false)
	} else if typ == typeArtifact && hasSecret(inputs.Get("value")) {
		// The child outputs are secret as a whole, so the secret mark comes from the input: only an actual
		// secret is secret. A known value is marked here, because an unknown one cannot be secret.
		out[key] = out[key].WithSecret(true)
	}
	return property.NewMap(out)
}

// finishStacks makes the outputs of each entry depend on the child of the entry, and the outputs of the
// component on every child: a Stacks resource is one unit, so a consumer waits for the whole group.
func finishStacks(state, children property.Map) property.Map {
	var all []urn.URN
	for _, child := range children.AsMap() {
		all = append(all, urn.URN(child.AsString()))
	}
	outputs, ok := state.GetOk("outputs")
	if !ok || !outputs.IsMap() {
		// Unknown while an entry waits, and in a preview: it stays unknown, and no consumer sees an empty map.
		// The SDK leaves an unknown value out of the state, so a missing value is unknown too.
		if !ok {
			outputs = property.New(property.Computed)
		}
		return state.Delete("childUrns").Set("outputs", outputs.WithDependencies(append(outputs.Dependencies(), all...)))
	}
	entries := map[string]property.Value{}
	for entry, value := range outputs.AsMap().AsMap() {
		if child, ok := children.GetOk(entry); ok {
			value = value.WithDependencies(append(value.Dependencies(), urn.URN(child.AsString())))
		}
		entries[entry] = value
	}
	return state.Delete("childUrns").Set("outputs",
		property.New(property.NewMap(entries)).WithSecret(outputs.Secret()).WithDependencies(all))
}

// runRelease returns the release number of the run for a Pipeline. It is nil in a registration, and for a run
// without a release.
func runRelease(ctx context.Context, client *cloudclient.Client, req p.ConstructRequest) (*int64, error) {
	if req.Urn.Type() != typePipeline {
		return nil, nil
	}
	run, err := client.GetRun(ctx)
	if err != nil || run == nil {
		return nil, err
	}
	return run.ReleaseNumber, nil
}

// sourceCommit returns the commit that the run recorded for a source. It is empty in a registration and
// for a component that is not a source.
func sourceCommit(ctx context.Context, client *cloudclient.Client, req p.ConstructRequest) (string, error) {
	kind, ok := sourceKinds[string(req.Urn.Type())]
	if !ok {
		return "", nil
	}
	run, err := client.GetRun(ctx)
	if err != nil || run == nil {
		return "", err
	}
	repository, branch := stringInput(req.Inputs, "repository"), stringInput(req.Inputs, "branch")
	if repository == "" || branch == "" {
		return "", nil
	}
	value, err := client.SourceValue(ctx, cloudclient.SourceIdentity{
		Kind: kind, Integration: stringInput(req.Inputs, "integration"), Repository: repository, Branch: branch,
	})
	if err != nil {
		return "", err
	}
	commit, _ := value["commit"].(string)
	return commit, nil
}

// pipelineValue is what the run recorded for a PipelineSource.
type pipelineValue struct {
	releaseNumber, runNumber float64
	artifacts                property.Map
}

// state sets the outputs of a PipelineSource. Without a value (a registration, a preview, or an input that is
// not known) all three are unknown.
func (v *pipelineValue) state(state property.Map) property.Map {
	if v == nil {
		for _, name := range []string{"releaseNumber", "runNumber", "artifacts"} {
			state = state.Set(name, property.New(property.Computed))
		}
		return state
	}
	return state.Set("releaseNumber", property.New(v.releaseNumber)).
		Set("runNumber", property.New(v.runNumber)).
		Set("artifacts", property.New(v.artifacts).WithSecret(hasSecret(property.New(v.artifacts))))
}

// qualifyStackNames prefixes the organization of the pipeline to the project/stack names in the inputs
// (`pipeline` of a PipelineSource, `stack` of each Stacks entry), so that everything after sees org/project/stack.
// Pulumi Cloud only accepts stacks of the organization of the pipeline. Other values are left for validation.
func qualifyStackNames(typ string, inputs property.Map, organization string) property.Map {
	// like gives v the secret mark and the dependencies of old.
	like := func(old, v property.Value) property.Value {
		return v.WithSecret(old.Secret()).WithDependencies(old.Dependencies())
	}
	qualify := func(value property.Value) property.Value {
		if !value.IsString() || organization == "" {
			return value
		}
		if project, stack, ok := strings.Cut(value.AsString(), "/"); !ok || project == "" || stack == "" || strings.Contains(stack, "/") {
			return value
		}
		return like(value, property.New(organization+"/"+value.AsString()))
	}
	switch typ {
	case typePipelineSource:
		if v, ok := inputs.GetOk("pipeline"); ok {
			inputs = inputs.Set("pipeline", qualify(v))
		}
	case typeStacks:
		if stacks := inputs.Get("stacks"); stacks.IsMap() {
			entries := stacks.AsMap()
			for name, entry := range entries.AsMap() {
				if v, ok := mapInput(entry).GetOk("stack"); ok {
					entries = entries.Set(name, like(entry, property.New(entry.AsMap().Set("stack", qualify(v)))))
				}
			}
			inputs = inputs.Set("stacks", like(stacks, property.New(entries)))
		}
	}
	return inputs
}

// readPipelineSource returns the value of a PipelineSource in a run, or nil. The map is secret if any artifact
// is, and the artifacts keep their own marks inside.
func readPipelineSource(ctx context.Context, client *cloudclient.Client, req p.ConstructRequest) (*pipelineValue, error) {
	if req.Urn.Type() != typePipelineSource {
		return nil, nil
	}
	pipeline, stage := stringInput(req.Inputs, "pipeline"), stringInput(req.Inputs, "stage")
	if pipeline == "" || stage == "" {
		return nil, nil
	}
	run, err := client.GetRun(ctx)
	if err != nil || run == nil {
		return nil, err
	}
	value, err := client.RunValue(ctx, "pipeline", cloudclient.PipelineIdentity{Pipeline: pipeline, Stage: stage})
	if err != nil {
		return nil, err
	}
	out := &pipelineValue{}
	out.releaseNumber, _ = value["releaseNumber"].(float64)
	out.runNumber, _ = value["runNumber"].(float64)
	artifacts, _ := value["artifacts"].(map[string]any)
	decoded, err := stack.DeserializeProperties(artifacts, config.NewErrorCrypter("no ciphertext"))
	if err != nil {
		return nil, fmt.Errorf("decoding the artifacts of %s: %w", pipeline, err)
	}
	out.artifacts = resource.FromResourcePropertyValue(resource.NewObjectProperty(decoded)).AsMap()
	return out, nil
}

// hasSecret reports whether a value is secret or holds a secret.
func hasSecret(value property.Value) bool {
	if value.Secret() {
		return true
	}
	switch {
	case value.IsMap():
		for _, child := range value.AsMap().AsMap() {
			if hasSecret(child) {
				return true
			}
		}
	case value.IsArray():
		for _, child := range value.AsArray().AsSlice() {
			if hasSecret(child) {
				return true
			}
		}
	}
	return false
}

// secretRegistryPassword marks image.credentials.password of a Job, or of each Stacks entry, as secret.
// Most SDKs do not mark a plain string, so the provider does. Unknown and null values stay as they are.
func secretRegistryPassword(typ string, inputs property.Map) property.Map {
	switch typ {
	case typeJob:
		return withSecretPassword(inputs)
	case typeStacks:
		stacks, ok := inputs.GetOk("stacks")
		if !ok || !stacks.IsMap() {
			return inputs
		}
		entries := map[string]property.Value{}
		for name, entry := range stacks.AsMap().AsMap() {
			if entry.IsMap() {
				entry = property.New(withSecretPassword(entry.AsMap())).WithSecret(entry.Secret()).WithDependencies(entry.Dependencies())
			}
			entries[name] = entry
		}
		return inputs.Set("stacks", property.New(property.NewMap(entries)).WithSecret(stacks.Secret()).WithDependencies(stacks.Dependencies()))
	}
	return inputs
}

// withSecretPassword marks image.credentials.password of one member as secret.
func withSecretPassword(member property.Map) property.Map {
	image, ok := member.GetOk("image")
	if !ok || !image.IsMap() {
		return member
	}
	credentials, ok := image.AsMap().GetOk("credentials")
	if !ok || !credentials.IsMap() {
		return member
	}
	password, ok := credentials.AsMap().GetOk("password")
	if !ok || !password.IsString() {
		return member
	}
	credentials = property.New(credentials.AsMap().Set("password", password.WithSecret(true))).
		WithSecret(credentials.Secret()).WithDependencies(credentials.Dependencies())
	image = property.New(image.AsMap().Set("credentials", credentials)).
		WithSecret(image.Secret()).WithDependencies(image.Dependencies())
	return member.Set("image", image)
}

func mapInput(value property.Value) property.Map {
	if value.IsMap() {
		return value.AsMap()
	}
	return property.Map{}
}

// stringInput returns a known string input, or "".
func stringInput(inputs property.Map, name string) string {
	if value, ok := inputs.GetOk(name); ok && value.IsString() {
		return value.AsString()
	}
	return ""
}

var sourceTypes = []string{
	"delivery:index:GitHubSource", "delivery:index:GitLabSource",
	"delivery:index:BitbucketSource", "delivery:index:AzureDevOpsSource",
}

// requireRef checks that an input is a reference to a resource of one of the types, unless it is unknown.
func requireRef(inputs property.Map, name string, types ...string) error {
	value, ok := inputs.GetOk(name)
	switch {
	case ok && value.IsComputed():
		return nil
	case !ok || !value.IsResourceReference():
		return fmt.Errorf("%s is required and must be a resource", name)
	}
	if got := string(value.AsResourceReference().URN.Type()); !slices.Contains(types, got) {
		return fmt.Errorf("%s must be one of %v, not %s", name, types, got)
	}
	return nil
}

// validateTrigger checks the trigger of a source: true, or for a VCS source (allowPaths) an object with a list of
// paths. Unknown values pass.
func validateTrigger(inputs property.Map, allowPaths bool) error {
	trigger, ok := inputs.GetOk("trigger")
	if !ok || trigger.IsNull() || trigger.IsComputed() {
		return nil
	}
	if trigger.IsBool() && trigger.AsBool() {
		return nil
	}
	if !allowPaths {
		return errors.New("trigger must be true. Omit it for a source that does not start runs.")
	}
	if !trigger.IsMap() {
		return errors.New("trigger must be true or { paths: [...] }. Omit it for a source that does not start runs.")
	}
	paths := trigger.AsMap().Get("paths")
	if paths.IsComputed() {
		return nil
	}
	if !paths.IsArray() || paths.AsArray().Len() == 0 {
		return errors.New("trigger.paths must be a list with at least one path. Use trigger: true to start a run on every push.")
	}
	for _, path := range paths.AsArray().AsSlice() {
		if path.IsString() && strings.TrimSpace(path.AsString()) == "" {
			return errors.New("trigger.paths must not contain an empty path")
		}
	}
	return nil
}

func validateInputs(typ string, inputs property.Map) error {
	if _, ok := sourceKinds[typ]; ok {
		if err := requiredUnlessUnknown(inputs, stringInput(inputs, "repository"), "repository"); err != nil {
			return err
		}
		if err := requiredUnlessUnknown(inputs, stringInput(inputs, "branch"), "branch"); err != nil {
			return err
		}
		return validateTrigger(inputs, true)
	}
	switch typ {
	case typePipeline:
		return validateServicePrincipal(inputs)
	case typeStage:
		if err := requireRef(inputs, "pipeline", typePipeline); err != nil {
			return err
		}
		return validateRuleValue(inputs.Get("when"), 1)
	case typeStacks:
		return validateStacks(inputs)
	case typePipelineSource:
		if pipeline := stringInput(inputs, "pipeline"); pipeline != "" {
			if parts := strings.Split(pipeline, "/"); len(parts) != 3 || slices.Contains(parts, "") {
				return fmt.Errorf("pipeline must be project/stack, not %q", pipeline)
			}
		} else if err := requiredUnlessUnknown(inputs, "", "pipeline"); err != nil {
			return err
		}
		if err := requiredUnlessUnknown(inputs, stringInput(inputs, "stage"), "stage"); err != nil {
			return err
		}
		return validateTrigger(inputs, false)
	case typeArtifact:
		if _, ok := inputs.GetOk("value"); !ok {
			return errors.New("value is required")
		}
	case typeJob:
		if err := requireRef(inputs, "source", sourceTypes...); err != nil {
			return err
		}
		for _, name := range []string{"directory", "command"} {
			if err := requiredUnlessUnknown(inputs, stringInput(inputs, name), name); err != nil {
				return err
			}
		}
		if timeout := stringInput(inputs, "timeout"); timeout != "" {
			if d, err := time.ParseDuration(timeout); err != nil || d <= 0 {
				return fmt.Errorf("timeout must be a positive duration, such as 30m")
			}
		}
		if _, ok := inputs.GetOk("runtime"); ok {
			if err := requireRef(inputs, "runtime", "delivery:index:WorkflowsRuntime"); err != nil {
				return err
			}
		}
		if err := checkImage(inputs); err != nil {
			return err
		}
		return checkEnvironment(inputs)
	}
	return nil
}

func validateServicePrincipal(inputs property.Map) error {
	if _, ok := inputs.GetOk("servicePrincipal"); !ok || unknown(inputs, "servicePrincipal") ||
		unknown(inputs, "servicePrincipal", "userId") || unknown(inputs, "servicePrincipal", "teamName") {
		return nil
	}
	principal := mapInput(inputs.Get("servicePrincipal"))
	user, team := stringInput(principal, "userId") != "", stringInput(principal, "teamName") != ""
	switch {
	case user && team:
		return errors.New("servicePrincipal takes userId or teamName, not both")
	case !user && !team:
		return errors.New("servicePrincipal needs userId or teamName")
	}
	return nil
}

func checkImage(inputs property.Map) error {
	if _, ok := inputs.GetOk("image"); !ok || unknown(inputs, "image") {
		return nil
	}
	image := mapInput(inputs.Get("image"))
	return requiredUnlessUnknown(inputs, stringInput(image, "reference"), "image", "reference")
}

func checkEnvironment(inputs property.Map) error {
	environment := mapInput(inputs.Get("environment"))
	for _, key := range reservedEnvironment {
		if _, reserved := environment.GetOk(key); reserved {
			return fmt.Errorf("environment %q is reserved for values that Delivery sets", key)
		}
	}
	return nil
}

var stacksEntryName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// validateStacks checks what the provider must check: the entries are well formed and no two entries
// update the same stack. The service checks the rest.
func validateStacks(inputs property.Map) error {
	if unknown(inputs, "stacks") {
		return nil
	}
	entries := mapInput(inputs.Get("stacks")).AsMap()
	if len(entries) == 0 {
		return errors.New("stacks must contain at least one entry")
	}
	targets := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		if !stacksEntryName.MatchString(name) {
			return fmt.Errorf("stacks entry name %q must contain only letters, digits, '-' and '_'", name)
		}
		if entries[name].IsComputed() {
			continue
		}
		entry := mapInput(entries[name])
		fail := func(err error) error { return fmt.Errorf("stacks[%q]: %w", name, err) }
		stackName := stringInput(entry, "stack")
		if err := requiredUnlessUnknown(entry, stackName, "stack"); err != nil {
			return fail(err)
		}
		if stackName != "" {
			if parts := strings.Split(stackName, "/"); len(parts) != 3 || slices.Contains(parts, "") {
				return fail(fmt.Errorf("stack must be project/stack, not %q", stackName))
			}
			if other, dup := targets[stackName]; dup {
				return fmt.Errorf("entries %q and %q both update stack %s; a stack can appear once per Stacks resource", other, name, stackName)
			}
			targets[stackName] = name
		}
		if err := requireRef(entry, "source", sourceTypes...); err != nil {
			return fail(err)
		}
		if err := requiredUnlessUnknown(entry, stringInput(entry, "directory"), "directory"); err != nil {
			return fail(err)
		}
		if _, ok := entry.GetOk("runtime"); ok {
			if err := requireRef(entry, "runtime", "delivery:index:WorkflowsRuntime"); err != nil {
				return fail(err)
			}
		}
		if err := checkImage(entry); err != nil {
			return fail(err)
		}
		if err := checkEnvironment(entry); err != nil {
			return fail(err)
		}
	}
	return nil
}

// reservedEnvironment are the variables that the service sets in every job.
var reservedEnvironment = []string{"DELIVERY_RELEASE", "DELIVERY_RUN", "DELIVERY_STAGE", "DELIVERY_COMMIT", "DELIVERY_PIPELINE"}

// userLog is the part of the provider logger that reportDeclaration needs.
type userLog interface {
	Info(string)
	Warning(string)
}

// reportDeclaration shows the message and the warnings of the service to the user.
func reportDeclaration(log userLog, response cloudclient.DeclarationResponse) {
	if response.Message != "" {
		log.Info(response.Message)
	}
	for _, warning := range response.Warnings {
		log.Warning(warning)
	}
}

// logConstruct appends the state of a Construct response to the file in DELIVERY_CONSTRUCT_LOG, for the
// engine tests. It does nothing when the variable is unset.
func logConstruct(resourceURN urn.URN, resp p.ConstructResponse) {
	path := os.Getenv("DELIVERY_CONSTRUCT_LOG")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\nstate: %#v\n", resourceURN, redact(resp.State))
}

// redact replaces every secret value, at any depth, with "[secret]". Unknowns stay visible.
func redact(m property.Map) property.Map {
	out := make(map[string]property.Value, m.Len())
	for key, value := range m.AsMap() {
		out[key] = redactValue(value)
	}
	return property.NewMap(out)
}

func redactValue(value property.Value) property.Value {
	if value.Secret() {
		if value.IsComputed() {
			return value
		}
		return property.New("[secret]").WithDependencies(value.Dependencies())
	}
	switch {
	case value.IsMap():
		return property.New(redact(value.AsMap())).WithDependencies(value.Dependencies())
	case value.IsArray():
		items := make([]property.Value, 0, value.AsArray().Len())
		for _, item := range value.AsArray().AsSlice() {
			items = append(items, redactValue(item))
		}
		return property.New(property.NewArray(items)).WithDependencies(value.Dependencies())
	}
	return value
}

// value is the programSource of the Pipeline declaration. It leaves out the fields that are not set.
func (s ProgramSource) value() property.Map {
	m := map[string]property.Value{"url": property.New(s.URL)}
	if s.Branch != "" {
		m["branch"] = property.New(s.Branch)
	}
	if s.Directory != "" {
		m["directory"] = property.New(s.Directory)
	}
	return property.NewMap(m)
}
