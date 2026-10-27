# sandbox

Run an agent harness's shell commands in an isolated guest. The guest sees a workspace view as files. The backend commits what the guest changes back to the workspace as change sets.

## Packages

| Package | What it does |
|---|---|
| `sandbox` | The `Backend` and `Sandbox` interfaces, `Spec`, `Cmd`, `Process`, the errors `ErrUnsupported`, `ErrGone` and `ErrBusy`, and `UpperIndex`, a helper that makes repeated checkpoints of one upper layer correct |
| `sandbox/sbtest` | `RunBackendSuite`, the conformance suite that every backend must pass; `EchoHandler` and `ConnectProxy`, port handlers for tests |
| `sandbox/guestagent` | The guest agent (PID 1 in the guest) and the host side of its vsock protocol. Standard library only |
| `sandbox/chv` | The Cloud Hypervisor backend. It is a nested module with its own `go.mod` |
| `sandbox/ociimage` | Image code that `chv` and `gvisor` share: references with digests, the checks of an OCI layout, the cache per digest. A nested module |
| `sandbox/gvisor` | The gVisor backend for hosts without KVM. A nested module |
| `sandbox/images/guest` | Dockerfile of the guest image: Debian, Python with the Paper office libraries (`docx`, `openpyxl` and `pptx` imports) and the PDF libraries, Poppler, the guest kernel and the agent |
| `sandbox/images/host` | Dockerfile of the sandbox host image: pinned `cloud-hypervisor`, `virtiofsd`, `bwrap` and e2fsprogs |

## How a sandbox works

- `Create` builds a read-only tree of the view with `workspace.Store.Materialize`. This is the lower layer. It also makes an empty ext4 image, the upper layer, and boots the guest. The guest mounts an overlay of both at `/workspace`.
- `Exec` runs a process as user `agent` (uid 1000), with pipes or a terminal. The process gets the image environment plus `Cmd.Env`. The home folder of `agent` is an overlay too. Writes to it stay until the next `Refresh`.
- `Checkpoint` freezes the guest file system, reads the upper image on the host, thaws the guest and commits the changes as one change set. Call it at the end of each agent turn, on demand, or on a timer for long background runs.
- `Refresh` and `Suspend` run a checkpoint of their own and drop its conflicts, for example those with the reasons `read-only`, `unsupported` and `bad-name`. A caller that needs the conflicts runs `Checkpoint` first.
- A file that the guest edits again after an earlier checkpoint is not a conflict. An edit of a file that another writer changed is a conflict copy. The guest keeps its own version until the next `Refresh`.
- `Refresh` checkpoints, rebuilds the lower layer from the current revision, clears the upper layer and restarts the guest. **Processes do not survive a refresh.** When the checkpoint fails, `Refresh` changes nothing.
- `Suspend` checkpoints and saves the guest memory and devices. A `Suspend` that fails after it detached the guest processes leaves them detached, so the end of the host context does not kill them later. `Resume` restores them: processes continue, also background ones, and open files keep working.
- `Destroy` stops the guest and deletes its files. Committed change sets stay. Uncommitted changes are lost.

## Guest

The bullets that say `chv` apply to that backend only. The gVisor backend has its own values in its section.

- Processes run as uid 1000 with only `CAP_DAC_OVERRIDE` and `CAP_FOWNER` as ambient capabilities, and with `no_new_privs`. The lower layer holds read-only files, so these two capabilities let the agent edit them.
- `/sys` and `/proc/sys` are read-only.
- The kernel has no modules. It has no coredump, no sysrq, no loop devices, no `/dev/mem` and no debugfs.
- The guest has no network device. Under `chv` it reaches the host only through the vsock ports that the spec configures. Under `gvisor` it has loopback only, and the host serves the ports through Unix sockets.
- A kernel panic is detected from the console log, so the backend reports it as a failed boot or a gone sandbox. After boot, the agent removes the console, kernel log (`/dev/console`, `/dev/hvc*`, `/dev/kmsg`) and `/dev/tty<N>` nodes, so a guest process cannot write the console log or print the panic line. `/dev/tty` stays: it is the caller's own controlling terminal.
- `chv` only: the host caps `console.log` at 16 MiB. When the file grows past the cap, for example in a kernel message flood, the backend kills the VM, marks the sandbox gone and keeps the last 64 KiB of the log for diagnosis. Calls then return `ErrGone`.
- `chv` only: the agent serves only the host on its vsock port. It closes a connection from any other peer, such as a process in the guest that connects through a local address.
- A sandbox runs at most 64 port handler connections at once, over all its ports. The guest agent closes the 65th and later connections at once, until a handler finishes.

### Stdin

Stdin to a process is flow-controlled with a 2 MiB window. The host sends at most 2 MiB before the guest confirms what the process has read. A slow reader slows the writer. It is never killed for being slow.

## Checkpoints

- `chv`: the guest auto-thaws 60 s after a freeze. A checkpoint whose upper image read takes longer than 45 s is refused. It commits nothing and returns an error that says to retry. A `Refresh` refuses to swap the layers in the same case. It thaws the guest and keeps the sandbox running. `gvisor` pauses the sandbox instead of freezing a disk, with the same 45 s limit and the same result.
- The whole set of files that a checkpoint sends is staged on the host, within `Options.StageLimit`, before the commit starts. The default under `chv` is the size of the upper image, and under `gvisor` it is twice `Spec.Disk`. A set over the limit fails the checkpoint.
- A file name that the workspace refuses becomes a conflict with the reason `bad-name`. A later `Refresh` drops that file from the upper layer. It leaves no other trace, so save the file elsewhere before a `Refresh` if you need it.
- A checkpoint reads at most 512 directory levels and 1,000,000 entries of the upper layer, under both backends. A layer over either cap fails the whole checkpoint with an error that names the cap. Nothing is committed.
- `chv` only: a structurally hostile upper image fails the checkpoint. The backend checks each record against the superblock and the name rules, and refuses the whole image.

## Snapshots

- `Resume` works on a snapshot again, after a failed resume. The snapshot disk is copied, not moved.
- `Resume` needs the same actor and a handler for each port that the sandbox was created with. The backend cannot check that the store is the same one. The caller must pass the same store.
- `state.json` of a session folder has an `epoch`. `Resume` adds 1 to it under the folder lock, and the new handle owns that epoch. `Checkpoint`, `Refresh`, `Suspend` and every save of `state.json` take the folder lock and compare epochs. A handle that `Suspend` released, or that another `Resume` replaced, gets an error that wraps `ErrGone` and changes nothing. Its `Destroy` stops what it runs itself and leaves the folder alone, so it never deletes the resumed sandbox or a later snapshot.

## Network

For each entry of `Env.Ports`, the guest listens on `127.0.0.1:<port>`. The host handler serves each connection. The guest image sets `HTTP_PROXY` and `HTTPS_PROXY` to `http://127.0.0.1:3128`, and it writes the same proxy into the pip, apt and git configuration. A handler on port 3128 therefore sees all web traffic. Tools that ignore the proxy settings fail. With no handler on port 3128, the connection closes at once.

## Images

- Every image reference needs a digest: `name@sha256:...`, or `oci-layout:<folder>@sha256:...` for a local OCI layout.
- The backend checks every blob of an `oci-layout:` image against its digest. It checks the manifest, the config and each layer.
- The layout folder must not be writable by untrusted users. The backend checks the blobs and then reads them again, so a writer could swap a blob between the two steps.
- The backend converts each image once and keeps the result in `CacheDir`, by digest.

## Cloud Hypervisor backend

```go
b, err := chv.New(chv.Options{StateDir: stateDir, CacheDir: cacheDir, DefaultImage: guestImage})
if errors.Is(err, sandbox.ErrUnsupported) {
	// no /dev/kvm or a host binary is missing: fall back to another backend
}
sb, err := b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: store, Actor: actor}, View: view})
```

- Host requirements: `/dev/kvm` with read and write access, `cloud-hypervisor` 54 or later, `virtiofsd` 1.13 or later, `bwrap` 0.9 or later and e2fsprogs 1.47 or later. `New` checks them. It returns `sandbox.ErrUnsupported` and names the first missing piece.
- The backend refuses to run as root. Run it as an unprivileged user.
- `StateDir` must be short. A Unix socket path under it has at most 107 bytes. The path must not contain a comma.
- virtiofsd and debugfs run inside bubblewrap. A product that runs the backend in a container sets `chv.Options.NoBwrap`. Bubblewrap cannot make namespaces in a default container, so the container is then the isolation of virtiofsd and debugfs. The sandbox host image sets `SANDBOX_NO_BWRAP=1`. This is only a convention: the backend does not read it. A product can read it and pass `NoBwrap` itself.
- With `NoBwrap`, two guards are gone. debugfs then parses the hostile upper image with the privileges of the product itself, and the only guard on the shared lower tree is the `--readonly` flag of virtiofsd. Use `NoBwrap` only in a container that isolates both programs.
- `Options.ReadyTimeout` is how long `Create`, `Refresh` and `Resume` wait for the guest. The default is 10 s. Set it higher on a slow host or in a container.
- A host crash leaves the session folders under `StateDir` behind. The backend does not sweep them. The product removes stale folders.

### In a container

The sandbox host image is built for containers. Run it with these flags:

```sh
docker run --device /dev/kvm --group-add "$(stat -c %g /dev/kvm)" ...
```

- `--device /dev/kvm` is required. Add `--group-add <gid of /dev/kvm>` when the device is not world-writable, as is usual.
- Boots are much slower in a container. In the host image, `TestInitExitFailsFast` took 9.2 s and `TestGuestHardening` took 5.9 s. On the host they took about 0.8 s and 0.9 s. `TestConvertedImageBootsWithFileKinds` took 25 s in the container and 2.2 s on the host. Set timeouts with room for this.
- `--tmpfs /tmp:exec,mode=1777` makes boots faster. It brought the two tests to 4.0 s and 3.8 s.
- `--cap-drop=ALL --security-opt no-new-privileges` is optional hardening. The tests pass with it.
- Do not turn off seccomp (`seccomp=unconfined`), not even for speed. The isolation depends on it.

## gVisor backend

For hosts without `/dev/kvm`: a laptop where the user is not in the `kvm` group, or a cloud VM without nested virtualization. It passes the same `sbtest` suite as `chv`, with the same guest image and guest agent, and with weaker limits.

```go
b, err := gvisor.New(gvisor.Options{StateDir: stateDir, CacheDir: cacheDir, Runsc: "/opt/gvisor/runsc", DefaultImage: guestImage})
if errors.Is(err, sandbox.ErrUnsupported) {
	// the error says why: no runsc, an old release, no user namespace, ...
}
```

- Host requirements: Linux, `runsc` release 20261012.0 or later with its `gvisor-bin` folder next to it, `taskset`, and one administrator step: an AppArmor profile that lets that `runsc` binary make user namespaces. `gvisor-profile /opt/gvisor/runsc` prints it; load it with `apparmor_parser -r`. The profile names the binary by path, so each `runsc` upgrade to a new path needs a new profile. `New` returns `ErrUnsupported`, with a one-line reason, when it runs as root, when `runsc` is missing, too old or has no `gvisor-bin` folder, when the path of `runsc` fails `gvisor.ValidateProfilePath`, when `taskset` or `/bin/true` is missing or the allowed CPU list is empty, when `StateDir` is too long for socket paths, and when `runsc` cannot make a user namespace or cannot start a sandbox. The user namespace case names `gvisor.Profile`; the other two name the folder of the probe. A missing `StateDir` or `CacheDir` option, and a `StateDir` that another backend holds, are plain errors, not `ErrUnsupported`.
- `runsc` and every sandbox process run as the calling user. The backend refuses to run as root.
- `StateDir` must be short: a Unix socket path under it has at most 107 bytes.
- Weaker isolation than `chv`: gVisor is a user-space kernel, not a VM.
- The limits are watched, not enforced by the kernel. CPUs: `runsc` runs under `taskset` with `Spec.CPUs` CPUs. Memory: the backend reads the sandbox's memory use every second, and three readings in a row over `Spec.Memory` stop the sandbox; calls then return `ErrGone` and the log says `memory limit`. Disk: every process has a file size limit of `Spec.Disk`, and the backend adds up the upper layer every 10 seconds; over `Spec.Disk`, `Exec` returns `gvisor.ErrDiskFull` ("The sandbox disk is full.") until a `Refresh` gives a new upper layer. A checkpoint does not shrink the upper layer. `Refresh` clears a full disk.
- File I/O is slower than under `chv`. The backend runs `runsc` with `--directfs=false`. The cost of that is not measured.
- A failed `Suspend` usually keeps the sandbox running, or starts it again from the checkpoint. When that fails too, the sandbox is gone and calls return `ErrGone`. In both cases the process handles from before are gone. New port connections are refused while `Suspend` runs.
- `Destroy` stops the sandbox with `runsc delete --force`.
- **Sandboxes outlive the process.** A `runsc` sandbox keeps running when the product that started it is killed, runs out of memory or loses its terminal. Nothing watches its limits then. `New` therefore takes an exclusive lock on `StateDir` (the file `.lock` in it) for the life of the backend, and a second `New` on the same `StateDir` fails with a plain error. The next `New` on that folder stops the sandboxes that the earlier process left: every session folder whose `state.json` is missing, unreadable or not `suspended` gets `runsc delete --force` and is removed. Suspended folders stay, because they hold a checkpoint that `Resume` uses. A folder whose sandbox cannot be stopped stays too, with a log line. `Backend.Close` releases the lock; it does not stop sandboxes. Use one `StateDir` per backend.
- Use one `CacheDir` per backend. `chv` and `gvisor` both name a cache folder by digest and put different files in it, so a folder that `chv` built is not usable by `gvisor`.
- Checkpoints: the caps of the Checkpoints section hold for `gvisor`: 512 directory levels and 1,000,000 entries, and `Options.StageLimit` (default twice `Spec.Disk`). A command can make millions of empty files, which use no disk blocks, so the disk watcher also counts entries: at the entry cap, `Exec` returns `ErrDiskFull`. A layer over a cap cannot be checkpointed, and `Refresh` checkpoints first, so such a sandbox can only be destroyed.
- `console.log` of a sandbox is not capped. The guest cannot write it directly (the agent is not dumpable and commands get pipes), but a restore renames it to `console.prev.log` and the next boot removes it.
- A `Resume` that fails after the restore (the agent does not answer, or the state cannot be saved) keeps the checkpoint for a retry. Processes of the first restore have run against the live upper layer by then, so a retry restores memory that is older than the files: for example, an append can happen twice.
- Every file of the image belongs to the calling user, which the sandbox sees as uid 1000. The unpacked image keeps its file modes on disk; the read-only root mount protects it. Setuid and setgid bits are dropped, there are no device nodes (the sandbox has its own `/dev`), and fifos in the image are not kept.
- Each sandbox folder under `StateDir` keeps `console.log`, the standard output and error of the guest agent.

## Testing a backend

```go
func TestSuite(t *testing.T) {
	sbtest.RunBackendSuite(t, func(t *testing.T) sandbox.Backend { return newBackend(t) })
}
```

The `chv` tests need `SANDBOX_TEST_IMAGE` (the guest image, with a digest) and a host that can run the backend. They skip and say why when either is missing. The `gvisor` tests need `SANDBOX_TEST_IMAGE` and `SANDBOX_TEST_RUNSC` (the `runsc` binary, with the AppArmor profile loaded). They skip when either is not set; when both are set, a host that cannot run the backend fails them. A guest image from before the gVisor mode of the agent does not work: build the image again, or add a fresh agent to it with `sandbox/ociimage/cmd/ocipatch`.

## Releasing the images

A release tag `vX.Y.Z` on `main` makes CI push both images for `linux/amd64` and `linux/arm64`. CI writes their digests to the job summary. Tags that are not on `main` are not published.

To release the chv backend:

1. Put the guest digest in the release notes.
2. Set `chv.DefaultImage` to `ghcr.io/<owner>/<repo>/sandbox-guest@sha256:...` in a commit after the release.
3. Replace the `replace` line in `sandbox/chv/go.mod` with the root module version `vX.Y.Z`.
4. Tag `sandbox/chv/vX.Y.Z` on that commit.

The same steps release `sandbox/ociimage` (tag `sandbox/ociimage/vX.Y.Z`, first) and `sandbox/gvisor` (tag `sandbox/gvisor/vX.Y.Z`): replace the two `replace` lines in each `go.mod` with the tagged root and `ociimage` versions. `sandbox/chv` needs the `ociimage` tag too.
