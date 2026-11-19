package gallery

import (
	"context"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/system"
)

type ArtifactMaterializer interface {
	Ensure(context.Context, string, modelartifacts.Spec) (modelartifacts.Result, error)
}

type installOptions struct {
	materializer        ArtifactMaterializer
	variant             string
	backendAvailability BackendAvailability
	// origin, when set, is recorded in the gallery file (version 2).
	origin *Origin
	// instance names the frontend that writes the install journal.
	instance string
	// modelLock, when set, is taken around the commit of the install.
	modelLock ModelLock
	// skipArtifactBinding makes PlanInstall leave the artifact unbound, so a
	// check can plan without materializing anything.
	skipArtifactBinding bool
}

type InstallOption func(*installOptions)

// WithBackendAvailability supplies the execution target's view of installed
// backends; absent, the local host's.
func WithBackendAvailability(available BackendAvailability) InstallOption {
	return func(options *installOptions) {
		if available != nil {
			options.backendAvailability = available
		}
	}
}

func WithArtifactMaterializer(materializer ArtifactMaterializer) InstallOption {
	return func(options *installOptions) {
		if materializer != nil {
			options.materializer = materializer
		}
	}
}

// WithVariant pins a gallery entry to a specific variant by name, bypassing
// hardware-based selection. The entry's own name is a valid pin, since the
// entry is itself the last resort. Ignored for entries that declare no
// variants.
func WithVariant(variant string) InstallOption {
	return func(options *installOptions) {
		options.variant = variant
	}
}

// WithOrigin records the gallery entry an install comes from in its gallery
// file.
func WithOrigin(origin *Origin) InstallOption {
	return func(options *installOptions) {
		options.origin = origin
	}
}

// ModelLock takes the cluster-wide lock of one installed model without
// waiting, for a change of its config and gallery file (an install commit, an
// upgrade, a hold, the removal of an old version). It returns ErrModelLocked
// when another holder has it; release frees it. While a frontend holds it, the
// journal of the model is live: another frontend takes the lock before it
// finishes or undoes an abandoned journal, so it never touches a running one.
type ModelLock func(ctx context.Context, name string) (release func(), err error)

// ErrModelLocked is returned by a ModelLock that another holder has.
var ErrModelLocked = errors.New("another change of this model is running")

// WithModelLock takes lock around the commit of the install. A busy lock
// fails the install before it writes the config.
func WithModelLock(lock ModelLock) InstallOption {
	return func(options *installOptions) {
		options.modelLock = lock
	}
}

// WithInstance names the frontend that runs the install, for its journal.
func WithInstance(instance string) InstallOption {
	return func(options *installOptions) {
		options.instance = instance
	}
}

// WithoutArtifactBinding makes PlanInstall skip binding the primary artifact,
// which would materialize it. The update check plans with it.
func WithoutArtifactBinding() InstallOption {
	return func(options *installOptions) {
		options.skipArtifactBinding = true
	}
}

// BindPlanArtifact binds the primary artifact of a plan made with
// WithoutArtifactBinding: it materializes the artifact and rewrites the plan
// config with the resolved spec. It reports whether the config changed. A
// plan of an entry that lists files, writes no config or has no artifact is
// left as it is.
func BindPlanArtifact(ctx context.Context, systemState *system.SystemState, plan *InstallPlan, options ...InstallOption) (bool, error) {
	if plan == nil || !plan.WriteConfig || len(plan.Gallery.Files) > 0 {
		return false, nil
	}
	installOptions := applyInstallOptions(options...)
	basePath := systemState.Model.ModelsPath
	spec, inferred, ok, err := plan.Model.PrimaryArtifactSpec(basePath)
	if err != nil || !ok {
		return false, err
	}
	var configMap map[string]any
	if err := yaml.Unmarshal(plan.ConfigYAML, &configMap); err != nil {
		return false, err
	}
	applied, err := bindPrimaryArtifact(ctx, basePath, &plan.Model, configMap, installOptions.materializer, spec, inferred)
	if err != nil || !applied {
		return false, err
	}
	out, err := yaml.Marshal(configMap)
	if err != nil {
		return false, fmt.Errorf("failed to marshal config with the bound artifact: %w", err)
	}
	plan.ConfigYAML = out
	return true, nil
}

func applyInstallOptions(options ...InstallOption) installOptions {
	result := installOptions{materializer: modelartifacts.NewDefaultManager()}
	for _, option := range options {
		option(&result)
	}
	return result
}

func bindPrimaryArtifact(ctx context.Context, modelsPath string, typed *config.ModelConfig, configMap map[string]any, materializer ArtifactMaterializer, artifactSpec modelartifacts.Spec, inferred bool) (bool, error) {
	result, err := materializer.Ensure(ctx, modelsPath, artifactSpec)
	if err != nil {
		if inferred {
			config.LogArtifactFallback(typed.Name, typed.Backend, err)
			return false, nil
		}
		return false, fmt.Errorf("materialize primary model artifact: %w", err)
	}
	next := []modelartifacts.Spec{result.Spec}
	if len(typed.Artifacts) > 1 {
		next = append(next, typed.Artifacts[1:]...)
	}
	typed.Artifacts = next
	// Companions are always explicit, so there is no legacy path to degrade to:
	// failing here leaves any previously installed config untouched rather than
	// persisting a half-acquired model that would fail later inside the backend.
	for i := 1; i < len(typed.Artifacts); i++ {
		if typed.Artifacts[i].Target != modelartifacts.TargetCompanion {
			continue
		}
		companion, err := materializer.Ensure(ctx, modelsPath, typed.Artifacts[i])
		if err != nil {
			return false, fmt.Errorf("materialize companion artifact %q: %w", typed.Artifacts[i].Name, err)
		}
		typed.Artifacts[i] = companion.Spec
	}
	artifactYAML, err := yaml.Marshal(typed.Artifacts)
	if err != nil {
		return false, err
	}
	var artifactValue any
	if err := yaml.Unmarshal(artifactYAML, &artifactValue); err != nil {
		return false, err
	}
	configMap["artifacts"] = artifactValue
	return true, nil
}
