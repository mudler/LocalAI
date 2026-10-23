// SPDX-License-Identifier: MIT
package launcher_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	launcher "github.com/mudler/LocalAI/cmd/launcher/internal"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Secure launcher configuration", func() {
	var home, path string
	var l *launcher.Launcher
	var configApp fyne.App
	BeforeEach(func() {
		// Keep Fyne's asynchronous settings watcher outside per-spec HOME.
		if configApp == nil {
			configApp = app.NewWithID("config.test")
		}
		originalHome, err := os.UserHomeDir()
		Expect(err).NotTo(HaveOccurred())
		GinkgoT().Setenv("XDG_CONFIG_HOME", filepath.Join(originalHome, ".config"))
		home = GinkgoT().TempDir()
		GinkgoT().Setenv("HOME", home)
		path = filepath.Join(home, ".localai", "launcher.json")
		Expect(os.MkdirAll(filepath.Dir(path), 0755)).To(Succeed())
		l = launcher.NewLauncher(nil, nil, configApp)
	})
	It("migrates all legacy settings to separate schema v2 profiles without logging values", func() {
		input := `{"models_path":"MODELS","backends_path":"BACKENDS","address":"localhost:8123","log_level":"debug","auto_start_server":false,"show_welcome":false,"start_on_boot":true,"environment_vars":{"API_KEY":"environment-secret"}}`
		input = strings.ReplaceAll(input, "MODELS", filepath.Join(home, "models-secret"))
		input = strings.ReplaceAll(input, "BACKENDS", filepath.Join(home, "backends-secret"))
		Expect(os.WriteFile(path, []byte(input), 0644)).To(Succeed())
		var logs bytes.Buffer
		original := log.Writer()
		log.SetOutput(&logs)
		defer log.SetOutput(original)
		Expect(l.Initialize()).To(Succeed())
		Expect(l.ShouldAutoStartServer()).To(BeFalse())
		data, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		var saved map[string]any
		Expect(json.Unmarshal(data, &saved)).To(Succeed())
		Expect(saved["schema_version"]).To(Equal(float64(2)))
		Expect(saved["mode"]).To(Equal("standalone"))
		Expect(saved["shared"]).To(HaveKeyWithValue("models_path", filepath.Join(home, "models-secret")))
		Expect(saved["shared"]).To(HaveKeyWithValue("auto_start_server", false))
		Expect(saved["shared"]).To(HaveKeyWithValue("show_welcome", false))
		Expect(saved["shared"]).To(HaveKeyWithValue("log_level", "debug"))
		Expect(l.GetConfig().Worker).To(Equal(launcher.WorkerConfig{}))
		Expect(l.BuildRunArgs()).To(ContainElements("run", "--address", "localhost:8123"))
		Expect(saved["legacy"]).To(HaveKeyWithValue("start_on_boot", true))
		Expect(saved["standalone"]).To(HaveKeyWithValue("environment_vars", map[string]any{"API_KEY": "environment-secret"}))
		Expect(saved).NotTo(HaveKey("environment_vars"))
		for _, secret := range []string{filepath.Join(home, "models-secret"), filepath.Join(home, "backends-secret"), "localhost:8123", "environment-secret"} {
			Expect(logs.String()).NotTo(ContainSubstring(secret))
		}
		info, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0600)))
	})
	It("retains startup tri-state and ignores obsolete auto_start", func() {
		for _, input := range []string{`{}`, `{"auto_start":false}`, `{"auto_start_server":false}`, `{"auto_start_server":true}`} {
			var c launcher.Config
			Expect(json.Unmarshal([]byte(input), &c)).To(Succeed())
			Expect(l.SetConfig(&c)).To(Succeed())
			Expect(l.Initialize()).To(Succeed())
			Expect(l.ShouldAutoStartServer()).To(Equal(c.AutoStart == nil || *c.AutoStart))
			data, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			var saved map[string]any
			Expect(json.Unmarshal(data, &saved)).To(Succeed())
			Expect(saved).To(HaveKey("shared"))
			shared := saved["shared"].(map[string]any)
			if c.AutoStart == nil {
				Expect(shared["auto_start_server"]).To(BeNil())
			} else {
				Expect(shared["auto_start_server"]).To(Equal(*c.AutoStart))
			}
		}
	})
	It("rejects a symlink destination without changing the target or active settings", func() {
		target := filepath.Join(home, "target")
		Expect(os.WriteFile(target, []byte("unchanged"), 0644)).To(Succeed())
		Expect(os.Symlink(target, path)).To(Succeed())
		previous := l.GetConfig().Address
		c := l.GetConfig()
		c.Address = "changed"
		Expect(l.SetConfig(c)).NotTo(Succeed())
		Expect(l.GetConfig().Address).To(Equal(previous))
		Expect(l.ShouldAutoStartServer()).To(BeFalse())
		data, err := os.ReadFile(target)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("unchanged"))
		Expect(l.Initialize()).NotTo(Succeed())
	})
	It("rejects unknown schema and mode without exposing their content", func() {
		for _, input := range []string{`{"schema_version":99,"mode":"standalone"}`, `{"schema_version":2,"mode":"secret-mode"}`} {
			Expect(os.WriteFile(path, []byte(input), 0600)).To(Succeed())
			err := l.Initialize()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring("secret-mode"))
			Expect(l.ShouldAutoStartServer()).To(BeFalse())
		}
	})
	It("round-trips worker secrets and independent environments without logging or fallback", func() {
		input := `{"schema_version":2,"mode":"worker","shared":{"auto_start_server":false},"standalone":{"address":"localhost:8123","environment_vars":{"STANDALONE":"standalone-secret"}},"worker":{"connection":{"frontend_url":"https://example.test","registration_token":"token-secret","advertised_address":"worker.test:50052","node_name":"node","grpc_max_port":50100},"nats":{"url":"tls://nats.test:4222","tls_ca":"ca.pem","tls_cert":"cert.pem","tls_key":"key.pem","jwt":"jwt-secret","user_seed":"seed-secret"},"environment_vars":{"WORKER":"worker-secret"}}}`
		var c launcher.Config
		Expect(json.Unmarshal([]byte(input), &c)).To(Succeed())
		var logs bytes.Buffer
		original := log.Writer()
		log.SetOutput(&logs)
		defer log.SetOutput(original)
		Expect(l.SetConfig(&c)).To(Succeed())
		Expect(l.Initialize()).To(Succeed())
		Expect(l.ShouldAutoStartServer()).To(BeFalse())
		Expect(l.StartLocalAI()).NotTo(Succeed(), "no runtime is installed here, so nothing may start")
		Expect(l.IsRunning()).To(BeFalse())
		worker := l.GetConfig().Worker
		for _, mode := range []string{"standalone", "worker", "standalone"} {
			draft := l.GetConfig()
			draft.Mode = mode
			Expect(l.SetConfig(draft)).To(Succeed())
			Expect(l.GetConfig().Worker).To(Equal(worker))
			Expect(l.GetConfig().EnvironmentVars).To(Equal(map[string]string{"STANDALONE": "standalone-secret"}))
		}
		for _, secret := range []string{"token-secret", "jwt-secret", "seed-secret", "worker-secret", "standalone-secret"} {
			Expect(logs.String()).NotTo(ContainSubstring(secret))
		}
		draft := l.GetConfig()
		draft.Worker.Connection.RegistrationToken = ""
		draft.Worker.NATS.JWT = ""
		draft.Worker.NATS.UserSeed = ""
		Expect(l.SetConfig(draft)).To(Succeed())
		Expect(l.GetConfig().Worker.Connection.RegistrationToken).To(BeEmpty())
	})
	It("returns isolated drafts and atomically replaces the old inode", func() {
		c := l.GetConfig()
		c.EnvironmentVars = map[string]string{"KEY": "original"}
		Expect(l.SetConfig(c)).To(Succeed())
		c.EnvironmentVars["KEY"] = "external mutation"
		draft := l.GetConfig()
		draft.EnvironmentVars["KEY"] = "cancelled"
		Expect(l.GetConfig().EnvironmentVars["KEY"]).To(Equal("original"))
		old, err := os.Open(path)
		Expect(err).NotTo(HaveOccurred())
		defer old.Close()
		before, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(l.SetConfig(draft)).To(Succeed())
		oldData, err := io.ReadAll(old)
		Expect(err).NotTo(HaveOccurred())
		Expect(oldData).To(Equal(before))
		after, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(after).NotTo(Equal(before))
		files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".launcher-*.tmp"))
		Expect(err).NotTo(HaveOccurred())
		Expect(files).To(BeEmpty())
	})
	It("rejects unsafe parent permissions, directories and hard links", func() {
		Expect(os.Chmod(filepath.Dir(path), 0777)).To(Succeed())
		Expect(l.SetConfig(l.GetConfig())).NotTo(Succeed())
		Expect(os.Chmod(filepath.Dir(path), 0755)).To(Succeed())
		Expect(os.Mkdir(path, 0700)).To(Succeed())
		Expect(l.SetConfig(l.GetConfig())).NotTo(Succeed())
		Expect(os.Remove(path)).To(Succeed())
		target := filepath.Join(home, "target")
		Expect(os.WriteFile(target, []byte("untouched"), 0644)).To(Succeed())
		Expect(os.Link(target, path)).To(Succeed())
		Expect(l.Initialize()).NotTo(Succeed())
		info, err := os.Stat(target)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0644)))
	})
	It("does not follow a symlink configuration directory", func() {
		Expect(os.Remove(filepath.Dir(path))).To(Succeed())
		target := filepath.Join(home, "elsewhere")
		Expect(os.Mkdir(target, 0700)).To(Succeed())
		Expect(os.Symlink(target, filepath.Dir(path))).To(Succeed())
		Expect(l.SetConfig(l.GetConfig())).NotTo(Succeed())
		entries, err := os.ReadDir(target)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})
	It("hardens malformed files before decoding and never includes their payload in errors", func() {
		for _, input := range []string{`{"secret-error":`, `{"schema_version":"secret-error"}`, ` null `} {
			Expect(os.WriteFile(path, []byte(input), 0644)).To(Succeed())
			Expect(os.Chmod(path, 0644)).To(Succeed())
			err := l.Initialize()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring("secret-error"))
			info, err := os.Stat(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0600)))
			data, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal(input))
		}
	})

	It("refuses foreign ownership and real permission failures", func() {
		if os.Getenv("LAUNCHER_CONFIG_PERMISSION_CHILD") == "1" {
			// The parent supplies an inaccessible file and an unwritable directory.
			foreign := os.Getenv("LAUNCHER_CONFIG_FOREIGN_HOME")
			GinkgoT().Setenv("HOME", foreign)
			err := l.SetConfig(l.GetConfig())
			Expect(err).To(HaveOccurred())
			Expect(l.ShouldAutoStartServer()).To(BeFalse())
			GinkgoT().Setenv("HOME", os.Getenv("LAUNCHER_CONFIG_READONLY_HOME"))
			Expect(l.SetConfig(l.GetConfig())).NotTo(Succeed())
			return
		}
		if os.Geteuid() != 0 {
			Expect(os.Chmod(filepath.Dir(path), 0500)).To(Succeed())
			defer os.Chmod(filepath.Dir(path), 0700)
			Expect(l.SetConfig(l.GetConfig())).NotTo(Succeed())
			return
		}
		Expect(os.WriteFile(path, []byte(`{}`), 0600)).To(Succeed())
		Expect(os.Chown(path, 65534, 65534)).To(Succeed())
		Expect(l.Initialize()).NotTo(Succeed())
		Expect(os.Chown(path, 0, 0)).To(Succeed())
		// Run the same assertion without root's DAC override in the isolated runner.
		scratch, err := os.MkdirTemp("", "config-permission-")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(scratch)
		Expect(os.Chmod(scratch, 0755)).To(Succeed())
		foreign := filepath.Join(scratch, "foreign")
		readonly := filepath.Join(scratch, "readonly")
		for _, h := range []string{foreign, readonly} {
			Expect(os.MkdirAll(filepath.Join(h, ".localai"), 0755)).To(Succeed())
		}
		Expect(os.Chown(readonly, 65534, 65534)).To(Succeed())
		Expect(os.Chown(filepath.Join(readonly, ".localai"), 65534, 65534)).To(Succeed())
		Expect(os.Chmod(filepath.Join(readonly, ".localai"), 0500)).To(Succeed())
		executable, err := os.Executable()
		Expect(err).NotTo(HaveOccurred())
		binary, err := os.ReadFile(executable)
		Expect(err).NotTo(HaveOccurred())
		helper := filepath.Join(scratch, "config.test")
		Expect(os.WriteFile(helper, binary, 0755)).To(Succeed())
		cmd := exec.Command(helper, "-test.run=TestLauncher", "-ginkgo.focus=refuses foreign ownership and real permission failures", "-ginkgo.no-color")
		cmd.Env = append(os.Environ(), "LAUNCHER_CONFIG_PERMISSION_CHILD=1", "LAUNCHER_CONFIG_FOREIGN_HOME="+foreign, "LAUNCHER_CONFIG_READONLY_HOME="+readonly)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
		output, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
	})

})
