package worker

import (
	"github.com/mudler/LocalAI/core/gallery"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Production incident (Jetson/Thor worker): deleting a backend returned HTTP
// 200 but left its gRPC process alive for ~40 minutes with its directory
// removed from disk. A later model load was routed to that orphan and failed
// with a certifi path pointing into the deleted directory.
//
// Root cause: s.processes is keyed by `modelID#replicaIndex`
// (buildProcessKey), so a delete keyed on the *backend* name resolved to no
// keys at all and the stop silently no-op'd. These specs pin the two lookups
// that must work by backend name.
var _ = Describe("Backend deletion reaps the backend's processes", func() {
	const (
		concrete    = "cuda13-nvidia-l4t-arm64-longcat-video"
		development = "cuda13-nvidia-l4t-arm64-longcat-video-development"
		alias       = "longcat-video"
	)

	Describe("resolveProcessKeysForBackend", func() {
		It("finds processes started for a backend even though they are keyed by modelID", func() {
			s := &backendSupervisor{
				processes: map[string]*backendProcess{
					// Started by a model load: key is modelID#replica, and the
					// backend name survives only in backendName.
					"LongCat-Video#0": {addr: "127.0.0.1:30232", backendName: concrete},
					"LongCat-Video#1": {addr: "127.0.0.1:30234", backendName: concrete},
					"other-model#0":   {addr: "127.0.0.1:30235", backendName: "llama-cpp"},
				},
			}

			Expect(s.resolveProcessKeysForBackend(setOf(concrete))).To(
				ConsistOf("LongCat-Video#0", "LongCat-Video#1"),
				"a delete keyed on the backend name must reach every process started for it")
		})

		It("matches through the alias the model config used at install time", func() {
			// The model config referenced `backend: longcat-video` (the alias),
			// so the process recorded the alias; the delete request carries the
			// concrete directory name. Without alias resolution the orphan
			// survives exactly as it did in production.
			s := &backendSupervisor{
				processes: map[string]*backendProcess{
					"LongCat-Video#0": {addr: "127.0.0.1:30232", backendName: alias},
				},
			}

			Expect(s.resolveProcessKeysForBackend(setOf(concrete, alias))).To(
				ConsistOf("LongCat-Video#0"),
				"alias and concrete name must resolve to the same process")
		})

		It("does not reap processes belonging to a different backend", func() {
			s := &backendSupervisor{
				processes: map[string]*backendProcess{
					"LongCat-Video#0": {addr: "127.0.0.1:30233", backendName: development},
				},
			}

			Expect(s.resolveProcessKeysForBackend(setOf(concrete))).To(BeEmpty(),
				"deleting the non-development backend must not kill the -development process")
		})

		It("still resolves legacy entries that predate backendName via the modelID prefix", func() {
			// Installs with an empty modelID key the map by the backend name
			// itself (buildProcessKey falls back). Those must keep working.
			s := &backendSupervisor{
				processes: map[string]*backendProcess{
					concrete + "#0": {addr: "127.0.0.1:30232"},
				},
			}

			Expect(s.resolveProcessKeysForBackend(setOf(concrete))).To(ConsistOf(concrete + "#0"))
		})
	})

	Describe("resolveStopTargets", func() {
		// backend.stop is ambiguous by design: the admin backends UI publishes
		// a BACKEND name, UnloadRemoteModel publishes a MODEL name, and the
		// router's abandoned-load reap (#10948) publishes an exact
		// modelID#replica key. Resolving only one of the three silently
		// strands the others.
		newSupervisor := func() *backendSupervisor {
			return &backendSupervisor{
				processes: map[string]*backendProcess{
					"LongCat-Video#0": {addr: "127.0.0.1:30232", backendName: concrete},
				},
			}
		}

		It("stops a process addressed by model ID", func() {
			Expect(newSupervisor().resolveStopTargets("LongCat-Video")).To(ConsistOf("LongCat-Video#0"),
				"model-name stop (UnloadRemoteModel) must still reach the process")
		})

		It("stops a process addressed by backend name", func() {
			Expect(newSupervisor().resolveStopTargets(concrete)).To(ConsistOf("LongCat-Video#0"),
				"backend-name stop must reach the process too")
		})

		It("stops a process addressed by an exact replica key", func() {
			Expect(newSupervisor().resolveStopTargets("LongCat-Video#0")).To(ConsistOf("LongCat-Video#0"),
				"the router reaps an abandoned load by exact modelID#replica key")
		})

		It("does not stop unrelated processes", func() {
			Expect(newSupervisor().resolveStopTargets("some-other-model")).To(BeEmpty())
		})
	})

	Describe("processMatchesBackend", func() {
		It("rejects reusing a process that was started for a different backend", func() {
			// The install fast path returns the address of any live process
			// under this processKey. After the concrete backend was deleted and
			// the -development variant installed, the same model+replica got
			// the deleted backend's port handed back to it.
			s := &backendSupervisor{
				processes: map[string]*backendProcess{
					"LongCat-Video#0": {addr: "127.0.0.1:30232", backendName: concrete},
				},
			}

			Expect(s.processMatchesBackend("LongCat-Video#0", development)).To(BeFalse(),
				"a process started for the deleted backend must not be reused for another backend")
		})

		It("accepts reusing a process started for the same backend", func() {
			s := &backendSupervisor{
				processes: map[string]*backendProcess{
					"LongCat-Video#0": {addr: "127.0.0.1:30232", backendName: concrete},
				},
			}

			Expect(s.processMatchesBackend("LongCat-Video#0", concrete)).To(BeTrue())
		})

		It("accepts a process recorded under the alias of the requested backend", func() {
			s := &backendSupervisor{
				processes: map[string]*backendProcess{
					"LongCat-Video#0": {addr: "127.0.0.1:30232", backendName: alias},
				},
			}

			Expect(s.processMatchesBackend("LongCat-Video#0", alias)).To(BeTrue())
		})

		It("accepts legacy entries with no recorded backend name", func() {
			// Pre-upgrade processes carry no backendName. Treating them as a
			// mismatch would restart every running backend once on rollout.
			s := &backendSupervisor{
				processes: map[string]*backendProcess{
					"LongCat-Video#0": {addr: "127.0.0.1:30232"},
				},
			}

			Expect(s.processMatchesBackend("LongCat-Video#0", concrete)).To(BeTrue())
		})
	})

	Describe("backendIdentitySet", func() {
		It("maps an alias to its concrete backend and back", func() {
			backends := gallery.SystemBackends{
				concrete: {Name: concrete, Metadata: &gallery.BackendMetadata{Name: concrete, Alias: alias}},
				alias:    {Name: alias, Metadata: &gallery.BackendMetadata{Name: concrete, Alias: alias}},
			}

			Expect(backendIdentitySet(backends, concrete)).To(HaveKey(alias))
			Expect(backendIdentitySet(backends, alias)).To(HaveKey(concrete))
		})

		It("falls back to the bare name when the backend is unknown", func() {
			// A delete must never fail (or over-reach) because the gallery
			// listing is unavailable or the entry is already gone from disk.
			Expect(backendIdentitySet(gallery.SystemBackends{}, concrete)).To(
				Equal(map[string]struct{}{concrete: {}}))
		})

		It("does not conflate two concrete backends sharing an alias", func() {
			// Both variants declare alias "longcat-video"; ListSystemBackends
			// picks one for the alias row. Deleting the non-chosen concrete
			// must not pull in the chosen one's identity.
			backends := gallery.SystemBackends{
				development: {Name: development, Metadata: &gallery.BackendMetadata{Name: development, Alias: alias}},
				alias:       {Name: alias, Metadata: &gallery.BackendMetadata{Name: development, Alias: alias}},
			}

			set := backendIdentitySet(backends, development)
			Expect(set).To(HaveKey(development))
			Expect(set).ToNot(HaveKey(concrete))
		})
	})
})

// setOf builds the identity set the resolver consumes, keeping the specs
// readable without repeating the map literal.
func setOf(names ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	return set
}
