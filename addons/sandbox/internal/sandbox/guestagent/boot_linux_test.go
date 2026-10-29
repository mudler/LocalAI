package guestagent

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type recSys struct {
	calls []string
	fail  string // a call that fails
}

func (r *recSys) rec(s string) error {
	r.calls = append(r.calls, s)
	if r.fail != "" && strings.HasPrefix(s, r.fail) {
		return syscall.EIO
	}
	return nil
}

func (r *recSys) Mount(source, target, fstype string, flags uintptr, data string) error {
	return r.rec(fmt.Sprintf("mount %s %s %s %#x %s", source, target, fstype, flags, data))
}
func (r *recSys) MkdirAll(p string, perm os.FileMode) error { return r.rec("mkdir " + p) }
func (r *recSys) Chown(p string, uid, gid int) error {
	return r.rec(fmt.Sprintf("chown %s %d:%d", p, uid, gid))
}
func (r *recSys) Remove(p string) error          { return r.rec("rm " + p) }
func (r *recSys) Symlink(target, p string) error { return r.rec("ln -s " + target + " " + p) }
func (r *recSys) LoopbackUp() error              { return r.rec("lo up") }
func (r *recSys) Glob(pattern string) ([]string, error) {
	_ = r.rec("glob " + pattern)
	switch pattern {
	case "/dev/hvc*":
		return []string{"/dev/hvc0", "/dev/hvc1"}, nil
	case "/dev/tty?*":
		return []string{"/dev/tty0", "/dev/ttyS0"}, nil
	}
	return nil, nil
}

var _ = Describe("Boot", func() {
	It("lays out the guest", func() {
		r := &recSys{}
		Expect(Boot(r, BootConfig{Uid: 1000, Gid: 1000})).To(Succeed())
		want := []string{
			"mount proc /proc proc 0xe ",
			"mount /proc/sys /proc/sys  0x1000 ",
			"mount /proc/sys /proc/sys  0x1021 ",
			"mount sysfs /sys sysfs 0xf ",
			"mount devtmpfs /dev devtmpfs 0x2 mode=0755",
			"mkdir /dev/pts",
			"mount devpts /dev/pts devpts 0xa ptmxmode=0666,mode=0620,gid=5",
			"ln -s /proc/self/fd /dev/fd",
			"ln -s /proc/self/fd/0 /dev/stdin",
			"ln -s /proc/self/fd/1 /dev/stdout",
			"ln -s /proc/self/fd/2 /dev/stderr",
			"mount tmpfs /tmp tmpfs 0x6 mode=1777",
			"mount tmpfs /run tmpfs 0x6 mode=0755",
			"mkdir /run/upper",
			"mount /dev/vdb /run/upper ext4 0x2 ",
			"mkdir /run/upper/data",
			"mkdir /run/upper/work",
			"mkdir /run/upper/home/data",
			"mkdir /run/upper/home/work",
			"chown /run/upper/data 1000:1000",
			"chown /run/upper/home/data 1000:1000",
			"mkdir /run/lower",
			"mount lower /run/lower virtiofs 0x7 ",
			"mount overlay /workspace overlay 0x6 lowerdir=/run/lower,upperdir=/run/upper/data,workdir=/run/upper/work,redirect_dir=off,metacopy=off,index=off",
			"mount overlay /home/agent overlay 0x6 lowerdir=/home/agent,upperdir=/run/upper/home/data,workdir=/run/upper/home/work,redirect_dir=off,metacopy=off,index=off",
			"rm /dev/vda",
			"rm /dev/vdb",
			"rm /dev/console",
			"rm /dev/kmsg",
			"glob /dev/hvc*",
			"rm /dev/hvc0",
			"rm /dev/hvc1",
			"glob /dev/tty?*",
			"rm /dev/tty0",
			"rm /dev/ttyS0",
			"lo up",
		}
		Expect(strings.Join(r.calls, "\n")).To(Equal(strings.Join(want, "\n")))
	})

	It("supports binds and a plain overlay", func() {
		r := &recSys{}
		cfg := BootConfig{Uid: 1000, Gid: 1000, UpperBind: "/mnt/upper", LowerBind: "/mnt/lower", PlainOverlay: true}
		Expect(Boot(r, cfg)).To(Succeed())
		got := strings.Join(r.calls, "\n")
		for _, want := range []string{
			"mount /mnt/upper /run/upper  0x5000 ",
			"mount /mnt/lower /run/lower  0x5000 ",
			"mount  /run/lower  0x5021 ",
			"mount overlay /workspace overlay 0x6 lowerdir=/run/lower,upperdir=/run/upper/data,workdir=/run/upper/work\n",
			"mount overlay /home/agent overlay 0x6 lowerdir=/home/agent,upperdir=/run/upper/home/data,workdir=/run/upper/home/work\n",
		} {
			Expect(got + "\n").To(ContainSubstring(want))
		}
		for _, not := range []string{"ext4", "virtiofs", "redirect_dir", "metacopy", "index="} {
			Expect(got).ToNot(ContainSubstring(not))
		}
	})

	It("stops at the first error", func() {
		r := &recSys{fail: "mount lower"}
		Expect(Boot(r, BootConfig{Uid: 1000, Gid: 1000})).ToNot(Succeed(), "boot ignored a failed mount")
		Expect(r.calls[len(r.calls)-1]).To(HavePrefix("mount lower"), "boot went on after the failure")
	})
})

var _ = Describe("ParsePorts", func() {
	It("reads the ports option of the command line", func() {
		got := ParsePorts("console=hvc0 root=/dev/vda sandbox.ports=3128,7000,x,0,1024,70000 quiet")
		Expect(fmt.Sprint(got)).To(Equal("[3128 7000]"))
		Expect(ParsePorts("console=hvc0")).To(BeNil(), "ports without the option")
	})
})

var _ = Describe("ParsePortList", func() {
	It("keeps the valid ports", func() {
		Expect(fmt.Sprint(ParsePortList("3128,7000,x,0,1024,70000"))).To(Equal("[3128 7000]"))
		Expect(ParsePortList("")).To(BeNil())
	})
})

var _ = Describe("Freezer", func() {
	It("thaws on its own", func() {
		var reqs []uintptr
		z := &Freezer{Path: GinkgoT().TempDir(), AutoThaw: 100 * time.Millisecond, Ioctl: func(f *os.File, req uintptr) error {
			reqs = append(reqs, req)
			return nil
		}}
		Expect(z.Freeze()).To(Succeed())
		Expect(z.Freeze()).To(MatchError(ErrFrozen))
		Expect(z.Frozen()).To(BeTrue())
		time.Sleep(300 * time.Millisecond)
		z.mu.Lock()
		frozen := z.frozen
		got := fmt.Sprintf("%#x", reqs)
		z.mu.Unlock()
		Expect(frozen).To(BeFalse())
		Expect(got).To(Equal("[0xc0045877 0xc0045878]"))
		Expect(z.Thaw()).To(Succeed(), "thaw when thawed")
	})
})
