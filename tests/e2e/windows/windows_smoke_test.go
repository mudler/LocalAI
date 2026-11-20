// Windows host smoke test for the native-windows backend path.
//
// Unlike the in-process e2e suite (tests/e2e/e2e_suite_test.go, which drives
// LocalAI through httpapi.API inside the test binary), this suite boots the
// real local-ai.exe with a --backends-path backend laid out the way the
// gallery contract ships it (run.sh stub for discovery + run.ps1 to launch),
// then exercises the HTTP API and the job-object process reaper from
// pkg/model. It is the regression guard for the Windows-native backend
// support: run.ps1 execution, backend discovery, and kill-on-close tree
// cleanup only ever happen on a live Windows host.
package windows_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/phayes/freeport"
)

const buildTimeout = 15 * time.Minute

var (
	serverProcess *os.Process
	serverStdout  io.WriteCloser
	serverURL     string
	serverLogPath string
	httpClient    *http.Client

	modelsPath   string
	backendsPath string
	tempDir      string

	// Binaries under test. Names carry smoke- to avoid colliding with any
	// in-tree artifact; both are built into tempDir.
	localAIExe     string
	mockBackendExe string

	serverKilled bool
)

// procRow is one row of the live Win32_Process snapshot.
type procRow struct {
	PID  int    `json:"ProcessId"`
	PPID int    `json:"ParentProcessId"`
	Name string `json:"Name"`
	Cmd  string `json:"CommandLine"`
}

// repoRoot walks up from the package directory to the module root (the first
// ancestor containing go.mod). The test run's working directory is a package
// directory of the LocalAI module, so this terminates quickly.
func repoRoot(start string) string {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			Fail("could not locate the LocalAI module root from " + start)
		}
		dir = parent
	}
}

// buildBinary builds the package into out, mirroring the Makefile's
// CGO_ENABLED=0 so the build is environment-independent.
func buildBinary(pkg, out string) {
	wd, err := os.Getwd()
	Expect(err).ToNot(HaveOccurred())
	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", out, pkg)
	cmd.Dir = repoRoot(wd)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		Fail(fmt.Sprintf("go build %s -> %s failed: %s\n%s", pkg, out, err, output))
	}
}

// resolveBinary returns an existing binary from the given env override, or
// builds the package into tempDir.
func resolveBinary(env, pkg, name string) string {
	if p := os.Getenv(env); p != "" {
		Expect(p).To(BeAnExistingFile(), env+" must point at an existing executable")
		return p
	}
	out := filepath.Join(tempDir, name)
	buildBinary(pkg, out)
	return out
}

// powershellInvoke runs a PowerShell snippet and returns its raw stdout.
// Probes legitimately return non-zero exit codes (e.g. Get-Process for missing
// ids), so the exit status is deliberately ignored and only stdout matters.
func powershellInvoke(script string) string {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "powershell probe (stdout-only) returned %v\n%s\n", err, out)
	}
	return string(out)
}

// processSnapshot returns one row per live process, keyed for parent-child
// walks. CIM yields ProcessId/ParentProcessId in one query, which is the
// reliable way to reconstruct backend trees on Windows (no job/toolhelp API
// dependency and no command-line substring heuristic).
func processSnapshot() []procRow {
	const query = "@(Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,Name,CommandLine) | ConvertTo-Json -Compress"
	var rows []procRow
	out := powershellInvoke(query)
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		if rows = rows[:0]; len(out) == 0 {
			// Empty snapshot (extremely unlikely) — the spec will fail with a
			// clear message rather than panic.
			_, _ = fmt.Fprintf(GinkgoWriter, "process query produced no output\n")
			return nil
		}
		_, _ = fmt.Fprintf(GinkgoWriter, "process query did not unmarshal: %v\n%s\n", err, out)
	}
	return rows
}

// treeForPID collects the process subtree rooted at root (processes whose
// ancestry chains back to it), excluding the root itself. Used to identify the
// wrapper + backend processes owned by the local-ai.exe under test without
// matching against command-line text (which can collide with unrelated
// processes on a developer machine).
func treeForPID(snapshot []procRow, root int) []procRow {
	children := make(map[int][]procRow)
	for _, r := range snapshot {
		children[r.PPID] = append(children[r.PPID], r)
	}
	var tree []procRow
	frontier := []int{root}
	seen := map[int]bool{root: true}
	for len(frontier) > 0 {
		next := []int{}
		for _, pid := range frontier {
			for _, child := range children[pid] {
				if seen[child.PID] {
					continue
				}
				seen[child.PID] = true
				tree = append(tree, child)
				next = append(next, child.PID)
			}
		}
		frontier = next
	}
	sort.Slice(tree, func(i, j int) bool { return tree[i].PID < tree[j].PID })
	return tree
}

// describeV returns a readable listing of a process subtree for failure
// messages.
func describeV(rows []procRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = fmt.Sprintf("pid=%d ppid=%d name=%s cmd=%q", r.PID, r.PPID, r.Name, r.Cmd)
	}
	return out
}

// survivingPIDs filters ids down to those the current snapshot still holds.
func survivingPIDs(snapshot []procRow, ids []int) []int {
	alive := make(map[int]bool)
	for _, r := range snapshot {
		alive[r.PID] = true
	}
	var out []int
	for _, id := range ids {
		if alive[id] {
			out = append(out, id)
		}
	}
	return out
}

var _ = BeforeSuite(func() {
	if runtime.GOOS != "windows" {
		Skip("Windows host smoke test only runs on Windows")
	}

	httpClient = &http.Client{Timeout: 30 * time.Second}

	var err error
	tempDir, err = os.MkdirTemp("", "localai-windows-smoke-*")
	Expect(err).ToNot(HaveOccurred())

	localAIExe = resolveBinary("LOCAL_AI_EXE", "github.com/mudler/LocalAI/cmd/local-ai", "local-ai.exe")
	mockBackendExe = resolveBinary("MOCK_BACKEND_EXE", "github.com/mudler/LocalAI/tests/e2e/mock-backend", "mock-backend.exe")

	modelsPath = filepath.Join(tempDir, "models")
	backendsPath = filepath.Join(tempDir, "backends")
	Expect(os.MkdirAll(modelsPath, 0755)).To(Succeed())

	// Backend layout mirroring the gallery contract: the binary lives in
	// <backends>/mock-backend/ with run.sh (kept for uniform discovery —
	// ListSystemBackends looks for run.sh) and run.ps1 (what startProcess
	// actually executes on a Windows host).
	backendDir := filepath.Join(backendsPath, "mock-backend")
	Expect(os.MkdirAll(backendDir, 0755)).To(Succeed())

	Expect(os.WriteFile(filepath.Join(backendDir, "run.sh"), []byte("#!/bin/sh\nexit 0\n"), 0644)).To(Succeed())
	runPS1 := "$ErrorActionPreference = 'Stop'\n" +
		"& \"$PSScriptRoot\\mock-backend.exe\" @args\n" +
		"exit $LASTEXITCODE\n"
	Expect(os.WriteFile(filepath.Join(backendDir, "run.ps1"), []byte(runPS1), 0644)).To(Succeed())

	backendBinary, err := os.ReadFile(mockBackendExe)
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(backendDir, "mock-backend.exe"), backendBinary, 0755)).To(Succeed())

	modelCfg := "name: mock-model\n" +
		"backend: mock-backend\n" +
		"parameters:\n" +
		"  model: mock-model.bin\n"
	Expect(os.WriteFile(filepath.Join(modelsPath, "mock-model.yaml"), []byte(modelCfg), 0644)).To(Succeed())

	port, err := freeport.GetFreePort()
	Expect(err).ToNot(HaveOccurred())
	serverURL = fmt.Sprintf("http://127.0.0.1:%d/v1", port)

	// Capture local-ai.exe diagnostics so CI failures are not blind.
	serverLogPath = filepath.Join(tempDir, "local-ai.log")
	serverStdout, err = os.Create(serverLogPath)
	Expect(err).ToNot(HaveOccurred())

	// Run local-ai.exe from the temp dir so its state (.local_user_id etc.)
	// never lands in the repository working copy.
	cmd := exec.Command(localAIExe,
		"--address", fmt.Sprintf("127.0.0.1:%d", port),
		"--models-path", modelsPath,
		"--backends-path", backendsPath,
		"--log-level", "debug",
	)
	cmd.Dir = tempDir
	cmd.Stdout = serverStdout
	cmd.Stderr = serverStdout
	Expect(cmd.Start()).To(Succeed())
	serverProcess = cmd.Process

	// Server must come up and register the discovered mock-backend.
	Eventually(func() error {
		resp, err := httpClient.Get(serverURL + "/models")
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GET /v1/models: HTTP %d", resp.StatusCode)
		}
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return err
		}
		for _, m := range out.Data {
			if m.ID == "mock-model" {
				return nil
			}
		}
		return fmt.Errorf("mock-model not listed yet (got %d models)", len(out.Data))
	}, "2m", "2s").Should(Succeed(), dumpServerLog)
})

// dumpServerLog returns the tail of the local-ai.exe log for CI debugging.
func dumpServerLog() string {
	if serverLogPath == "" {
		return ""
	}
	data, err := os.ReadFile(serverLogPath)
	if err != nil {
		return fmt.Sprintf("server log (%s) unreadable: %s", serverLogPath, err)
	}
	const max = 2000
	if len(data) > max {
		data = data[len(data)-max:]
	}
	return fmt.Sprintf("server log (%s):\n%s", serverLogPath, data)
}

var _ = Describe("LocalAI Windows smoke", func() {
	It("serves a chat completion through the mock backend via run.ps1", func() {
		body := strings.NewReader(`{"model":"mock-model","messages":[{"role":"user","content":"hello"}]}`)
		resp, err := httpClient.Post(serverURL+"/chat/completions", "application/json", body)
		Expect(err).ToNot(HaveOccurred(), dumpServerLog)
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusOK), dumpServerLog)

		var out struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		Expect(json.NewDecoder(resp.Body).Decode(&out)).To(Succeed(), dumpServerLog)
		Expect(out.Choices).ToNot(BeEmpty(), dumpServerLog)
		Expect(out.Choices[0].Message.Content).To(ContainSubstring("This is a mocked response."))
	})

	// Must run last: it hard-kills the server under test. ginkgo runs specs in
	// declaration order.
	It("reaps the backend tree when local-ai.exe is hard-killed (job object)", func() {
		// Identify the wrapper + backend as the process subtree of the server,
		// not by command-line text (which matches unrelated processes).
		before := processSnapshot()
		tree := treeForPID(before, serverProcess.Pid)
		Expect(tree).ToNot(BeEmpty(),
			"no child processes of local-ai.exe (pid %d) found — the chat spec must have loaded the backend first", serverProcess.Pid)

		var treePIDs []int
		for _, r := range tree {
			treePIDs = append(treePIDs, r.PID)
		}

		// Hard-kill: no graceful shutdown runs, so a surviving backend means
		// kill-on-close failed, not a stop path.
		Expect(serverProcess.Kill()).To(Succeed())
		_, _ = serverProcess.Wait()
		serverKilled = true

		Eventually(func() []int {
			return survivingPIDs(processSnapshot(), treePIDs)
		}, "30s", "1s").Should(BeEmpty(),
			"these processes survived local-ai.exe kill-on-close (job object not applied?):\n%s",
			strings.Join(describeV(tree), "\n"))
	})
})

var _ = AfterSuite(func() {
	if runtime.GOOS != "windows" {
		return
	}
	if serverProcess != nil && !serverKilled {
		_ = serverProcess.Kill()
		_, _ = serverProcess.Wait()
	}
	if serverStdout != nil {
		_ = serverStdout.Close()
	}
	if serverLogPath != "" {
		_, _ = fmt.Fprintln(GinkgoWriter, dumpServerLog())
	}
	if tempDir != "" {
		_ = os.RemoveAll(tempDir)
	}
})
