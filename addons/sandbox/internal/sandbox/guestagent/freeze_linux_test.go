package guestagent

import (
	"errors"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Freezer auto-thaw", func() {
	// A failed auto-thaw must re-arm the timer: the guest must not stay frozen.
	It("retries after a failed thaw", func() {
		var reqs []uintptr
		fail := true
		z := &Freezer{Path: GinkgoT().TempDir(), AutoThaw: 50 * time.Millisecond, Ioctl: func(f *os.File, req uintptr) error {
			reqs = append(reqs, req)
			if req == fiThaw && fail {
				fail = false
				return errors.New("thaw refused")
			}
			return nil
		}}
		Expect(z.Freeze()).To(Succeed())
		deadline := time.Now().Add(3 * time.Second)
		for z.Frozen() && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		Expect(z.Frozen()).To(BeFalse(), "still frozen after a failed auto-thaw was retried; ioctls %#x", reqs)
		Expect(reqs).To(Equal([]uintptr{fiFreeze, fiThaw, fiThaw}), "want freeze, failed thaw, thaw")
	})

	// A timer of an older freeze must not thaw a newer freeze.
	It("does not let a stale timer thaw a newer freeze", func() {
		z := &Freezer{Path: GinkgoT().TempDir(), AutoThaw: time.Hour, Ioctl: func(*os.File, uintptr) error { return nil }}
		Expect(z.Freeze()).To(Succeed()) // freeze 1
		old := z.gen
		Expect(z.Thaw()).To(Succeed())
		Expect(z.Freeze()).To(Succeed()) // freeze 2
		z.autoThaw(old)                  // the timer of freeze 1 fires late
		Expect(z.Frozen()).To(BeTrue(), "a stale auto-thaw thawed a newer freeze")
		_ = z.Thaw()
	})
})
