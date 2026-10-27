package gvisor

import (
	"encoding/json"
	"strings"
	"testing"
)

// wantCaps is the spec's six capabilities plus CAP_SETGID, written out so the
// tests do not compare initCaps with itself.
var wantCaps = []string{"CAP_SYS_ADMIN", "CAP_MKNOD", "CAP_DAC_OVERRIDE", "CAP_CHOWN", "CAP_FOWNER", "CAP_FSETID", "CAP_SETGID"}

func TestBundleConfig(t *testing.T) {
	raw, err := bundleConfig(bundleParams{
		Rootfs: "/cache/img/rootfs", Lower: "/s/a/lower", Upper: "/s/a/upper", Ctl: "/s/a/ctl",
		HostUID: 4242, HostGID: 4343, Ports: []int{3128, 7000}, Disk: 1 << 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Process struct {
			User         struct{ UID, GID int }
			Args, Env    []string
			Capabilities map[string][]string
			Rlimits      []struct {
				Type       string
				Hard, Soft uint64
			}
			NoNewPrivileges bool
			Terminal        bool
		}
		Root struct {
			Path     string
			Readonly bool
		}
		Mounts []struct {
			Destination, Type, Source string
			Options                   []string
		}
		Linux struct {
			Namespaces []struct{ Type string }
			UIDMap     []struct{ ContainerID, HostID, Size int } `json:"uidMappings"`
			GIDMap     []struct{ ContainerID, HostID, Size int } `json:"gidMappings"`
		}
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.Process.User.UID != 1000 || c.Process.User.GID != 1000 || c.Process.Terminal {
		t.Fatalf("user = %+v", c.Process)
	}
	if strings.Join(c.Process.Args, " ") != "/sbin/sandbox-agent" {
		t.Fatalf("args = %v", c.Process.Args)
	}
	env := strings.Join(c.Process.Env, "\n")
	for _, want := range []string{"SANDBOX_AGENT_CTL=/mnt/ctl", "SANDBOX_AGENT_PORTS=3128,7000", "HOME=/home/agent"} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %s: %s", want, env)
		}
	}
	for _, set := range []string{"bounding", "effective", "permitted", "inheritable", "ambient"} {
		if strings.Join(c.Process.Capabilities[set], ",") != strings.Join(wantCaps, ",") {
			t.Errorf("%s = %v", set, c.Process.Capabilities[set])
		}
	}
	if len(c.Process.Rlimits) != 1 || c.Process.Rlimits[0].Type != "RLIMIT_FSIZE" || c.Process.Rlimits[0].Hard != 1<<30 || c.Process.Rlimits[0].Soft != 1<<30 {
		t.Fatalf("rlimits = %+v", c.Process.Rlimits)
	}
	if c.Root.Path != "/cache/img/rootfs" || !c.Root.Readonly {
		t.Fatalf("root = %+v", c.Root)
	}
	binds := map[string]string{}
	for _, m := range c.Mounts {
		if m.Type == "bind" {
			binds[m.Destination] = m.Source + " " + strings.Join(m.Options, ",")
		}
	}
	want := map[string]string{
		"/mnt/lower": "/s/a/lower rbind,ro",
		"/mnt/upper": "/s/a/upper rbind,rw",
		"/mnt/ctl":   "/s/a/ctl rbind,rw",
	}
	if len(binds) != len(want) {
		t.Fatalf("binds = %v", binds)
	}
	for k, v := range want {
		if binds[k] != v {
			t.Errorf("bind %s = %q, want %q", k, binds[k], v)
		}
	}
	// One id map: guest 1000 to the host user. No wider map, because the host
	// has no newuidmap.
	if len(c.Linux.UIDMap) != 1 || c.Linux.UIDMap[0].ContainerID != 1000 || c.Linux.UIDMap[0].HostID != 4242 || c.Linux.UIDMap[0].Size != 1 {
		t.Fatalf("uid map = %+v", c.Linux.UIDMap)
	}
	if len(c.Linux.GIDMap) != 1 || c.Linux.GIDMap[0].ContainerID != 1000 || c.Linux.GIDMap[0].HostID != 4343 || c.Linux.GIDMap[0].Size != 1 {
		t.Fatalf("gid map = %+v", c.Linux.GIDMap)
	}
	ns := ""
	for _, n := range c.Linux.Namespaces {
		ns += n.Type + " "
	}
	if ns != "pid ipc uts mount network user " {
		t.Fatalf("namespaces = %q", ns)
	}
}

func TestInitCapsAreTheSpecsPlusSetgid(t *testing.T) {
	// The spec names six; CAP_SETGID is added because the agent's spawner calls
	// setgroups for every command.
	if got, want := strings.Join(initCaps, " "), strings.Join(wantCaps, " "); got != want {
		t.Fatalf("caps = %s, want %s", got, want)
	}
}

func TestProfileNamesOnlyTheBinary(t *testing.T) {
	p := Profile("/opt/gvisor/runsc")
	for _, want := range []string{"abi <abi/4.0>,", "include <tunables/global>", `"/opt/gvisor/runsc" flags=(unconfined) {`, "  userns,\n}"} {
		if !strings.Contains(p, want) {
			t.Errorf("profile lacks %q:\n%s", want, p)
		}
	}
	if p == Profile("/opt/other/runsc") {
		t.Fatal("two paths gave one profile (the profile name must differ)")
	}
}

func TestCheckConfigNeedsAnAbsoluteProgram(t *testing.T) {
	if _, err := checkConfig("true", 1, 1); err == nil {
		t.Fatal("relative program accepted")
	}
	raw, err := checkConfig("/bin/true", 1000, 1000)
	if err != nil || !strings.Contains(string(raw), `"/bin/true"`) {
		t.Fatalf("%s %v", raw, err)
	}
}

func TestValidateProfilePath(t *testing.T) {
	for _, ok := range []string{"/opt/gvisor/runsc", "/home/u/my runsc/runsc", "/opt/\u00e9/runsc"} {
		if err := ValidateProfilePath(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "runsc", "./runsc", "/opt/../runsc", "/opt//runsc", "/opt/runsc/",
		"/opt/a\"b/runsc", "/opt/a\\b/runsc", "/opt/a{b}/runsc", "/opt/a[b]/runsc",
		"/opt/a*/runsc", "/opt/a?/runsc", "/opt/a^b/runsc",
		"/opt/a\nb/runsc", "/opt/a\u2028b/runsc", "/opt/a\u00a0b/runsc", "/opt/a\rb/runsc", "/opt/a\x00b/runsc", "/opt/a\tb/runsc", "/opt/a\xffb/runsc",
	} {
		if err := ValidateProfilePath(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestBundleConfigRefusesANonPositiveDisk(t *testing.T) {
	for _, d := range []int64{0, -1} {
		if _, err := bundleConfig(bundleParams{Rootfs: "/r", Lower: "/l", Upper: "/u", Ctl: "/c", Disk: d}); err == nil {
			t.Errorf("disk %d accepted", d)
		}
	}
}
