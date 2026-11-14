// SPDX-License-Identifier: MIT

// Command sandbox is the executable of the Sandbox add-on. It runs the code
// of a chat in an isolated sandbox; the files of the user stay with the host,
// which hands them over through the workspace operations.
package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/addons/sandbox/internal/egress"
	"github.com/mudler/LocalAI/addons/sandbox/internal/profiles"
	"github.com/mudler/LocalAI/addons/sandbox/internal/remote"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage"
	"github.com/mudler/LocalAI/addons/sandbox/internal/tools"
	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/hostclient"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
)

// hostEgress asks the host about the connections of the sandboxes, over the
// host operations egress.check and egress.audit of the live call.
type hostEgress struct{ c *hostclient.Client }

func (h hostEgress) Check(ctx context.Context, op string, a egress.CheckArgs) (egress.Verdict, error) {
	var v egress.Verdict
	err := h.c.Do(ctx, op, "egress.check", a, &v)
	return v, err
}

func (h hostEgress) Audit(ctx context.Context, op string, evs []egress.Event) error {
	return h.c.Do(ctx, op, "egress.audit", map[string]any{"events": evs}, nil)
}

// hostAdmin asks the host whether the user of a request is an administrator.
type hostAdmin struct{ c *hostclient.Client }

func (a hostAdmin) IsAdmin(ctx context.Context, operationID string) bool {
	return a.c.Do(ctx, operationID, "workspace.admin", nil, nil) == nil
}

func main() {
	os.Exit(protocol.Run(os.Args, func(s protocol.Startup) contract.Runtime {
		client := hostclient.New(s)
		sel, err := newSelector(s.DataDir)
		if err != nil {
			// Without settings no sandbox runs; every tool says so.
			xlog.Error("sandbox: cannot read the settings", "error", err)
			return tools.New(unavailable{}, nil, hostAdmin{client})
		}
		// The network proxy of the sandboxes: guest port 3128 of every sandbox,
		// whatever the chat's preset (the host decides each connection).
		// Without a host channel nothing can decide, so no port is offered.
		var ports func(user, conv string) map[int]sandbox.PortHandler
		var begin func(ctx context.Context, user, conv, op string) func()
		var opts []tools.Option
		if client != nil {
			proxy := egress.New(egress.Options{Host: hostEgress{client}})
			ports, begin = proxy.Ports, proxy.Begin
			// A background job reaches the network under its own operation,
			// which the host keeps live for it (the job lease).
			opts = append(opts, tools.WithJobEgress(proxy))
		}
		store := remote.New(client, s.DataDir)
		imgs, err := releaseImages()
		if err != nil {
			xlog.Error("sandbox: the profile images of this build are not valid", "error", err)
			return tools.New(unavailable{}, nil, hostAdmin{client})
		}
		var m *runner.Manager
		deliv, err := profiles.New(profiles.Options{
			DataDir:  s.DataDir,
			Declared: profiles.Declared(),
			Images:   imgs,
			Cache:    sel.ImageCache,
			// Both backends keep their own copy of an image; a backend that
			// was used before may still hold one.
			ForgetOld: func(ctx context.Context, ref string) {
				for _, dir := range []string{"kvm", "gvisor"} {
					if err := ociimage.Forget(ctx, filepath.Join(s.DataDir, "images", dir), ref); err != nil {
						xlog.Warn("sandbox: cannot delete an old image", "image", ref, "backend", dir, "error", err)
					}
				}
			},
			InUse: func() map[string]bool {
				if m == nil {
					return nil
				}
				return m.ImagesInUse()
			},
		})
		if err != nil {
			xlog.Error("sandbox: cannot read the image record", "error", err)
			return tools.New(unavailable{}, nil, hostAdmin{client})
		}
		m, err = runner.New(runner.Options{
			Ports:   ports,
			Begin:   begin,
			Backend: sel.Backend,
			Store:   store,
			// The host binds each workspace operation to the call's chat and
			// mounts only that chat's folders (a scoped chat: the agent's
			// folder at home and the chat folder at chat).
			Actor: remote.Actor,
			View:  store.HomeView,
			// The backends keep their sandboxes under run/. The manager needs the
			// folder to remove the snapshots it gives up, and leaves it alone at
			// start because each backend stops the sandboxes of an earlier
			// process itself before it removes their folders.
			StateDir:     filepath.Join(s.DataDir, "run"),
			KeepStateDir: true,
			Tools:        tools.Runs,
			MissingTool:  func(profile string) string { return sandbox.MissingToolNote(profile, profiles.Declared()) },
			Profile: func(ctx context.Context, r runner.Request) (runner.Choice, error) {
				c, err := deliv.Resolve(ctx, r.Call.Profile, r.Call.Pin, profiles.Caps{MemoryMiB: r.Call.MemoryMiB, DiskMiB: r.Call.DiskMiB})
				return runner.Choice{ID: c.ID, Image: c.Image, CPUs: c.CPUs, Memory: c.Memory, Disk: c.Disk, Notice: c.Notice}, err
			},
			Prepare: func(ctx context.Context, r runner.Request) context.Context {
				return remote.WithOperation(ctx, r.Operation)
			},
		})
		if err != nil {
			xlog.Error("sandbox: cannot start the manager", "error", err)
			return tools.New(unavailable{}, nil, hostAdmin{client})
		}
		m.Start(context.Background())
		// Start the backend now: it removes the sandboxes that a process before
		// this one left running. Waiting for the first call would leave them
		// until somebody uses the add-on.
		go func() {
			sel.Warm()
			// After an upgrade, the images that changed are pulled before
			// anybody waits for them.
			deliv.Prepull(context.Background())
		}()
		return tools.New(m, settingsAdapter{Selector: sel, m: m, d: deliv}, hostAdmin{client}, opts...)
	}))
}
