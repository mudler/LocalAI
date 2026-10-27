package guestagent

import (
	"encoding/binary"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// Sys is the part of the kernel that Boot uses. RealSys calls the kernel;
// tests record the calls.
type Sys interface {
	Mount(source, target, fstype string, flags uintptr, data string) error
	MkdirAll(path string, perm os.FileMode) error
	Chown(path string, uid, gid int) error
	Remove(path string) error
	Symlink(target, path string) error
	LoopbackUp() error
	Glob(pattern string) ([]string, error)
}

// BootConfig is the guest layout.
type BootConfig struct {
	Uid, Gid int    // owner of the writable folders
	Upper    string // upper disk, default /dev/vdb
	LowerTag string // virtiofs tag of the lower tree, default "lower"
	Home     string // home of the agent user, default /home/agent
	// RemoveDevices lists device nodes to delete after the mounts, so a
	// process with CAP_DAC_OVERRIDE cannot write the raw disks. Default
	// /dev/vda and the upper disk.
	RemoveDevices []string
	// RemoveGlobs lists patterns of device nodes to delete the same way.
	// Default: the console, the kernel log, every hvc and every tty node
	// except /dev/tty. A process with CAP_DAC_OVERRIDE could otherwise
	// flood the console log of the host or print a fake panic line. The
	// agent keeps writing to its own open console descriptors.
	RemoveGlobs []string
	// UpperBind and LowerBind replace the upper disk and the virtiofs lower
	// tree with folders that the host binds into the sandbox (gVisor): the
	// folder UpperBind is bound at /run/upper, and LowerBind is bound
	// read-only at /run/lower. Both empty is the VM layout.
	UpperBind, LowerBind string
	// PlainOverlay leaves redirect_dir, metacopy and index out of the overlay
	// options. gVisor refuses all three.
	PlainOverlay bool
}

// Overlay options: no redirect, metacopy or index, so the upper layer has the
// format that workspace.CommitUpper reads (a rename of a folder is a copy).
const overlayOpts = ",redirect_dir=off,metacopy=off,index=off"

// Boot mounts the guest file systems:
//
//	/proc (with /proc/sys read-only), /sys (read-only), /dev, /dev/pts, /tmp, /run
//	/run/upper   the upper disk (ext4)
//	/run/lower   the lower tree (virtiofs, read-only)
//	/workspace   overlay of /run/lower and /run/upper/data
//	/home/agent  overlay of the image's home and /run/upper/home/data
//
// and brings up the loopback interface.
func Boot(sys Sys, cfg BootConfig) error {
	overlay := overlayOpts
	if cfg.PlainOverlay {
		overlay = ""
	}
	if cfg.Upper == "" {
		cfg.Upper = "/dev/vdb"
	}
	if cfg.LowerTag == "" {
		cfg.LowerTag = "lower"
	}
	if cfg.Home == "" {
		cfg.Home = "/home/agent"
	}
	if cfg.RemoveDevices == nil {
		cfg.RemoveDevices = []string{"/dev/vda", cfg.Upper, "/dev/console", "/dev/kmsg"}
	}
	if cfg.RemoveGlobs == nil {
		// /dev/tty is the controlling terminal of the caller. Without one it
		// cannot be opened, so it never reaches the console.
		cfg.RemoveGlobs = []string{"/dev/hvc*", "/dev/tty?*"}
	}
	const nosuid, nodev, noexec = syscall.MS_NOSUID, syscall.MS_NODEV, syscall.MS_NOEXEC
	steps := []func() error{
		func() error { return sys.Mount("proc", "/proc", "proc", nosuid|nodev|noexec, "") },
		// /proc/sys is read-only for every process, including one with
		// CAP_DAC_OVERRIDE: writing core_pattern would run a helper as root.
		func() error { return sys.Mount("/proc/sys", "/proc/sys", "", syscall.MS_BIND, "") },
		func() error {
			return sys.Mount("/proc/sys", "/proc/sys", "", syscall.MS_RDONLY|syscall.MS_BIND|syscall.MS_REMOUNT, "")
		},
		func() error {
			return sys.Mount("sysfs", "/sys", "sysfs", syscall.MS_RDONLY|nosuid|nodev|noexec, "")
		},
		func() error {
			// The kernel mounts devtmpfs itself when it has DEVTMPFS_MOUNT.
			if err := sys.Mount("devtmpfs", "/dev", "devtmpfs", nosuid, "mode=0755"); err != nil && !errors.Is(err, syscall.EBUSY) {
				return err
			}
			return nil
		},
		func() error { return sys.MkdirAll("/dev/pts", 0o755) },
		func() error {
			return sys.Mount("devpts", "/dev/pts", "devpts", nosuid|noexec, "ptmxmode=0666,mode=0620,gid=5")
		},
	}
	// devtmpfs has no /dev/fd: bash process substitution and /dev/stdin need it.
	for _, l := range [][2]string{{"/proc/self/fd", "/dev/fd"}, {"/proc/self/fd/0", "/dev/stdin"}, {"/proc/self/fd/1", "/dev/stdout"}, {"/proc/self/fd/2", "/dev/stderr"}} {
		steps = append(steps, func() error {
			if err := sys.Symlink(l[0], l[1]); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			return nil
		})
	}
	steps = append(steps,
		func() error { return sys.Mount("tmpfs", "/tmp", "tmpfs", nosuid|nodev, "mode=1777") },
		func() error { return sys.Mount("tmpfs", "/run", "tmpfs", nosuid|nodev, "mode=0755") },
		func() error { return sys.MkdirAll("/run/upper", 0o755) },
		func() error {
			if cfg.UpperBind != "" {
				return sys.Mount(cfg.UpperBind, "/run/upper", "", syscall.MS_BIND|syscall.MS_REC, "")
			}
			return sys.Mount(cfg.Upper, "/run/upper", "ext4", nosuid, "")
		},
	)
	for _, d := range []string{"/run/upper/data", "/run/upper/work", "/run/upper/home/data", "/run/upper/home/work"} {
		steps = append(steps, func() error { return sys.MkdirAll(d, 0o755) })
	}
	steps = append(steps,
		func() error { return sys.Chown("/run/upper/data", cfg.Uid, cfg.Gid) },
		func() error { return sys.Chown("/run/upper/home/data", cfg.Uid, cfg.Gid) },
		func() error { return sys.MkdirAll("/run/lower", 0o755) },
		func() error {
			if cfg.LowerBind != "" {
				if err := sys.Mount(cfg.LowerBind, "/run/lower", "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
					return err
				}
				// MS_REC: a mount below the lower folder becomes read-only too.
				return sys.Mount("", "/run/lower", "", syscall.MS_BIND|syscall.MS_REC|syscall.MS_REMOUNT|syscall.MS_RDONLY, "")
			}
			return sys.Mount(cfg.LowerTag, "/run/lower", "virtiofs", syscall.MS_RDONLY|nosuid|nodev, "")
		},
		func() error {
			return sys.Mount("overlay", "/workspace", "overlay", nosuid|nodev,
				"lowerdir=/run/lower,upperdir=/run/upper/data,workdir=/run/upper/work"+overlay)
		},
		func() error {
			return sys.Mount("overlay", cfg.Home, "overlay", nosuid|nodev,
				"lowerdir="+cfg.Home+",upperdir=/run/upper/home/data,workdir=/run/upper/home/work"+overlay)
		},
	)
	for _, d := range cfg.RemoveDevices {
		steps = append(steps, func() error {
			if err := sys.Remove(d); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		})
	}
	for _, g := range cfg.RemoveGlobs {
		steps = append(steps, func() error {
			names, err := sys.Glob(g)
			if err != nil {
				return err
			}
			for _, d := range names {
				if err := sys.Remove(d); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			return nil
		})
	}
	steps = append(steps, sys.LoopbackUp)
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// RealSys is the Sys of the running kernel.
type RealSys struct{}

func (RealSys) Mount(source, target, fstype string, flags uintptr, data string) error {
	if err := syscall.Mount(source, target, fstype, flags, data); err != nil {
		return &os.PathError{Op: "mount " + fstype, Path: target, Err: err}
	}
	return nil
}
func (RealSys) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func (RealSys) Chown(path string, uid, gid int) error        { return os.Chown(path, uid, gid) }
func (RealSys) Remove(path string) error                     { return os.Remove(path) }
func (RealSys) Symlink(target, path string) error            { return os.Symlink(target, path) }
func (RealSys) Glob(pattern string) ([]string, error)        { return filepath.Glob(pattern) }

// LoopbackUp sets the IFF_UP flag of the "lo" interface. A kernel that does
// not support the ioctls (gVisor answers ENOTTY) is fine when "lo" is
// already up.
func (RealSys) LoopbackUp() error {
	return loopbackFallback(loopbackUp(), loopbackIsUp)
}

// loopbackFallback returns nil when the ioctl failed but "lo" is up.
func loopbackFallback(err error, up func() bool) error {
	if err != nil && up() {
		return nil
	}
	return err
}

// loopbackIsUp reports whether "lo" has the IFF_UP flag.
func loopbackIsUp() bool {
	ifc, err := net.InterfaceByName("lo")
	return err == nil && ifc.Flags&net.FlagUp != 0
}

func loopbackUp() error {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	var ifr [40]byte // struct ifreq: name[16], then a union; flags is a short
	copy(ifr[:], "lo")
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.SIOCGIFFLAGS, uintptr(unsafe.Pointer(&ifr[0]))); e != 0 {
		return e
	}
	flags := binary.NativeEndian.Uint16(ifr[16:])
	binary.NativeEndian.PutUint16(ifr[16:], flags|syscall.IFF_UP)
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.SIOCSIFFLAGS, uintptr(unsafe.Pointer(&ifr[0]))); e != 0 {
		return e
	}
	return nil
}
