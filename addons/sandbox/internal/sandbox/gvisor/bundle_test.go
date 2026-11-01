package gvisor

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// wantCaps is the spec's six capabilities plus CAP_SETGID, written out so the
// tests do not compare initCaps with itself.
var wantCaps = []string{"CAP_SYS_ADMIN", "CAP_MKNOD", "CAP_DAC_OVERRIDE", "CAP_CHOWN", "CAP_FOWNER", "CAP_FSETID", "CAP_SETGID"}

var _ = Describe("bundle", func() {
	It("builds the bundle config", func() {
		raw, err := bundleConfig(bundleParams{
			Rootfs: "/cache/img/rootfs", Lower: "/s/a/lower", Upper: "/s/a/upper", Ctl: "/s/a/ctl",
			HostUID: 4242, HostGID: 4343, Ports: []int{3128, 7000}, Disk: 1 << 30,
		})
		Expect(err).ToNot(HaveOccurred())
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
		Expect(json.Unmarshal(raw, &c)).To(Succeed())
		Expect(c.Process.User.UID).To(Equal(1000), "user = %+v", c.Process)
		Expect(c.Process.User.GID).To(Equal(1000), "user = %+v", c.Process)
		Expect(c.Process.Terminal).To(BeFalse(), "user = %+v", c.Process)
		Expect(strings.Join(c.Process.Args, " ")).To(Equal("/sbin/sandbox-agent"))
		env := strings.Join(c.Process.Env, "\n")
		for _, want := range []string{"SANDBOX_AGENT_CTL=/mnt/ctl", "SANDBOX_AGENT_PORTS=3128,7000", "HOME=/home/agent"} {
			Expect(env).To(ContainSubstring(want), "env lacks %s", want)
		}
		for _, set := range []string{"bounding", "effective", "permitted", "inheritable", "ambient"} {
			Expect(strings.Join(c.Process.Capabilities[set], ",")).To(Equal(strings.Join(wantCaps, ",")), set)
		}
		Expect(c.Process.Rlimits).To(HaveLen(1))
		Expect(c.Process.Rlimits[0].Type).To(Equal("RLIMIT_FSIZE"))
		Expect(c.Process.Rlimits[0].Hard).To(Equal(uint64(1 << 30)))
		Expect(c.Process.Rlimits[0].Soft).To(Equal(uint64(1 << 30)))
		Expect(c.Root.Path).To(Equal("/cache/img/rootfs"))
		Expect(c.Root.Readonly).To(BeTrue())
		binds := map[string]string{}
		for _, m := range c.Mounts {
			if m.Type == "bind" {
				binds[m.Destination] = m.Source + " " + strings.Join(m.Options, ",")
			}
		}
		Expect(binds).To(Equal(map[string]string{
			"/mnt/lower": "/s/a/lower rbind,ro",
			"/mnt/upper": "/s/a/upper rbind,rw",
			"/mnt/ctl":   "/s/a/ctl rbind,rw",
		}))
		// One id map: guest 1000 to the host user. No wider map, because the host
		// has no newuidmap.
		Expect(c.Linux.UIDMap).To(HaveLen(1))
		Expect(c.Linux.UIDMap[0].ContainerID).To(Equal(1000))
		Expect(c.Linux.UIDMap[0].HostID).To(Equal(4242))
		Expect(c.Linux.UIDMap[0].Size).To(Equal(1))
		Expect(c.Linux.GIDMap).To(HaveLen(1))
		Expect(c.Linux.GIDMap[0].ContainerID).To(Equal(1000))
		Expect(c.Linux.GIDMap[0].HostID).To(Equal(4343))
		Expect(c.Linux.GIDMap[0].Size).To(Equal(1))
		ns := ""
		for _, n := range c.Linux.Namespaces {
			ns += n.Type + " "
		}
		Expect(ns).To(Equal("pid ipc uts mount network user "))
	})

	It("uses the spec's init capabilities plus setgid", func() {
		// The spec names six; CAP_SETGID is added because the agent's spawner calls
		// setgroups for every command.
		Expect(strings.Join(initCaps, " ")).To(Equal(strings.Join(wantCaps, " ")))
	})

	It("names only the binary in the profile", func() {
		p := Profile("/opt/gvisor/runsc")
		for _, want := range []string{"abi <abi/4.0>,", "include <tunables/global>", `"/opt/gvisor/runsc" flags=(unconfined) {`, "  userns,\n}"} {
			Expect(p).To(ContainSubstring(want))
		}
		Expect(p).ToNot(Equal(Profile("/opt/other/runsc")), "two paths gave one profile (the profile name must differ)")
	})

	It("needs an absolute program for the check config", func() {
		_, err := checkConfig("true", 1, 1)
		Expect(err).To(HaveOccurred(), "relative program accepted")
		raw, err := checkConfig("/bin/true", 1000, 1000)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"/bin/true"`))
	})

	It("validates the profile path", func() {
		for _, ok := range []string{"/opt/gvisor/runsc", "/home/u/my runsc/runsc", "/opt/\u00e9/runsc"} {
			Expect(ValidateProfilePath(ok)).To(Succeed(), "%q refused", ok)
		}
		for _, bad := range []string{
			"", "runsc", "./runsc", "/opt/../runsc", "/opt//runsc", "/opt/runsc/",
			"/opt/a\"b/runsc", "/opt/a\\b/runsc", "/opt/a{b}/runsc", "/opt/a[b]/runsc",
			"/opt/a*/runsc", "/opt/a?/runsc", "/opt/a^b/runsc",
			"/opt/a\nb/runsc", "/opt/a\u2028b/runsc", "/opt/a\u00a0b/runsc", "/opt/a\rb/runsc", "/opt/a\x00b/runsc", "/opt/a\tb/runsc", "/opt/a\xffb/runsc",
		} {
			Expect(ValidateProfilePath(bad)).ToNot(Succeed(), "%q accepted", bad)
		}
	})

	It("refuses a non-positive disk", func() {
		for _, d := range []int64{0, -1} {
			_, err := bundleConfig(bundleParams{Rootfs: "/r", Lower: "/l", Upper: "/u", Ctl: "/c", Disk: d})
			Expect(err).To(HaveOccurred(), "disk %d accepted", d)
		}
	})
})
