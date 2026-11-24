package removedoptions_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	. "github.com/mudler/LocalAI/core/cli/removedoptions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRemovedOptions(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Removed options test suite")
}

// names returns the names of the findings.
func names(fs []Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Name
	}
	return out
}

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

var _ = Describe("Check", func() {
	It("finds nothing on a clean start", func() {
		Expect(Check(Input{Args: []string{"run", "--address=:8080"}, Env: env(nil)})).To(BeEmpty())
		Expect(Message(nil)).To(BeEmpty())
	})

	DescribeTable("names a removed command and its replacement",
		func(args []string, name, replacement string) {
			fs := Check(Input{Args: args, Env: env(nil)})
			Expect(fs).To(HaveLen(1))
			Expect(fs[0].Kind).To(Equal(Command))
			Expect(fs[0].Name).To(Equal(name))
			Expect(fs[0].Replacement).To(ContainSubstring(replacement))
		},
		Entry("llama.cpp worker", []string{"p2p-worker", "p2p-llama-cpp-rpc", "--token", "x"}, "p2p-worker p2p-llama-cpp-rpc", "local-ai worker llama-cpp-rpc"),
		Entry("llama.cpp worker under the new name", []string{"worker", "p2p-llama-cpp-rpc"}, "worker p2p-llama-cpp-rpc", "local-ai worker llama-cpp-rpc"),
		Entry("mlx worker", []string{"p2p-worker", "p2p-mlx"}, "p2p-worker p2p-mlx", "local-ai worker mlx-distributed"),
		Entry("federated", []string{"federated"}, "local-ai federated", "federation groups of P2P remotes"),
		Entry("explorer", []string{"explorer", "--address", ":8080"}, "local-ai explorer", "no replacement"),
	)

	It("does not touch the commands that stay", func() {
		for _, args := range [][]string{{"worker", "--register-to=x"}, {"p2p-worker", "vllm"}, {"worker", "llama-cpp-rpc"}} {
			Expect(Check(Input{Args: args, Env: env(nil)})).To(BeEmpty(), "%v", args)
		}
	})

	It("names the replacement of --p2p as --p2p-mode", func() {
		fs := Check(Input{Args: []string{"run", "--p2p"}, Env: env(nil)})
		Expect(fs).To(HaveLen(1))
		Expect(fs[0].Replacement).To(ContainSubstring("--p2p-mode connect"))
		Expect(fs[0].Replacement).To(ContainSubstring("share"))
	})

	It("finds every removed flag, with and without a value", func() {
		fs := Check(Input{Args: []string{"--p2p", "--p2p-token=abc", "--p2ptoken", "abc", "--p2p-network-id=n", "--peer-2-peer-network-id=n",
			"--p2p-dht-interval=1", "--p2p-otp-interval=1", "--p2p-enable-limits=true", "--federated"}, Env: env(nil)})
		Expect(names(fs)).To(ConsistOf("--p2p", "--p2p-token", "--p2ptoken", "--p2p-network-id", "--peer-2-peer-network-id",
			"--p2p-dht-interval", "--p2p-otp-interval", "--p2p-enable-limits", "--federated"))
	})

	It("does not mistake a longer flag for a removed one", func() {
		Expect(Check(Input{Args: []string{"--p2p-mode=connect", "--p2p-listen=/ip4/0.0.0.0/tcp/0"}, Env: env(nil)})).To(BeEmpty())
	})

	It("finds the old environment variables, but not the ones that were kept", func() {
		fs := Check(Input{Env: env(map[string]string{
			"LOCALAI_P2P": "true", "P2P": "true", "LOCALAI_P2P_TOKEN": "t", "P2P_TOKEN": "t", "LOCALAI_TOKEN": "t", "TOKEN": "t",
			"LOCALAI_P2P_NETWORK_ID": "n", "P2P_NETWORK_ID": "n", "LOCALAI_P2P_DHT_INTERVAL": "1", "LOCALAI_P2P_OTP_INTERVAL": "1",
			"LOCALAI_P2P_ENABLE_LIMITS": "true", "LOCALAI_P2P_DISABLE_DHT": "true", "LOCALAI_P2P_DHT_ANNOUNCE_MADDRS": "x",
			"LOCALAI_FEDERATED": "true", "FEDERATED": "true", "LOCALAI_P2P_LOGLEVEL": "debug", "LOCALAI_P2P_LIB_LOGLEVEL": "debug",
			"LOCALAI_P2P_LISTEN_MADDRS": "x", "LOCALAI_P2P_BOOTSTRAP_PEERS_MADDRS": "x", "LOCALAI_P2P_MODE": "connect",
		})})
		Expect(names(fs)).To(ConsistOf("LOCALAI_P2P", "P2P", "LOCALAI_P2P_TOKEN", "P2P_TOKEN", "LOCALAI_TOKEN", "TOKEN",
			"LOCALAI_P2P_NETWORK_ID", "P2P_NETWORK_ID", "LOCALAI_P2P_DHT_INTERVAL", "LOCALAI_P2P_OTP_INTERVAL",
			"LOCALAI_P2P_ENABLE_LIMITS", "LOCALAI_P2P_DISABLE_DHT", "LOCALAI_P2P_DHT_ANNOUNCE_MADDRS", "LOCALAI_FEDERATED", "FEDERATED",
			"LOCALAI_P2P_LOGLEVEL", "LOCALAI_P2P_LIB_LOGLEVEL"))
		for _, f := range fs {
			Expect(f.Replacement).ToNot(BeEmpty(), f.Name)
		}
	})

	It("ignores an empty variable", func() {
		Expect(Check(Input{Env: env(map[string]string{"TOKEN": "", "LOCALAI_P2P": ""})})).To(BeEmpty())
	})

	It("does not check options of commands that never read them", func() {
		Expect(Check(Input{Args: []string{"models", "list"}, Env: env(map[string]string{"TOKEN": "t", "P2P": "1"})})).To(BeEmpty())
	})

	It("only warns about the bare names that other programs may set", func() {
		fs := Check(Input{Args: []string{"run"}, Env: env(map[string]string{"TOKEN": "hf_abc", "P2P": "banana", "TARGET_WORKER": "w"})})
		Expect(names(fs)).To(ConsistOf("TOKEN", "P2P", "TARGET_WORKER"))
		Expect(Fatal(fs)).To(BeEmpty())
		Expect(Message(fs)).To(BeEmpty())
		Expect(Warning(fs)).To(ContainSubstring("warning: environment variable TOKEN"))
	})

	It("ignores a bare boolean that is false and fails on one that is true", func() {
		Expect(Check(Input{Env: env(map[string]string{"P2P": "false", "FEDERATED": "0"})})).To(BeEmpty())
		fs := Check(Input{Env: env(map[string]string{"P2P": "true"})})
		Expect(names(Fatal(fs))).To(ConsistOf("P2P"))
	})

	It("always fails on a name with the LOCALAI_ prefix", func() {
		fs := Check(Input{Env: env(map[string]string{"LOCALAI_TOKEN": "x", "LOCALAI_P2P": "false"})})
		Expect(names(Fatal(fs))).To(ConsistOf("LOCALAI_TOKEN", "LOCALAI_P2P"))
	})

	It("does not take the value of a flag for the command", func() {
		// The value "models" of --backend must not turn `run` into a command
		// that never reads the options, and a flag value before the command
		// must not hide it.
		Expect(Check(Input{Args: []string{"--address", ":8080", "models", "list"}, Env: env(map[string]string{"LOCALAI_P2P": "1"})})).To(BeEmpty())
		fs := Check(Input{Args: []string{"--address", ":8080", "run", "--p2p"}, Env: env(nil)})
		Expect(names(fs)).To(ConsistOf("--p2p"))
		fs = Check(Input{Args: []string{"--models-path", "/data", "--p2p"}, Env: env(map[string]string{"LOCALAI_P2P": "1"})})
		Expect(names(fs)).To(ConsistOf("--p2p", "LOCALAI_P2P"))
		fs = Check(Input{Args: []string{"worker", "--register-to", "http://f", "p2p-mlx"}, Env: env(nil)})
		Expect(names(fs)).To(ConsistOf("worker p2p-mlx"))
	})

	It("finds the settings that are still set and ignores the unset ones", func() {
		s := map[string]json.RawMessage{
			"p2p_token": json.RawMessage(`"abc"`), "p2p_network_id": json.RawMessage(`"net"`), "federated": json.RawMessage(`true`),
		}
		Expect(names(Check(Input{Settings: s}))).To(ConsistOf("p2p_token", "p2p_network_id", "federated"))
		s = map[string]json.RawMessage{"p2p_token": json.RawMessage(`""`), "federated": json.RawMessage(`false`), "p2p_network_id": json.RawMessage(`null`)}
		Expect(Check(Input{Settings: s})).To(BeEmpty())
	})

	It("prints one line per finding with its replacement", func() {
		fs := Check(Input{Args: []string{"--p2p"}, Env: env(map[string]string{"TOKEN": "t"})})
		msg := Message(fs)
		Expect(msg).To(ContainSubstring("flag --p2p was removed: use --p2p-mode"))
		Expect(msg).ToNot(ContainSubstring("TOKEN"), "a bare TOKEN only warns")
		Expect(Warning(fs)).To(ContainSubstring("environment variable TOKEN"))
		Expect(Warning(fs)).To(ContainSubstring("invite created under Swarm, P2P"))
	})
})

var _ = Describe("ReadSettings", func() {
	It("reads the file and tolerates a missing or broken one", func() {
		dir := GinkgoT().TempDir()
		Expect(ReadSettings(dir)).To(BeNil())
		Expect(ReadSettings("")).To(BeNil())
		Expect(os.WriteFile(filepath.Join(dir, "runtime_settings.json"), []byte(`{"p2p_token":"x"}`), 0o600)).To(Succeed())
		Expect(names(Check(Input{Settings: ReadSettings(dir)}))).To(ConsistOf("p2p_token"))
		Expect(os.WriteFile(filepath.Join(dir, "runtime_settings.json"), []byte(`{`), 0o600)).To(Succeed())
		Expect(ReadSettings(dir)).To(BeNil())
	})
})
