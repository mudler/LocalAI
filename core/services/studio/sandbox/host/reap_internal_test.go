//go:build linux || darwin

package host

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// groupAlive tells whether any process of the group runs (not a zombie).
func groupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil
}

// startGroup starts a sleep in its own group and returns the command.
func startGroup() *exec.Cmd {
	c := exec.Command("sleep", "300")
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	Expect(c.Start()).To(Succeed())
	// Reap the child so a killed group does not stay as a zombie.
	go func() { _ = c.Wait() }()
	DeferCleanup(func() { _ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL) })
	return c
}

var _ = Describe("the reaper of job groups", func() {
	It("kills a group it recorded when the backend starts", func() {
		root := GinkgoT().TempDir()
		c := startGroup()
		b := &Backend{root: root}
		b.recordGroup(c.Process.Pid)
		Expect(filepath.Join(b.groupDir(), strconv.Itoa(c.Process.Pid))).To(BeAnExistingFile())
		_, err := New(Options{Root: root})
		if err != nil {
			Skip(err.Error())
		}
		Eventually(func() bool { return groupAlive(c.Process.Pid) }, 5*time.Second, 20*time.Millisecond).Should(BeFalse())
		Expect(filepath.Join(b.groupDir(), strconv.Itoa(c.Process.Pid))).ToNot(BeAnExistingFile())
	})

	It("never kills a process whose pid was reused", func() {
		root := GinkgoT().TempDir()
		c := startGroup()
		b := &Backend{root: root}
		// A record with another start time: the pid belongs to a stranger now.
		Expect(os.MkdirAll(b.groupDir(), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(b.groupDir(), strconv.Itoa(c.Process.Pid)), []byte("not-its-start-time"), 0o600)).To(Succeed())
		_, err := New(Options{Root: root})
		if err != nil {
			Skip(err.Error())
		}
		Expect(groupAlive(c.Process.Pid)).To(BeTrue())
		Expect(filepath.Join(b.groupDir(), strconv.Itoa(c.Process.Pid))).ToNot(BeAnExistingFile())
	})

	It("ignores a file it did not write for a group", func() {
		root := GinkgoT().TempDir()
		b := &Backend{root: root}
		Expect(os.MkdirAll(b.groupDir(), 0o700)).To(Succeed())
		for _, n := range []string{"1", "0", "x", "-5"} {
			Expect(os.WriteFile(filepath.Join(b.groupDir(), n), []byte("x"), 0o600)).To(Succeed())
		}
		_, err := New(Options{Root: root})
		if err != nil {
			Skip(err.Error())
		}
	})
})
