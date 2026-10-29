package main

import (
	"errors"
	"net"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeCtl struct {
	events  []string
	hideErr error
}

func (f *fakeCtl) Hide() error { f.events = append(f.events, "hide"); return f.hideErr }
func (f *fakeCtl) Listen(name string) (net.Listener, error) {
	f.events = append(f.events, "listen "+name)
	return net.Listen("tcp", "127.0.0.1:0")
}
func (f *fakeCtl) Dial(name string) (net.Conn, error) {
	f.events = append(f.events, "dial "+name)
	return nil, errors.New("no dial in this test")
}

var _ = Describe("serveCtl", func() {
	// The agent socket must exist only after the control folder is hidden.
	It("hides the control folder before it listens", func() {
		f := &fakeCtl{}
		sl, _, err := serveCtl(f, nil)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = sl.Stop() }()
		Expect(strings.Join(f.events, "|")).To(Equal("hide|listen agent.sock"))
	})

	It("does not serve when hide fails", func() {
		f := &fakeCtl{hideErr: errors.New("EPERM")}
		_, _, err := serveCtl(f, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("EPERM"))
		Expect(strings.Join(f.events, "|")).To(Equal("hide"), "the agent listened after a failed Hide")
	})
})

var _ = Describe("retry", func() {
	It("tries again until it works", func() {
		calls := 0
		err := retry(5, time.Millisecond, func() error {
			if calls++; calls < 3 {
				return errors.New("busy")
			}
			return nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(calls).To(Equal(3))
		calls = 0
		err = retry(4, time.Millisecond, func() error { calls++; return errors.New("still busy") })
		Expect(err).To(MatchError("still busy"))
		Expect(calls).To(Equal(4))
	})
})
