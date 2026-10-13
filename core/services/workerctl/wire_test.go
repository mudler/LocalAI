package workerctl_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

// Workers and controllers of different versions talk to each other during a
// rolling update, so the JSON field names and omitempty choices are a wire
// contract. These specs pin the exact bytes rather than a round trip, because
// a round trip still passes after a tag is renamed on both sides at once.
var _ = Describe("Control payload wire format", func() {
	DescribeTable("marshals to the pinned bytes",
		func(v any, expected string) {
			raw, err := json.Marshal(v)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(raw)).To(Equal(expected))
		},
		Entry("BackendInstallRequest, every field set", workerctl.BackendInstallRequest{
			Backend: "b", ModelID: "m", BackendGalleries: "g", URI: "u", Name: "n", Alias: "a",
			ReplicaIndex: 2, Force: true, OpID: "op",
		}, `{"backend":"b","model_id":"m","backend_galleries":"g","uri":"u","name":"n","alias":"a","replica_index":2,"force":true,"op_id":"op"}`),
		Entry("BackendInstallRequest, zero value", workerctl.BackendInstallRequest{}, `{"backend":""}`),

		Entry("BackendInstallReply, every field set", workerctl.BackendInstallReply{
			Success: true, Address: "h:1", Error: "e",
		}, `{"success":true,"address":"h:1","error":"e"}`),
		Entry("BackendInstallReply, zero value", workerctl.BackendInstallReply{}, `{"success":false}`),

		Entry("BackendUpgradeRequest, every field set", workerctl.BackendUpgradeRequest{
			Backend: "b", BackendGalleries: "g", URI: "u", Name: "n", Alias: "a", ReplicaIndex: 2, OpID: "op",
		}, `{"backend":"b","backend_galleries":"g","uri":"u","name":"n","alias":"a","replica_index":2,"op_id":"op"}`),
		Entry("BackendUpgradeRequest, zero value", workerctl.BackendUpgradeRequest{}, `{"backend":""}`),

		Entry("BackendUpgradeReply, every field set", workerctl.BackendUpgradeReply{
			Success: true, Error: "e", StoppedProcessKeys: []string{"k#0"}, ReportsStoppedProcesses: true,
		}, `{"success":true,"error":"e","stopped_process_keys":["k#0"],"reports_stopped_processes":true}`),
		Entry("BackendUpgradeReply, zero value", workerctl.BackendUpgradeReply{}, `{"success":false}`),

		Entry("BackendListRequest", workerctl.BackendListRequest{}, `{}`),

		Entry("BackendListReply, every field set", workerctl.BackendListReply{
			Backends: []workerctl.NodeBackendInfo{{
				Name: "n", IsSystem: true, IsMeta: true, InstalledAt: "t", GalleryURL: "gu",
				Version: "v", URI: "u", Digest: "d",
			}},
			Error: "e",
		}, `{"backends":[{"name":"n","is_system":true,"is_meta":true,"installed_at":"t","gallery_url":"gu","version":"v","uri":"u","digest":"d"}],"error":"e"}`),
		Entry("BackendListReply, zero value", workerctl.BackendListReply{}, `{"backends":null}`),

		Entry("NodeBackendInfo, every field set", workerctl.NodeBackendInfo{
			Name: "n", IsSystem: true, IsMeta: true, InstalledAt: "t", GalleryURL: "gu",
			Version: "v", URI: "u", Digest: "d",
		}, `{"name":"n","is_system":true,"is_meta":true,"installed_at":"t","gallery_url":"gu","version":"v","uri":"u","digest":"d"}`),
		Entry("NodeBackendInfo, zero value", workerctl.NodeBackendInfo{}, `{"name":"","is_system":false,"is_meta":false}`),

		Entry("BackendStopRequest, every field set", workerctl.BackendStopRequest{Backend: "b", Force: true},
			`{"backend":"b","force":true}`),
		Entry("BackendStopRequest, zero value", workerctl.BackendStopRequest{}, `{"backend":""}`),

		Entry("BackendStopReply, every field set", workerctl.BackendStopReply{
			Success: true, Error: "e", StoppedProcessKeys: []string{"k#0"}, ReportsStoppedProcesses: true,
		}, `{"success":true,"error":"e","stopped_process_keys":["k#0"],"reports_stopped_processes":true}`),
		Entry("BackendStopReply, zero value", workerctl.BackendStopReply{}, `{"success":false}`),

		Entry("ModelStopRequest, every field set", workerctl.ModelStopRequest{
			ModelName: "m", ProcessKey: "k", ExpectedAddress: "a", Force: true, ConfigRevision: "r",
		}, `{"model_name":"m","process_key":"k","expected_address":"a","force":true,"config_revision":"r"}`),
		Entry("ModelStopRequest, zero value", workerctl.ModelStopRequest{},
			`{"model_name":"","process_key":"","expected_address":""}`),

		Entry("ModelStopReply, every field set", workerctl.ModelStopReply{
			Matched: true, Freed: true, Terminated: true, ProcessKey: "k", Address: "a", Error: "e",
		}, `{"matched":true,"freed":true,"terminated":true,"process_key":"k","address":"a","error":"e"}`),
		Entry("ModelStopReply, zero value", workerctl.ModelStopReply{},
			`{"matched":false,"freed":false,"terminated":false,"process_key":""}`),

		Entry("BackendDeleteRequest", workerctl.BackendDeleteRequest{Backend: "b"}, `{"backend":"b"}`),

		Entry("BackendDeleteReply, every field set", workerctl.BackendDeleteReply{
			Success: true, Error: "e", StoppedProcessKeys: []string{"k#0"}, ReportsStoppedProcesses: true,
		}, `{"success":true,"error":"e","stopped_process_keys":["k#0"],"reports_stopped_processes":true}`),
		Entry("BackendDeleteReply, zero value", workerctl.BackendDeleteReply{}, `{"success":false}`),

		Entry("ModelUnloadRequest, every field set", workerctl.ModelUnloadRequest{ModelName: "m", Address: "a"},
			`{"model_name":"m","address":"a"}`),
		Entry("ModelUnloadRequest, zero value", workerctl.ModelUnloadRequest{}, `{"model_name":""}`),

		Entry("ModelUnloadReply, every field set", workerctl.ModelUnloadReply{Success: true, Error: "e"},
			`{"success":true,"error":"e"}`),
		Entry("ModelUnloadReply, zero value", workerctl.ModelUnloadReply{}, `{"success":false}`),

		Entry("ModelDeleteRequest", workerctl.ModelDeleteRequest{ModelName: "m"}, `{"model_name":"m"}`),

		Entry("ModelDeleteReply, every field set", workerctl.ModelDeleteReply{Success: true, Error: "e"},
			`{"success":true,"error":"e"}`),
		Entry("ModelDeleteReply, zero value", workerctl.ModelDeleteReply{}, `{"success":false}`),

		Entry("ModelsRunningRequest", workerctl.ModelsRunningRequest{}, `{}`),

		Entry("ModelsRunningReply, every field set", workerctl.ModelsRunningReply{
			Models: []workerctl.RunningModelInfo{{ModelID: "m", ReplicaIndex: 1, Address: "a"}},
			Error:  "e",
		}, `{"models":[{"model_id":"m","replica_index":1,"address":"a"}],"error":"e"}`),
		Entry("ModelsRunningReply, zero value", workerctl.ModelsRunningReply{}, `{"models":null}`),

		Entry("RunningModelInfo, every field set", workerctl.RunningModelInfo{ModelID: "m", ReplicaIndex: 1, Address: "a"},
			`{"model_id":"m","replica_index":1,"address":"a"}`),
		Entry("RunningModelInfo, zero value", workerctl.RunningModelInfo{}, `{"model_id":"","replica_index":0}`),

		Entry("BackendInstallProgressEvent, every field set", workerctl.BackendInstallProgressEvent{
			OpID: "op", NodeID: "n", Backend: "b", FileName: "f", Current: "1 MB", Total: "2 MB",
			Percentage: 19.6, Phase: workerctl.PhaseDownloading,
		}, `{"op_id":"op","node_id":"n","backend":"b","file_name":"f","current":"1 MB","total":"2 MB","percentage":19.6,"phase":"downloading"}`),
		Entry("BackendInstallProgressEvent, zero value", workerctl.BackendInstallProgressEvent{},
			`{"op_id":"","node_id":"","backend":"","percentage":0}`),
	)

	// The file staging replies below must equal the single-key maps that the
	// worker's file staging handlers marshal today, so a worker that switches to
	// these structs sends the same bytes.
	DescribeTable("file staging payloads marshal to the pinned bytes",
		func(v any, expected string) {
			raw, err := json.Marshal(v)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(raw)).To(Equal(expected))
		},
		Entry("FileEnsureRequest", workerctl.FileEnsureRequest{Key: "k"}, `{"key":"k"}`),
		Entry("FileEnsureReply, success", workerctl.FileEnsureReply{LocalPath: "x"}, `{"local_path":"x"}`),
		Entry("FileEnsureReply, error", workerctl.FileEnsureReply{Error: "e"}, `{"error":"e"}`),

		Entry("FileStageRequest", workerctl.FileStageRequest{LocalPath: "p", Key: "k"}, `{"local_path":"p","key":"k"}`),
		Entry("FileStageReply, success", workerctl.FileStageReply{Key: "x"}, `{"key":"x"}`),
		Entry("FileStageReply, error", workerctl.FileStageReply{Error: "e"}, `{"error":"e"}`),

		Entry("FileTempRequest", workerctl.FileTempRequest{}, `{}`),
		Entry("FileTempReply, success", workerctl.FileTempReply{LocalPath: "x"}, `{"local_path":"x"}`),
		Entry("FileTempReply, error", workerctl.FileTempReply{Error: "e"}, `{"error":"e"}`),

		Entry("FileListDirRequest", workerctl.FileListDirRequest{KeyPrefix: "p/"}, `{"key_prefix":"p/"}`),
		Entry("FileListDirReply, success", workerctl.FileListDirReply{Files: []string{"a"}}, `{"files":["a"]}`),
		Entry("FileListDirReply, error", workerctl.FileListDirReply{Error: "e"}, `{"error":"e"}`),

		Entry("FileReleaseRequest, exact key", workerctl.FileReleaseRequest{Key: "k"}, `{"key":"k"}`),
		Entry("FileReleaseRequest, request id", workerctl.FileReleaseRequest{RequestID: "r"}, `{"request_id":"r"}`),
		Entry("FileReleaseReply, success", workerctl.FileReleaseReply{}, `{}`),
		Entry("FileReleaseReply, error", workerctl.FileReleaseReply{Error: "e"}, `{"error":"e"}`),
	)
})

var _ = Describe("Agent worker verbs", func() {
	It("names four verbs that the backend worker does not serve", func() {
		Expect(workerctl.AgentVerbs()).To(ConsistOf(
			workerctl.VerbMCPToolExecute, workerctl.VerbMCPDiscovery,
			workerctl.VerbAgentExecute, workerctl.VerbMCPCIRun,
		))
		for _, v := range workerctl.AgentVerbs() {
			Expect(workerctl.AllVerbs()).ToNot(ContainElement(v), v)
		}
	})

	It("maps each verb onto its own path below the prefix", func() {
		Expect(workerctl.PathOf(workerctl.VerbMCPToolExecute)).To(Equal("/v1/control/mcp/tools/execute"))
		Expect(workerctl.PathOf(workerctl.VerbMCPDiscovery)).To(Equal("/v1/control/mcp/discovery"))
		Expect(workerctl.PathOf(workerctl.VerbAgentExecute)).To(Equal("/v1/control/agent/execute"))
		Expect(workerctl.PathOf(workerctl.VerbMCPCIRun)).To(Equal("/v1/control/mcp-ci/run"))
	})

	It("carries the subject of a progress line only when there is one", func() {
		raw, err := json.Marshal(workerctl.Envelope{Subject: "jobs.j1.progress", Progress: json.RawMessage(`{"a":1}`)})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).To(Equal(`{"subject":"jobs.j1.progress","progress":{"a":1}}`))

		raw, err = json.Marshal(workerctl.Envelope{Progress: json.RawMessage(`{"a":1}`)})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).To(Equal(`{"progress":{"a":1}}`))
	})

	It("pins the bytes of the terminal reply of a run", func() {
		raw, err := json.Marshal(workerctl.RunReply{JobID: "j", Status: "completed", Result: "r", Error: "e"})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).To(Equal(`{"job_id":"j","status":"completed","result":"r","error":"e"}`))
		raw, err = json.Marshal(workerctl.RunReply{})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).To(Equal(`{}`))
	})
})
