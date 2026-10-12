// SPDX-License-Identifier: MIT
package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Offline dispatch measurement", func() {
	It("validates a sanitized local timeline through the real closed-schema sink", func() {
		var buf bytes.Buffer
		installMeasurementSink(&buf)
		ctx := context.WithValue(context.Background(), measurementKey{}, "PRIVATE_PROMPT_TOKEN_AUTH_ERROR_SENTINEL")
		stats := syntheticDispatch(NewRecorder(nil), ctx)
		Expect(stats.Dropped).To(BeZero())
		Expect(stats.Emitted).To(Equal(uint64(11)))
		Expect(buf.String()).NotTo(ContainSubstring("PRIVATE_"))
		decoder := json.NewDecoder(&buf)
		var rows []map[string]any
		for {
			var row map[string]any
			err := decoder.Decode(&row)
			if err == io.EOF {
				break
			}
			Expect(err).NotTo(HaveOccurred())
			rows = append(rows, row)
		}
		Expect(rows).To(HaveLen(int(stats.Emitted) + 1))
		allowed := map[string]bool{}
		for _, key := range []string{"time", "level", "msg", "id", "kind", "phase", "state", "outcome", "elapsed", "count", "offset", "span", "parent", "attempt", "connection", "dropped", "emitted", "sink_elapsed"} {
			allowed[key] = true
		}
		var phases []string
		for _, row := range rows {
			phases = append(phases, row["phase"].(string))
		}
		Expect(phases).To(Equal([]string{"http_entry", "route", "metadata_rpc", "metadata_rpc", "route", "frontend_message", "frontend_callback", "sse_role", "sse_content", "sse_flush", "sse_flush", "dispatch_summary"}))
		id := rows[0]["id"]
		last := float64(0)
		for _, row := range rows {
			Expect(row).To(HaveLen(len(allowed)))
			for key := range row {
				Expect(allowed[key]).To(BeTrue(), key)
			}
			Expect(row["id"]).To(Equal(id))
			Expect(row["kind"]).To(Equal("request"))
			offset := row["offset"].(float64)
			Expect(offset).To(BeNumerically(">=", last))
			last = offset
		}
		Expect(rows[2]["parent"]).To(Equal(rows[1]["span"]))
		Expect(rows[len(rows)-1]["phase"]).To(Equal(string(PhaseDispatchSummary)))
		// Reject free-form schema values even when callers bypass the recorder.
		for _, bad := range []Event{
			{ID: id.(string), Kind: KindRequest, Phase: Phase("PRIVATE_PHASE"), State: StateMark, Outcome: OutcomeOK},
			{ID: "PRIVATE_ID", Kind: KindRequest, Phase: PhaseSSEContent, State: StateMark, Outcome: OutcomeOK},
			{ID: id.(string), Kind: Kind("PRIVATE_KIND"), Phase: PhaseSSEContent, State: StateMark, Outcome: OutcomeOK},
			{ID: id.(string), Kind: KindRequest, Phase: PhaseSSEContent, State: State("PRIVATE_STATE"), Outcome: OutcomeOK},
			{ID: id.(string), Kind: KindRequest, Phase: PhaseSSEContent, State: StateMark, Outcome: Outcome("PRIVATE_ERROR")},
		} {
			before := buf.Len()
			LogEvent(bad)
			Expect(buf.Len()).To(Equal(before))
		}
	})
	It("reports disabled, collection, and real JSON sink distributions without a timing threshold", func() {
		if os.Getenv("LOCALAI_DIAGNOSTIC_MEASURE") != "1" {
			Skip("set LOCALAI_DIAGNOSTIC_MEASURE=1 for bounded local measurement")
		}
		installMeasurementSink(io.Discard)
		for _, mode := range []string{"disabled", "collection", "json-discard"} {
			var recorder *Recorder
			if mode == "collection" {
				recorder = NewRecorder(func(Event) {})
			}
			if mode == "json-discard" {
				recorder = NewRecorder(nil)
			}
			samples := make([]int64, 1000)
			var sink time.Duration
			for i := 0; i < 100; i++ {
				syntheticDispatch(recorder, context.Background())
			}
			for i := range samples {
				start := time.Now()
				stats := syntheticDispatch(recorder, context.Background())
				samples[i] = time.Since(start).Nanoseconds()
				sink += stats.SinkElapsed
			}
			sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
			fmt.Fprintf(GinkgoWriter, "dispatch_measurement mode=%s samples=%d unit=ns min=%d p50=%d p95=%d p99=%d max=%d mean_event_sink=%d\n", mode, len(samples), samples[0], samples[499], samples[949], samples[989], samples[999], sink.Nanoseconds()/int64(len(samples)))
		}
	})
})

type measurementKey struct{}

func installMeasurementSink(w io.Writer) {
	xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}), xlog.LogLevel("info")))
	DeferCleanup(func() {
		xlog.SetLogger(xlog.NewLogger(xlog.LogLevel(os.Getenv(xlog.EnvLogLevel)), os.Getenv(xlog.EnvLogFormat)))
	})
}

// This is a fixed local collection workload, not a simulated inference engine.
// Include request creation, nested spans, duplicate milestones, and final drain.
func syntheticDispatch(recorder *Recorder, base context.Context) DispatchStats {
	ctx := recorder.Request(base)
	root := Dispatch(ctx)
	root.MarkOnce(PhaseHTTPEntry, OutcomeOK)
	route, endRoute := root.NewAttempt().Start(PhaseRoute)
	_, endMetadata := route.Start(PhaseMetadataRPC)
	endMetadata(OutcomeOK, 1)
	endRoute(OutcomeOK, 1)
	root.MarkOnce(PhaseFrontendMessage, OutcomeOK)
	root.MarkOnce(PhaseFrontendCallback, OutcomeOK)
	root.MarkOnce(PhaseSSERole, OutcomeOK)
	for i := 0; i < 8; i++ {
		root.MarkOnce(PhaseSSEContent, OutcomeOK)
	}
	_, endFlush := root.Start(PhaseSSEFlush)
	endFlush(OutcomeOK, 1)
	return FlushDispatch(ctx)
}
