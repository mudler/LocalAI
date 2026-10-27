// SPDX-License-Identifier: MIT
package shipped_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/tests/addons/shipped/lib"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/phayes/freeport"
)

// The suite runs the real local-ai binary with the real packages that
// scripts/addons/package.sh makes, against a local library. Only the language
// model and the training engines are replaced (the mock backend and fakejobs).
//
// Environment:
//
//	LOCALAI_E2E_BINARY      a built local-ai (go build -tags auth ./cmd/local-ai); built when unset
//	LOCALAI_E2E_PORT        the port of the server; a free one when unset
//	LOCALAI_E2E_WORK        scratch directory; a temporary one when unset
//	LOCALAI_E2E_RUNSC_DIR   a directory made by addons/sandbox/package/fetch-runtime.sh; the Sandbox
//	                        package is skipped when it is unset and the download fails
//	LOCALAI_E2E_KNOWN_GAPS  set to "run" to run the specs that are skipped for a known gap

const (
	adminPassword = "correct-horse-battery"
	packageVer    = "0.0.1"
	upgradeVer    = "0.0.2"
)

var allAddons = []string{"face", "fine-tuning", "office", "quantization", "sandbox", "studio", "tables", "talk", "voice"}

type world struct {
	root, work, bin string
	port            int
	base            string
	libraryURL      string
	stopLibrary     func()
	cmd             *exec.Cmd
	dataDir         string
	skipSandbox     bool
}

var w world

// knownGap skips a spec that documents a defect, unless the run asks for it.
func knownGap(id, what string) {
	GinkgoHelper()
	if os.Getenv("LOCALAI_E2E_KNOWN_GAPS") != "run" {
		Skip("known gap: " + id + ": " + what)
	}
}

func repoRoot() string {
	dir, err := os.Getwd()
	Expect(err).NotTo(HaveOccurred())
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		Expect(parent).NotTo(Equal(dir), "go.mod not found")
		dir = parent
	}
}

func run(dir string, env []string, name string, args ...string) string {
	GinkgoHelper()
	cmd := exec.Command(name, args...) // #nosec G204 -- the suite starts the tools it builds
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "%s %s\n%s", name, strings.Join(args, " "), out)
	return string(out)
}

// setupWorld builds the binaries and the packages and starts the library.
func setupWorld() {
	w.root = repoRoot()
	w.work = os.Getenv("LOCALAI_E2E_WORK")
	if w.work == "" {
		w.work = GinkgoT().TempDir()
	}
	Expect(os.MkdirAll(w.work, 0o755)).To(Succeed())
	w.bin = os.Getenv("LOCALAI_E2E_BINARY")
	if w.bin == "" {
		w.bin = filepath.Join(w.work, "local-ai")
		run(w.root, nil, "go", "build", "-tags", "auth", "-o", w.bin, "./cmd/local-ai")
	}
	run(w.root, nil, "go", "build", "-o", filepath.Join(w.work, "mock-backend"), "./tests/e2e/mock-backend")
	run(w.root, nil, "go", "build", "-o", filepath.Join(w.work, "fakejobs"), "./tests/addons/shipped/cmd/fakejobs")

	runsc := os.Getenv("LOCALAI_E2E_RUNSC_DIR")
	if runsc == "" {
		dir := filepath.Join(w.work, "runsc")
		cmd := exec.Command("sh", "addons/sandbox/package/fetch-runtime.sh", runtime.GOOS+"/"+runtime.GOARCH, dir) // #nosec G204 -- fixed script of the repository
		cmd.Dir = w.root
		if out, err := cmd.CombinedOutput(); err == nil {
			runsc = dir
		} else {
			GinkgoWriter.Printf("the runsc download failed, the Sandbox package is skipped: %v\n%s", err, out)
		}
	}
	w.skipSandbox = runsc == ""

	platform := runtime.GOOS + "/" + runtime.GOARCH
	var specs []string
	pack := func(addon, version string) {
		layout := filepath.Join(w.work, "pkg", addon+"-"+version)
		Expect(os.RemoveAll(layout)).To(Succeed())
		args := []string{"scripts/addons/package.sh", "--layout", layout, "--version", version, "--platform", platform,
			"--config", "addons/" + addon + "/package/platforms.yaml", "--local-check"}
		if addon == "sandbox" {
			args = append(args, "--runtime-dir", runsc)
		}
		run(w.root, []string{"TMPDIR=" + w.work}, "sh", args...)
		specs = append(specs, addon+"@"+version+"="+layout)
	}
	for _, a := range allAddons {
		if a == "sandbox" && w.skipSandbox {
			continue
		}
		pack(a, packageVer)
	}
	pack("tables", upgradeVer)
	pack("face", upgradeVer)
	var err error
	w.libraryURL, w.stopLibrary, err = lib.Serve("127.0.0.1:0", specs)
	Expect(err).NotTo(HaveOccurred())
}

func cleanEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		// A static API key in the environment would put the whole server behind it.
		if strings.HasPrefix(kv, "API_KEY=") || strings.HasPrefix(kv, "OPENAI_API_KEY=") || strings.HasPrefix(kv, "LOCALAI_API_KEY=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

// startServer starts local-ai on a fresh data directory. The library source is
// written before the first start with the loopback opt-in, which only the
// owner's own configuration can set.
func startServer(name string, auth bool, extraEnv []string, extraArgs ...string) {
	GinkgoHelper()
	dir := filepath.Join(w.work, name)
	forceRemove(dir)
	w.dataDir = filepath.Join(dir, "data")
	models := filepath.Join(dir, "models")
	// The owner refuses a configuration directory that others can read.
	Expect(os.MkdirAll(filepath.Join(w.dataDir, "addons"), 0o700)).To(Succeed())
	for _, d := range []string{models, filepath.Join(dir, "backends")} {
		Expect(os.MkdirAll(d, 0o755)).To(Succeed())
	}
	Expect(os.WriteFile(filepath.Join(models, "mock-model.yaml"), []byte("name: mock-model\nbackend: mock-backend\nparameters:\n  model: mock-model.bin\n"), 0o600)).To(Succeed())
	sources := map[string]any{"local": map[string]any{"id": "local", "name": "Local", "url": w.libraryURL, "enabled": true, "kind": "addon",
		"policy": map[string]any{"requireSignature": false}, "allowLoopbackHTTP": true}}
	raw, _ := json.Marshal(sources)
	Expect(os.WriteFile(filepath.Join(w.dataDir, "addons", "sources.json"), raw, 0o600)).To(Succeed())
	for _, b := range []string{"trl", "llama-cpp-quantization"} {
		Expect(os.MkdirAll(filepath.Join(dir, "backends", b), 0o755)).To(Succeed())
		script := fmt.Sprintf("#!/bin/sh\nexec %s \"$@\"\n", filepath.Join(w.work, "fakejobs"))
		Expect(os.WriteFile(filepath.Join(dir, "backends", b, "run.sh"), []byte(script), 0o755)).To(Succeed()) // #nosec G306 -- a launcher the server runs
	}
	if w.port == 0 {
		if p := os.Getenv("LOCALAI_E2E_PORT"); p != "" {
			_, err := fmt.Sscanf(p, "%d", &w.port)
			Expect(err).NotTo(HaveOccurred())
		} else {
			p, err := freeport.GetFreePort()
			Expect(err).NotTo(HaveOccurred())
			w.port = p
		}
	}
	w.base = fmt.Sprintf("http://127.0.0.1:%d", w.port)
	env := cleanEnv(append([]string{"FAKEJOBS_DIR=" + filepath.Join(dir, "jobs")}, extraEnv...)...)
	Expect(os.MkdirAll(filepath.Join(dir, "jobs"), 0o755)).To(Succeed())
	if auth {
		env = append(env, "LOCALAI_AUTH=true", "LOCALAI_ADMIN_EMAIL=admin@e2e.test", "LOCALAI_AUTH_DATABASE_URL="+filepath.Join(dir, "auth.db"))
	}
	args := []string{"run", "--address", fmt.Sprintf("127.0.0.1:%d", w.port), "--models-path", models, "--data-path", w.dataDir,
		"--backends-path", filepath.Join(dir, "backends"), "--external-grpc-backends=mock-backend:" + filepath.Join(w.work, "mock-backend")}
	args = append(args, extraArgs...)
	w.cmd = exec.Command(w.bin, args...) // #nosec G204 -- the binary under test
	w.cmd.Env = env
	logFile, err := os.Create(filepath.Join(dir, "server.log")) // #nosec G304 -- the suite's own scratch directory
	Expect(err).NotTo(HaveOccurred())
	w.cmd.Stdout, w.cmd.Stderr = logFile, logFile
	Expect(w.cmd.Start()).To(Succeed())
	Eventually(func() error {
		resp, err := http.Get(w.base + "/api/features") // #nosec G107 -- loopback test server
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		return nil
	}, 180*time.Second, 500*time.Millisecond).Should(Succeed(), "the server did not start; see %s", filepath.Join(dir, "server.log"))
}

func stopServer() {
	if w.cmd == nil || w.cmd.Process == nil {
		return
	}
	_ = w.cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _ = w.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		_ = w.cmd.Process.Kill()
		<-done
	}
	w.cmd = nil
}

// client is one signed-in browser session or API key.
type client struct {
	http  *http.Client
	token string
	name  string
	id    string
}

type reply struct {
	Status int
	Body   []byte
}

func (r reply) JSON() map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal(r.Body, &out)
	return out
}

func (r reply) String() string { return fmt.Sprintf("[%d] %s", r.Status, r.Body) }

func (r reply) ErrType() string {
	e, _ := r.JSON()["error"].(map[string]any)
	s, _ := e["type"].(string)
	return s
}

var ids atomic.Int64

func opID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano()%1_000_000, ids.Add(1))
}

// do sends a request as a same-origin browser fetch (or with the API key).
func (c *client) do(method, path string, body any, headers map[string]string) reply {
	GinkgoHelper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, w.base+path, rd)
	Expect(err).NotTo(HaveOccurred())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	} else {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Sec-Fetch-Dest", "empty")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("Origin", w.base)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return reply{Status: resp.StatusCode, Body: raw}
}

func (c *client) get(path string) reply { return c.do("GET", path, nil, nil) }

// human is a request that needs the person's own browser session: the confirm
// header equals the identifier the route binds.
func (c *client) human(method, path string, body any, confirm string) reply {
	GinkgoHelper()
	return c.do(method, path, body, map[string]string{"X-LocalAI-Confirm": confirm})
}

func newSession(user string) *client {
	GinkgoHelper()
	jar, _ := cookiejar.New(nil)
	c := &client{http: &http.Client{Jar: jar, Timeout: 120 * time.Second}, name: user}
	creds := map[string]string{"email": user + "@e2e.test", "password": adminPassword, "name": user}
	r := c.do("POST", "/api/auth/register", creds, nil)
	if r.Status != 201 {
		r = c.do("POST", "/api/auth/login", creds, nil)
	}
	Expect(r.Status).To(BeNumerically("<", 300), r.String())
	if u, ok := r.JSON()["user"].(map[string]any); ok {
		c.id, _ = u["id"].(string)
	}
	return c
}

// keyOf makes an API key for the signed-in user.
func (c *client) key() *client {
	GinkgoHelper()
	r := c.do("POST", "/api/auth/api-keys", map[string]string{"name": "e2e"}, nil)
	Expect(r.Status).To(BeNumerically("<", 300), r.String())
	tok, _ := r.JSON()["key"].(string)
	Expect(tok).NotTo(BeEmpty(), r.String())
	return &client{http: &http.Client{Timeout: 120 * time.Second}, token: tok, name: c.name + "-key", id: c.id}
}

// dispatch sends one typed operation.
func (c *client) dispatch(addon string, generation uint64, action string, args map[string]any, resource string) reply {
	GinkgoHelper()
	if args == nil {
		args = map[string]any{}
	}
	req := map[string]any{"operationId": opID("op"), "addonId": addon, "generation": generation, "action": action, "arguments": args}
	if resource != "" {
		req["resourceId"] = resource
	}
	return c.do("POST", "/api/addons/dispatch", req, nil)
}

func payload(r reply) map[string]any {
	GinkgoHelper()
	p, _ := r.JSON()["payload"].(map[string]any)
	return p
}

// installed returns the installed view of an add-on, or nil.
func (c *client) installed(addon string) map[string]any {
	GinkgoHelper()
	r := c.dispatch(addon, 0, "addons-installed", nil, "")
	Expect(r.Status).To(Equal(200), r.String())
	list, _ := payload(r)["addons"].([]any)
	for _, a := range list {
		m := a.(map[string]any)
		if m["id"] == addon {
			return m
		}
	}
	return nil
}

func (c *client) state(addon string) string {
	if a := c.installed(addon); a != nil {
		s, _ := a["state"].(string)
		return s
	}
	return "absent"
}

func generation(a map[string]any) uint64 {
	g, _ := a["generation"].(float64)
	return uint64(g)
}

// install installs and enables an add-on of the local library and waits until it runs.
func (c *client) install(addon, version string) uint64 {
	GinkgoHelper()
	r := c.dispatch(addon, 1, "install", map[string]any{"sourceId": "local", "version": version}, "")
	Expect(r.Status).To(Equal(200), "install "+addon+": "+r.String())
	a := c.installed(addon)
	Expect(a).NotTo(BeNil())
	gen := generation(a)
	r = c.dispatch(addon, gen, "enable", nil, "")
	Expect(r.Status).To(Equal(200), "enable "+addon+": "+r.String())
	Eventually(func() string { return c.state(addon) }, 90*time.Second, 300*time.Millisecond).Should(Equal("running"), addon)
	return gen
}

// chat is a server-side chat of the user on the mock model.
func (c *client) chat() string {
	GinkgoHelper()
	r := c.do("POST", "/api/chats", map[string]string{"model": "mock-model"}, nil)
	Expect(r.Status).To(Equal(201), r.String())
	id, _ := r.JSON()["id"].(string)
	return id
}

// toolCall scripts one add-on tool call in a fresh chat of the user and returns
// the text the host gave the model back (the tool message of the transcript).
// The scripted model answers its first turn with the call.
func (c *client) toolCall(tool string, args map[string]any) string {
	GinkgoHelper()
	return c.toolCallIn(c.chat(), tool, args)
}

func (c *client) toolCallIn(chat, tool string, args map[string]any) string {
	GinkgoHelper()
	raw, err := json.Marshal(args)
	Expect(err).NotTo(HaveOccurred())
	text := "Please run ADDON_TOOL_CALL:addon__" + tool + ":" + base64.RawURLEncoding.EncodeToString(raw)
	r := c.do("POST", "/api/chats/"+chat+"/messages", map[string]string{"text": text}, nil)
	Expect(r.Status).To(Equal(200), r.String())
	var tool0 string
	Eventually(func() bool {
		info := c.get("/api/chats/" + chat).JSON()
		running, _ := info["running"].(bool)
		if running {
			return false
		}
		msgs, _ := c.get("/api/chats/" + chat + "/messages?after=0").JSON()["messages"].([]any)
		for _, m := range msgs {
			mm := m.(map[string]any)
			if mm["role"] == "tool" {
				tool0, _ = mm["text"].(string)
				return true
			}
		}
		return false
	}, 120*time.Second, 300*time.Millisecond).Should(BeTrue(), "no tool result in chat "+chat)
	return tool0
}

// toolResult decodes the JSON document of a tool message ("name: label\n{...}").
func toolResult(msg string) map[string]any {
	GinkgoHelper()
	i := strings.Index(msg, "{")
	out := map[string]any{}
	if i >= 0 {
		_ = json.Unmarshal([]byte(msg[i:]), &out)
	}
	return out
}

func jsonList(raw []byte, out *[]any) error { return json.Unmarshal(raw, out) }

// jobStatus is the status of the newest job of a list.
func jobStatus(list []any) string {
	if len(list) == 0 {
		return ""
	}
	m, _ := list[len(list)-1].(map[string]any)
	s, _ := m["status"].(string)
	return s
}

// newChatWhere makes chats until the id satisfies the test.
func newChatWhere(c *client, ok func(id string) bool) string {
	GinkgoHelper()
	for i := 0; i < 80; i++ {
		if id := c.chat(); ok(id) {
			return id
		}
	}
	Fail("no chat id of the wanted shape")
	return ""
}

// newLetterChat is a chat whose id starts with a letter. See gap G1.
func newLetterChat(c *client) string {
	return newChatWhere(c, func(id string) bool { return id[0] >= 'a' && id[0] <= 'f' })
}

// newDigitChat is a chat whose id starts with a digit.
func newDigitChat(c *client) string {
	return newChatWhere(c, func(id string) bool { return id[0] >= '0' && id[0] <= '9' })
}

// minimalPDF is a PDF with one line of text on each page.
func minimalPDF(pages ...string) []byte {
	var objs []string
	kids := ""
	for i := range pages {
		kids += fmt.Sprintf("%d 0 R ", 3+i*2)
	}
	objs = append(objs, "<< /Type /Catalog /Pages 2 0 R >>", fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids, len(pages)))
	font := 3 + len(pages)*2
	for i, text := range pages {
		stream := fmt.Sprintf("BT /F1 18 Tf 20 100 Td (%s) Tj ET", text)
		objs = append(objs,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Contents %d 0 R /Resources << /Font << /F1 %d 0 R >> >> >>", 4+i*2, font),
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}
	objs = append(objs, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	out := "%PDF-1.4\n"
	var offsets []int
	for i, o := range objs {
		offsets = append(offsets, len(out))
		out += fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := len(out)
	out += fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		out += fmt.Sprintf("%010d 00000 n \n", off)
	}
	out += fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return []byte(out)
}

// forceRemove deletes a directory that a previous run left, including the
// read-only trees of an unpacked add-on package.
func forceRemove(dir string) {
	GinkgoHelper()
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	Expect(os.RemoveAll(dir)).To(Succeed())
}
