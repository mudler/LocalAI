package e2econtainerusers_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
)

// warnLine is the start of the one warning LocalAI logs when it cannot write
// to some of its folders (core/application/startup.go).
const warnLine = "LocalAI cannot write to some of its folders"

// The add-on and the backend each case installs. talk is a signed add-on of
// the official library that asks for no host access, so a plain install runs
// the whole package check, the Sigstore signature check included (#13507).
// local-store is the smallest CPU backend of the backend library.
const (
	addonID       = "talk"
	addonSourceID = "official"
	backendID     = "local-store"
)

// Time limits. A download that is slower than these is a CI problem, not a
// product one, but a hang must not eat the job.
const (
	readyTimeout   = 5 * time.Minute
	installTimeout = 10 * time.Minute
	requestTimeout = 2 * time.Minute
)

// transientError matches failures of the network, not of the product. Only
// these are tried a second time; any other failure fails the case at once.
var transientError = regexp.MustCompile(`(?i)(i/o timeout|timeout awaiting|context deadline exceeded|connection reset|connection refused|TLS handshake timeout|no such host|temporary failure in name resolution|unexpected EOF|502 Bad Gateway|503 Service Unavailable|504 Gateway Timeout|429 Too Many Requests|toomanyrequests)`)

// outcome is what an install must do in a case.
type outcome struct {
	ok bool
	// failure, when ok is false, must match the error of the install or the
	// log of the container: the check that refused it, named.
	failure *regexp.Regexp
}

// userCase is one way of running the image.
type userCase struct {
	name string
	// uid is the user LocalAI must run as; 0 means the uid of this process
	// (the case of bind mounts owned by the runner).
	uid int
	// warn: whether the warning about unwritable folders must be logged, and
	// the folders it must name when it is.
	warn        bool
	warnFolders []string
	addon       outcome
	backend     outcome
	// setup changes the request; it may register cleanups.
	setup func(req *testcontainers.ContainerRequest)
}

func randomSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func cases() []userCase {
	return []userCase{
		{
			// docker run with the volumes of the image: the entrypoint runs
			// LocalAI as the image user, 10001.
			name: "default", uid: 10001,
			addon: outcome{ok: true}, backend: outcome{ok: true},
		},
		{
			// A fixed user. Before the fix of #13507 the home folder stayed
			// with uid 10001 and the signature check of every add-on failed.
			name: "localai-uid", uid: 1000,
			addon: outcome{ok: true}, backend: outcome{ok: true},
			setup: func(req *testcontainers.ContainerRequest) {
				req.Env["LOCALAI_UID"] = "1000"
				req.Env["LOCALAI_GID"] = "1000"
			},
		},
		{
			// docker run --user with fresh named volumes. The entrypoint does
			// nothing for a container that does not start as root, and the
			// volumes get the owner of the image folders (10001), so this
			// user cannot write them. This setup is not supported as it is
			// (see "Container user and permissions" in the container docs),
			// but it must fail in a way the user can act on: LocalAI starts,
			// names the folders it cannot write, turns add-ons off with a log
			// line that says why (the add-on API then answers that add-ons
			// are off), and a backend install fails on the backends folder.
			name: "docker-user-named-volumes", uid: 1234,
			warn:        true,
			warnFolders: []string{"/data", "/models", "/backends"},
			addon:       outcome{failure: regexp.MustCompile(`(?s)Add-ons are off: LocalAI cannot write the add-on folder.*add-ons are turned off|add-ons are turned off.*Add-ons are off: LocalAI cannot write the add-on folder`)},
			backend:     outcome{failure: regexp.MustCompile(`(?i)/backends/\S*: permission denied`)},
			setup: func(req *testcontainers.ContainerRequest) {
				suffix := randomSuffix()
				for _, target := range []string{"/models", "/backends", "/configuration", "/data"} {
					name := "localai-usermodes-" + strings.TrimPrefix(target, "/") + "-" + suffix
					req.Mounts = append(req.Mounts, testcontainers.VolumeMount(name, testcontainers.ContainerMountTarget(target)))
				}
				req.ConfigModifier = func(c *container.Config) { c.User = "1234:1234" }
			},
		},
		{
			// Bind mounts that the user made: /data and /models belong to the
			// uid of this process and LOCALAI_UID is not set, so the
			// entrypoint runs LocalAI as that uid and changes no owner.
			name: "bind-mounts-host-user", uid: 0,
			addon: outcome{ok: true}, backend: outcome{ok: true},
			setup: func(req *testcontainers.ContainerRequest) {
				root, err := os.MkdirTemp("", "localai-usermodes-")
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() { _ = os.RemoveAll(root) })
				var binds []string
				for _, target := range []string{"/models", "/backends", "/data"} {
					dir := filepath.Join(root, strings.TrimPrefix(target, "/"))
					Expect(os.Mkdir(dir, 0o755)).To(Succeed())
					binds = append(binds, dir+":"+target)
				}
				req.HostConfigModifier = func(h *container.HostConfig) { h.Binds = append(h.Binds, binds...) }
			},
		},
		{
			// Compose read_only: true with a tmpfs /tmp. Everything LocalAI
			// writes must be in the volumes or in /tmp.
			name: "read-only-root", uid: 10001,
			addon: outcome{ok: true}, backend: outcome{ok: true},
			setup: func(req *testcontainers.ContainerRequest) {
				req.HostConfigModifier = func(h *container.HostConfig) {
					h.ReadonlyRootfs = true
					h.Tmpfs = map[string]string{"/tmp": "rw,exec,mode=1777"}
				}
			},
		},
	}
}

var _ = Describe("The container image", func() {
	for _, tc := range cases() {
		It("works in the "+tc.name+" setup", Label(tc.name), func() {
			runCase(tc)
		})
	}
})

// box is one running container and its HTTP client.
type box struct {
	c    testcontainers.Container
	base string
	http *http.Client
}

func (b *box) logs() string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := b.c.Logs(ctx)
	if err != nil {
		return "(no logs: " + err.Error() + ")"
	}
	defer r.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

func runCase(tc userCase) {
	uid := tc.uid
	if uid == 0 {
		uid = os.Getuid()
		Expect(uid).NotTo(BeZero(), "the bind mount case needs a user that is not root, as on a CI runner")
	}

	req := testcontainers.ContainerRequest{
		Image:        image,
		ExposedPorts: []string{"8080/tcp"},
		Env:          map[string]string{"DEBUG": "true"},
	}
	if tc.setup != nil {
		tc.setup(&req)
	}

	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if c != nil {
		var volumes []string
		for _, m := range req.Mounts {
			if v, ok := m.Source.(testcontainers.DockerVolumeMountSource); ok {
				volumes = append(volumes, v.Name)
			}
		}
		DeferCleanup(func() {
			_ = testcontainers.TerminateContainer(c, testcontainers.RemoveVolumes(volumes...))
		})
	}
	Expect(err).NotTo(HaveOccurred())

	endpoint, err := c.PortEndpoint(ctx, "8080/tcp", "http")
	Expect(err).NotTo(HaveOccurred())
	b := &box{c: c, base: endpoint, http: &http.Client{Timeout: requestTimeout}}

	DeferCleanup(func() {
		logs := b.logs()
		if logDir != "" {
			_ = os.WriteFile(filepath.Join(logDir, tc.name+".log"), []byte(logs), 0o644)
		}
		if CurrentSpecReport().Failed() {
			GinkgoWriter.Printf("=== container log of %s ===\n%s\n=== end of log ===\n", tc.name, logs)
		}
	})

	By("waiting for /readyz")
	waitReady(b)

	By(fmt.Sprintf("checking that LocalAI runs as uid %d", uid))
	Expect(processUID(b)).To(Equal(uid))

	By("checking the warning about folders it cannot write")
	logs := b.logs()
	warning := lineWith(logs, warnLine)
	if tc.warn {
		Expect(warning).NotTo(BeEmpty(), "the start must name the folders LocalAI cannot write")
		for _, f := range tc.warnFolders {
			Expect(warning).To(ContainSubstring(f))
		}
	} else {
		Expect(warning).To(BeEmpty(), "LocalAI must be able to write all its folders here")
	}

	By("installing the signed add-on " + addonID)
	addonErr := withRetry("add-on install", func() error { return installAddon(b) })
	expectOutcome(b, "add-on "+addonID, tc.addon, addonErr)

	By("installing the backend " + backendID)
	backendErr := withRetry("backend install", func() error { return installBackend(b) })
	expectOutcome(b, "backend "+backendID, tc.backend, backendErr)
}

func expectOutcome(b *box, what string, want outcome, err error) {
	if want.ok {
		Expect(err).NotTo(HaveOccurred(), "the install of the %s must succeed", what)
		return
	}
	Expect(err).To(HaveOccurred(), "the install of the %s must fail in this setup", what)
	// The reason is in the error or, for an add-on, in the log line of the
	// failed operation, which names the check.
	text := err.Error() + "\n" + b.logs()
	Expect(want.failure.MatchString(text)).To(BeTrue(), "the failed install of the %s must name the reason (%s); error: %v", what, want.failure, err)
}

// withRetry runs fn, and once more only when the first failure is a network
// error.
func withRetry(what string, fn func() error) error {
	err := fn()
	if err != nil && transientError.MatchString(err.Error()) {
		GinkgoWriter.Printf("%s failed with a network error, trying once more: %v\n", what, err)
		time.Sleep(10 * time.Second)
		err = fn()
	}
	return err
}

func waitReady(b *box) {
	deadline := time.Now().Add(readyTimeout)
	for {
		resp, err := b.http.Get(b.base + "/readyz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		state, serr := b.c.State(context.Background())
		if serr == nil && !state.Running {
			Fail(fmt.Sprintf("the container stopped before it was ready (exit code %d)", state.ExitCode))
		}
		if time.Now().After(deadline) {
			Fail(fmt.Sprintf("/readyz did not answer 200 within %s", readyTimeout))
		}
		time.Sleep(2 * time.Second)
	}
}

// processUID reads the uid of process 1 of the container, which is LocalAI
// after the entrypoint dropped its privileges.
func processUID(b *box) int {
	code, r, err := b.c.Exec(context.Background(), []string{"stat", "-c", "%u", "/proc/1"})
	Expect(err).NotTo(HaveOccurred())
	out, _ := io.ReadAll(r)
	Expect(code).To(BeZero(), string(out))
	// Exec output is multiplexed; keep the digits only.
	digits := regexp.MustCompile(`\d+`).FindAllString(string(out), -1)
	Expect(digits).NotTo(BeEmpty(), string(out))
	n, err := strconv.Atoi(digits[len(digits)-1])
	Expect(err).NotTo(HaveOccurred())
	return n
}

func lineWith(logs, needle string) string {
	for _, l := range strings.Split(logs, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	return ""
}

// call sends a JSON request the way the page of the host does: a same-origin
// fetch, which the add-on routes require from a caller without a session.
func (b *box) call(method, path string, body any, out any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, b.base+path, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Origin", b.base)
	resp, err := b.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if out != nil && resp.StatusCode/100 == 2 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, raw, fmt.Errorf("decode %s %s: %w: %s", method, path, err, raw)
		}
	}
	return resp.StatusCode, raw, nil
}

func newOperationID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// dispatch sends one typed add-on request and returns its payload.
func (b *box) dispatch(action, addon string, args map[string]any, operationID string) (map[string]any, error) {
	body := map[string]any{"operationId": operationID, "addonId": addon, "generation": 0, "action": action, "arguments": args}
	var out struct {
		Payload map[string]any `json:"payload"`
	}
	code, raw, err := b.call(http.MethodPost, "/api/addons/dispatch", body, &out)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", action, err)
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d: %s", action, code, raw)
	}
	return out.Payload, nil
}

func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// installAddon finds the add-on in the official library, installs it and
// waits for the operation to end.
func installAddon(b *box) error {
	found, err := b.dispatch("catalog-search", "", map[string]any{"text": addonID, "kind": "addon", "sourceId": addonSourceID, "offset": 0, "limit": 50}, newOperationID())
	if err != nil {
		return err
	}
	var version string
	releases, _ := found["releases"].([]any)
	for _, r := range releases {
		rel, _ := r.(map[string]any)
		if str(rel, "id", "addonId") == addonID {
			version = str(rel, "version")
			break
		}
	}
	if version == "" {
		return fmt.Errorf("catalog-search: %s is not in the %s library: %v", addonID, addonSourceID, found)
	}

	op := newOperationID()
	status, err := b.dispatch("install", addonID, map[string]any{"sourceId": addonSourceID, "version": version}, op)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(installTimeout)
	for {
		phase := str(status, "Phase", "phase")
		switch phase {
		case "completed":
			return checkAddonInstalled(b)
		case "failed":
			return fmt.Errorf("install of %s failed: %v; log: %s", addonID, status, lastAddonFailure(b))
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("install of %s did not end within %s: %v", addonID, installTimeout, status)
		}
		time.Sleep(2 * time.Second)
		if status, err = b.dispatch("operation-status", addonID, nil, op); err != nil {
			return err
		}
	}
}

// lastAddonFailure is the log line that names the check behind a failed
// add-on operation; the API answer has a fixed text only.
func lastAddonFailure(b *box) string {
	logs := b.logs()
	line := ""
	for _, l := range strings.Split(logs, "\n") {
		if strings.Contains(l, "Addon operation failed") {
			line = l
		}
	}
	return line
}

// installBackend installs the backend from the backend library and waits
// for the job to end.
func installBackend(b *box) error {
	var job struct {
		ID string `json:"id"`
	}
	code, raw, err := b.call(http.MethodPost, "/backends/apply", map[string]any{"id": backendID}, &job)
	if err != nil {
		return err
	}
	if code != http.StatusOK || job.ID == "" {
		return fmt.Errorf("backends/apply: HTTP %d: %s", code, raw)
	}
	deadline := time.Now().Add(installTimeout)
	for {
		var st struct {
			Processed bool   `json:"processed"`
			Error     string `json:"error"`
			Message   string `json:"message"`
		}
		code, raw, err := b.call(http.MethodGet, "/backends/jobs/"+job.ID, nil, &st)
		if err != nil {
			return err
		}
		if code != http.StatusOK {
			return fmt.Errorf("backends/jobs: HTTP %d: %s", code, raw)
		}
		if st.Processed {
			if st.Error != "" {
				return fmt.Errorf("install of %s failed: %s", backendID, st.Error)
			}
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("install of %s did not end within %s: %s", backendID, installTimeout, raw)
		}
		time.Sleep(2 * time.Second)
	}
}

// checkAddonInstalled reads the installed add-ons back, so a completed
// operation that left nothing behind does not pass.
func checkAddonInstalled(b *box) error {
	out, err := b.dispatch("addons-installed", "", nil, newOperationID())
	if err != nil {
		return err
	}
	list, _ := out["addons"].([]any)
	for _, a := range list {
		if m, _ := a.(map[string]any); str(m, "id", "ID", "addonId") == addonID {
			return nil
		}
	}
	return fmt.Errorf("install of %s completed but it is not installed: %v", addonID, out)
}
