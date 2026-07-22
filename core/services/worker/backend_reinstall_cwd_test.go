package worker

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Production incident (Jetson Thor worker, distributed mode): after two
// successive reinstalls of a backend, a later model load failed with
//
//	rpc error: code = Internal desc = failed to load LongCat model: [Errno 2] No such file or directory
//
// raised from inside the backend's `import torch`, before any model file was
// touched — torch's custom-op registration calls inspect.getmodule ->
// os.path.abspath, and abspath calls getcwd(2) for a relative path.
//
// gallery.InstallBackend replaces a backend by renaming the live directory to
// `<name>.install-backup`, moving the staged directory into place, then
// deleting the backup. A working directory follows the inode across a rename,
// so a backend process that outlives that swap ends up with a deleted inode as
// its CWD and every getcwd(2) in it fails with ENOENT. Scanning /proc inside
// the worker container found exactly that:
//
//	pid 23467 CWD DELETED: /backends/cuda13-nvidia-l4t-arm64-longcat-video-development.install-backup (deleted)
//
// Python backends import torch lazily inside LoadModel, so such a survivor
// looks perfectly healthy — it answers HealthCheck and keeps its gRPC port —
// and only detonates when a model is actually loaded through it. Restarting
// the worker container cleared the condition.
//
// The install paths do stop running processes before replacing the directory
// (installBackend's force branch, upgradeBackend, backend.delete), but they
// resolve them by *name*. That bookkeeping reaps nothing whenever the recorded
// name no longer resolves into the install's identity set: a legacy entry with
// an empty backendName, a ListSystemBackends failure degrading backendIdentity
// to name-only matching, or an earlier reinstall having already rewritten the
// metadata.json that carries the alias. Each of those leaves a live process
// whose directory is about to be unlinked — and nothing downstream notices,
// because the reuse gate checks liveness and name, and the name is precisely
// what does NOT change across a reinstall.
//
// These specs pin the missing invariant: a process may only be reused if the
// directory it is running out of is still the installed one.
var _ = Describe("Backend reinstall must not poison later model loads", func() {
	const (
		backendName = "cuda13-nvidia-l4t-arm64-longcat-video-development"
		processKey  = "LongCat-Video#0"
	)

	var backendDir string

	BeforeEach(func() {
		backendDir = filepath.Join(GinkgoT().TempDir(), backendName)
		Expect(os.MkdirAll(backendDir, 0o750)).To(Succeed())
	})

	// newSupervisor mirrors startBackend: it records the backend directory and
	// its identity while that directory is still the live one.
	newSupervisor := func() *backendSupervisor {
		info, err := os.Stat(backendDir)
		Expect(err).ToNot(HaveOccurred())
		return &backendSupervisor{
			processes: map[string]*backendProcess{
				processKey: {
					addr:         "127.0.0.1:30232",
					backendName:  backendName,
					backendDir:   backendDir,
					backendDirID: info,
				},
			},
		}
	}

	// reinstall reproduces gallery.InstallBackend's atomic swap verbatim:
	// rename the live directory aside, move the staged one into place, delete
	// the backup. The path is unchanged afterwards; the inode is not.
	reinstall := func() {
		backup := backendDir + ".install-backup"
		Expect(os.Rename(backendDir, backup)).To(Succeed())
		Expect(os.MkdirAll(backendDir, 0o750)).To(Succeed())
		Expect(os.RemoveAll(backup)).To(Succeed())
	}

	Describe("processMatchesBackend", func() {
		It("refuses to reuse a process whose backend directory a reinstall replaced", func() {
			s := newSupervisor()
			reinstall()

			Expect(s.processMatchesBackend(processKey, backendName)).To(BeFalse(),
				"the process is running out of a deleted inode: its CWD no longer resolves, "+
					"so reusing it hands the next load a backend that fails inside import torch")
		})

		It("refuses to reuse a process whose backend directory was removed outright", func() {
			s := newSupervisor()
			Expect(os.RemoveAll(backendDir)).To(Succeed())

			Expect(s.processMatchesBackend(processKey, backendName)).To(BeFalse(),
				"a backend delete that missed this process leaves it in the same unusable state")
		})

		It("reuses a process whose backend directory is untouched", func() {
			Expect(newSupervisor().processMatchesBackend(processKey, backendName)).To(BeTrue(),
				"the common case must stay on the fast path")
		})

		It("reuses processes that predate directory recording", func() {
			// Entries created before this field exists carry no recorded
			// directory. Treating them as mismatched would restart every
			// running backend once on rollout.
			s := &backendSupervisor{
				processes: map[string]*backendProcess{
					processKey: {addr: "127.0.0.1:30232", backendName: backendName},
				},
			}

			Expect(s.processMatchesBackend(processKey, backendName)).To(BeTrue())
		})
	})
})
