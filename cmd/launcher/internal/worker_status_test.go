// SPDX-License-Identifier: MIT
package launcher

import (
	"context"
	"github.com/alecthomas/kong"
	"github.com/mudler/LocalAI/pkg/workerstatus"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
	"time"
)

var _ = Describe("Worker status", func() {
	It("checks exact bounded CLI capability output, not a version threshold", func() {
		dir := GinkgoT().TempDir()
		binary := filepath.Join(dir, "runtime")
		for _, body := range []string{"echo old-version", "exit 1", "printf '%02000d' 1", "echo localai-worker-status-v2"} {
			Expect(os.WriteFile(binary, []byte("#!/bin/sh\n"+body+"\n"), 0700)).To(Succeed())
			Expect(checkWorkerRuntime(context.Background(), binary)).To(MatchError(errWorkerUpgrade))
		}
		Expect(os.WriteFile(binary, []byte("#!/bin/sh\n[ \"$1\" = worker-status-capability ] || exit 2\n[ -z \"$LOCALAI_REGISTRATION_TOKEN\" ] || exit 3\necho localai-worker-status-v1\n"), 0700)).To(Succeed())
		Expect(checkWorkerRuntime(context.Background(), binary)).To(Succeed())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(checkWorkerRuntime(ctx, binary)).NotTo(Succeed())
	})
	It("parses the actual capability command without worker connection flags", func() {
		var cli struct {
			Capability workerstatus.CapabilityCommand `cmd:"" name:"worker-status-capability"`
		}
		parser, err := kong.New(&cli)
		Expect(err).NotTo(HaveOccurred())
		ctx, err := parser.Parse([]string{"worker-status-capability"})
		Expect(err).NotTo(HaveOccurred())
		Expect(ctx.Run()).To(Succeed())
	})
	It("reads only current private evidence and never treats liveness as ready", func() {
		now := time.Now()
		path, generation, err := workerstatus.NewSession(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		reader := workerStatusReader{path: path, tracker: workerstatus.Tracker{Generation: generation, Started: now}}
		Expect(reader.Read(now)).To(Equal("status_unavailable"))
		s := workerstatus.Snapshot{Schema: 1, Generation: generation, Sequence: 1, Time: now, Registration: "starting", HeartbeatInterval: 10 * time.Second}
		Expect(workerstatus.Write(path, s)).To(Succeed())
		Expect(reader.Read(now)).To(Equal("starting"))
		Expect(reader.Read(now.Add(10 * time.Second))).To(Equal("status_unavailable"))
		s.Sequence++
		s.Generation = "previous"
		Expect(workerstatus.Write(path, s)).To(Succeed())
		Expect(reader.Read(now)).To(Equal("status_unavailable"))
	})
})
