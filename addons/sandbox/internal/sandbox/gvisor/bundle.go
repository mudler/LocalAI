package gvisor

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Guest paths of the three folders the host binds into the sandbox, and the
// agent inside the image.
const (
	guestLower = "/mnt/lower"
	guestUpper = "/mnt/upper"
	guestCtl   = "/mnt/ctl"
	guestAgent = "/sbin/sandbox-agent"

	// agentUID is the user of the agent and of every command. The one id map
	// takes it to the host user: the host has no newuidmap, so a wider map is
	// not possible.
	agentUID = 1000
)

// initCaps are the capabilities of the guest agent, which is the init process
// and not root. They are ambient so that "mount" works; the agent clears the
// ambient set after boot, and commands get only CAP_DAC_OVERRIDE and
// CAP_FOWNER.
var initCaps = []string{
	"CAP_SYS_ADMIN", "CAP_MKNOD", "CAP_DAC_OVERRIDE", "CAP_CHOWN", "CAP_FOWNER", "CAP_FSETID", "CAP_SETGID",
}

// bundleParams is what the OCI config of one sandbox depends on.
type bundleParams struct {
	Rootfs           string // unpacked image, mounted read-only
	Lower, Upper     string // host folders
	Ctl              string // host folder with the sockets
	HostUID, HostGID int
	Ports            []int
	Disk             int64 // RLIMIT_FSIZE of every process
}

type m = map[string]any

// bundleConfig returns the config.json of a sandbox bundle: user namespace
// with one id map (guest 1000 to the host user), a read-only root, the three
// binds, no network, the agent as init with its capabilities as ambient.
func bundleConfig(p bundleParams) ([]byte, error) {
	if p.Disk <= 0 {
		return nil, fmt.Errorf("gvisor: disk limit %d must be positive", p.Disk)
	}
	caps := m{"bounding": initCaps, "effective": initCaps, "permitted": initCaps, "inheritable": initCaps, "ambient": initCaps}
	ports := make([]string, len(p.Ports))
	for i, n := range p.Ports {
		ports[i] = strconv.Itoa(n)
	}
	cfg := m{
		"ociVersion": "1.0.2",
		"process": m{
			"terminal": false,
			"user":     m{"uid": agentUID, "gid": agentUID},
			"args":     []string{guestAgent},
			"env": []string{
				"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
				"HOME=/home/agent",
				"SANDBOX_AGENT_CTL=" + guestCtl,
				"SANDBOX_AGENT_PORTS=" + strings.Join(ports, ","),
			},
			"cwd":             "/",
			"capabilities":    caps,
			"noNewPrivileges": false,
			"rlimits": []m{
				{"type": "RLIMIT_FSIZE", "hard": uint64(p.Disk), "soft": uint64(p.Disk)},
			},
		},
		"root":     m{"path": p.Rootfs, "readonly": true},
		"hostname": "sandbox",
		"mounts": []m{
			{"destination": "/proc", "type": "proc", "source": "proc"},
			{"destination": "/dev", "type": "tmpfs", "source": "tmpfs", "options": []string{"nosuid", "mode=755"}},
			{"destination": "/dev/pts", "type": "devpts", "source": "devpts", "options": []string{"nosuid", "noexec", "newinstance", "ptmxmode=0666", "mode=0620", "gid=5"}},
			{"destination": "/sys", "type": "sysfs", "source": "sysfs", "options": []string{"nosuid", "noexec", "nodev", "ro"}},
			{"destination": guestLower, "type": "bind", "source": p.Lower, "options": []string{"rbind", "ro"}},
			{"destination": guestUpper, "type": "bind", "source": p.Upper, "options": []string{"rbind", "rw"}},
			{"destination": guestCtl, "type": "bind", "source": p.Ctl, "options": []string{"rbind", "rw"}},
		},
		"linux": m{
			"namespaces":  []m{{"type": "pid"}, {"type": "ipc"}, {"type": "uts"}, {"type": "mount"}, {"type": "network"}, {"type": "user"}},
			"uidMappings": []m{{"containerID": agentUID, "hostID": p.HostUID, "size": 1}},
			"gidMappings": []m{{"containerID": agentUID, "hostID": p.HostGID, "size": 1}},
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// checkConfig is the config.json of the sandbox that New starts to find out
// whether runsc may make a user namespace: it runs the program at prog
// (found on the host) on the root of the host.
func checkConfig(prog string, uid, gid int) ([]byte, error) {
	if prog == "" || !strings.HasPrefix(prog, "/") {
		return nil, fmt.Errorf("gvisor: check program %q is not an absolute path", prog)
	}
	cfg := m{
		"ociVersion": "1.0.2",
		"process": m{
			"terminal": false, "user": m{"uid": uid, "gid": gid},
			"args": []string{prog}, "env": []string{"PATH=/usr/bin:/bin"}, "cwd": "/",
			"capabilities": m{"bounding": []string{}, "effective": []string{}, "permitted": []string{}, "inheritable": []string{}, "ambient": []string{}},
		},
		"root":     m{"path": "/", "readonly": true},
		"hostname": "check",
		"mounts":   []m{{"destination": "/proc", "type": "proc", "source": "proc"}},
		"linux": m{
			"namespaces":  []m{{"type": "pid"}, {"type": "mount"}, {"type": "network"}, {"type": "user"}},
			"uidMappings": []m{{"containerID": uid, "hostID": uid, "size": 1}},
			"gidMappings": []m{{"containerID": gid, "hostID": gid, "size": 1}},
		},
	}
	return json.Marshal(cfg)
}
