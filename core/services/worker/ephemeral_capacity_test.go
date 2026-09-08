// SPDX-License-Identifier: MIT

package worker

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type capacityGatedFileWriter struct {
	file    *os.File
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (w *capacityGatedFileWriter) Write(p []byte) (int, error) {
	w.once.Do(func() {
		close(w.entered)
		<-w.resume
	})
	return w.file.Write(p)
}

type capacityShortWriter struct{}

func (capacityShortWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return 1, nil
}

var _ = Describe("EphemeralCapacityGuard", func() {
	It("accounts existing regular files without following symlinks", func() {
		root := GinkgoT().TempDir()
		outside := filepath.Join(GinkgoT().TempDir(), "outside.bin")
		Expect(os.WriteFile(filepath.Join(root, "existing.bin"), make([]byte, 6), 0o600)).To(Succeed())
		Expect(os.WriteFile(outside, make([]byte, 100), 0o600)).To(Succeed())
		Expect(os.Symlink(outside, filepath.Join(root, "outside-link"))).To(Succeed())

		guard, err := NewEphemeralCapacityGuard([]string{root}, 10, 0)
		Expect(err).NotTo(HaveOccurred())

		err = guard.Reserve(filepath.Join(root, "next.bin"), 5)
		var capacityErr *EphemeralCapacityError
		Expect(errors.As(err, &capacityErr)).To(BeTrue())
		Expect(capacityErr.RequestedBytes).To(Equal(int64(5)))
		Expect(capacityErr.UsageBytes).To(Equal(int64(6)))
		Expect(capacityErr.LimitBytes).To(Equal(int64(10)))
		Expect(capacityErr.AvailableBytes).To(BeNumerically(">", 0))
		Expect(capacityErr.HeadroomBytes).To(BeZero())
	})

	It("serializes competing reservations", func() {
		root := GinkgoT().TempDir()
		guard, err := NewEphemeralCapacityGuard([]string{root}, 1, 0)
		Expect(err).NotTo(HaveOccurred())

		start := make(chan struct{})
		results := make(chan error, 32)
		var wait sync.WaitGroup
		for i := range 32 {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				<-start
				results <- guard.Reserve(filepath.Join(root, string(rune('a'+index))), 1)
			}(i)
		}
		close(start)
		wait.Wait()
		close(results)

		succeeded := 0
		for result := range results {
			if result == nil {
				succeeded++
				continue
			}
			var capacityErr *EphemeralCapacityError
			Expect(errors.As(result, &capacityErr)).To(BeTrue())
		}
		Expect(succeeded).To(Equal(1))
	})

	It("makes only an equal active reservation idempotent", func() {
		root := GinkgoT().TempDir()
		guard, err := NewEphemeralCapacityGuard([]string{root}, 10, 0)
		Expect(err).NotTo(HaveOccurred())
		path := filepath.Join(root, "nested", "payload.bin")

		Expect(guard.Reserve(path, 4)).To(Succeed())
		Expect(guard.Reserve(filepath.Join(root, "nested", ".", "payload.bin"), 4)).To(Succeed())
		err = guard.Reserve(path, 5)
		var conflictErr *EphemeralReservationConflictError
		Expect(errors.As(err, &conflictErr)).To(BeTrue())
		Expect(conflictErr.ActiveBytes).To(Equal(int64(4)))
		Expect(conflictErr.RequestedBytes).To(Equal(int64(5)))
		Expect(guard.Reserve(filepath.Join(root, "other.bin"), 6)).To(Succeed())
		Expect(guard.Release(filepath.Join(root, "nested", ".", "payload.bin"))).To(Succeed())
		Expect(guard.Release(path)).To(Succeed())
		Expect(guard.Reserve(filepath.Join(root, "replacement.bin"), 4)).To(Succeed())
	})

	It("retains committed bytes when the same path starts another reservation", func() {
		root := GinkgoT().TempDir()
		guard, err := NewEphemeralCapacityGuard([]string{root}, 10, 0)
		Expect(err).NotTo(HaveOccurred())
		path := filepath.Join(root, "payload.bin")

		Expect(guard.Reserve(path, 4)).To(Succeed())
		Expect(os.WriteFile(path, make([]byte, 4), 0o600)).To(Succeed())
		Expect(guard.Commit(path)).To(Succeed())
		Expect(guard.HasActiveReservation(path)).To(BeFalse())
		Expect(guard.Reserve(path, 6)).To(Succeed())
		Expect(guard.HasActiveReservation(path)).To(BeTrue())

		err = guard.Reserve(filepath.Join(root, "overflow.bin"), 1)
		var capacityErr *EphemeralCapacityError
		Expect(errors.As(err, &capacityErr)).To(BeTrue())
		Expect(capacityErr.UsageBytes).To(Equal(int64(10)))
	})

	It("retains startup-accounted bytes when the path is reserved", func() {
		root := GinkgoT().TempDir()
		path := filepath.Join(root, "payload.bin")
		Expect(os.WriteFile(path, make([]byte, 4), 0o600)).To(Succeed())
		guard, err := NewEphemeralCapacityGuard([]string{root}, 10, 0)
		Expect(err).NotTo(HaveOccurred())

		Expect(guard.Reserve(path, 6)).To(Succeed())
		err = guard.Reserve(filepath.Join(root, "overflow.bin"), 1)
		var capacityErr *EphemeralCapacityError
		Expect(errors.As(err, &capacityErr)).To(BeTrue())
		Expect(capacityErr.UsageBytes).To(Equal(int64(10)))
	})

	It("commits the regular file's actual size", func() {
		root := GinkgoT().TempDir()
		guard, err := NewEphemeralCapacityGuard([]string{root}, 10, 0)
		Expect(err).NotTo(HaveOccurred())
		path := filepath.Join(root, "payload.bin")

		Expect(guard.Reserve(path, 10)).To(Succeed())
		Expect(os.WriteFile(path, []byte("four"), 0o600)).To(Succeed())
		Expect(guard.Commit(path)).To(Succeed())
		Expect(guard.Commit(filepath.Clean(path))).To(Succeed())
		Expect(guard.Reserve(filepath.Join(root, "six.bin"), 6)).To(Succeed())
	})

	It("preserves configured filesystem headroom", func() {
		root := GinkgoT().TempDir()
		guard, err := NewEphemeralCapacityGuard([]string{root}, 1<<30, 1<<62)
		Expect(err).NotTo(HaveOccurred())

		err = guard.Reserve(filepath.Join(root, "payload.bin"), 1)
		var capacityErr *EphemeralCapacityError
		Expect(errors.As(err, &capacityErr)).To(BeTrue())
		Expect(capacityErr.RequestedBytes).To(Equal(int64(1)))
		Expect(capacityErr.AvailableBytes).To(BeNumerically(">", 0))
		Expect(capacityErr.HeadroomBytes).To(Equal(int64(1 << 62)))
	})

	It("reserves bounded chunks before forwarding unknown-length input", func() {
		root := GinkgoT().TempDir()
		guard, err := NewEphemeralCapacityGuard([]string{root}, ephemeralCapacityWriteChunk+1, 0)
		Expect(err).NotTo(HaveOccurred())
		path := filepath.Join(root, "payload.bin")
		file, err := os.Create(path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(file.Close)
		writer, err := guard.NewWriter(path, file)
		Expect(err).NotTo(HaveOccurred())

		n, err := writer.Write(make([]byte, ephemeralCapacityWriteChunk+2))
		Expect(n).To(Equal(int(ephemeralCapacityWriteChunk)))
		var capacityErr *EphemeralCapacityError
		Expect(errors.As(err, &capacityErr)).To(BeTrue())
		Expect(writer.Close()).To(Succeed())
		info, err := file.Stat()
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Size()).To(Equal(ephemeralCapacityWriteChunk))
	})

	It("waits for an open bounded writer before committing", func() {
		root := GinkgoT().TempDir()
		guard, err := NewEphemeralCapacityGuard([]string{root}, 10, 0)
		Expect(err).NotTo(HaveOccurred())
		path := filepath.Join(root, "payload.bin")
		file, err := os.Create(path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(file.Close)
		gated := &capacityGatedFileWriter{
			file: file, entered: make(chan struct{}), resume: make(chan struct{}),
		}
		DeferCleanup(func() {
			select {
			case <-gated.resume:
			default:
				close(gated.resume)
			}
		})
		writer, err := guard.NewWriter(path, gated)
		Expect(err).NotTo(HaveOccurred())

		writeDone := make(chan error, 1)
		go func() {
			_, writeErr := writer.Write([]byte("1234567"))
			writeDone <- writeErr
		}()
		Eventually(gated.entered).Should(BeClosed())
		commitDone := make(chan error, 1)
		go func() { commitDone <- guard.Commit(path) }()
		Eventually(func() int { return guard.commitWaiterCount(path) }).Should(Equal(1))
		Expect(commitDone).NotTo(Receive())

		close(gated.resume)
		Expect(<-writeDone).To(Succeed())
		Expect(commitDone).NotTo(Receive())
		Expect(writer.Close()).To(Succeed())
		Eventually(commitDone).Should(Receive(Succeed()))

		err = guard.Reserve(filepath.Join(root, "other.bin"), 4)
		var capacityErr *EphemeralCapacityError
		Expect(errors.As(err, &capacityErr)).To(BeTrue())
		Expect(capacityErr.UsageBytes).To(Equal(int64(7)))
	})

	It("does not share pending capacity between concurrent writers", func() {
		root := GinkgoT().TempDir()
		guard, err := NewEphemeralCapacityGuard([]string{root}, 10, 0)
		Expect(err).NotTo(HaveOccurred())
		path := filepath.Join(root, "payload.bin")
		file, err := os.Create(path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(file.Close)
		gated := &capacityGatedFileWriter{
			file: file, entered: make(chan struct{}), resume: make(chan struct{}),
		}
		first, err := guard.NewWriter(path, gated)
		Expect(err).NotTo(HaveOccurred())
		var secondDestination bytes.Buffer
		second, err := guard.NewWriter(path, &secondDestination)
		Expect(err).NotTo(HaveOccurred())

		firstDone := make(chan error, 1)
		go func() {
			_, writeErr := first.Write([]byte("1234567"))
			firstDone <- writeErr
		}()
		Eventually(gated.entered).Should(BeClosed())

		n, err := second.Write([]byte("7654321"))
		Expect(n).To(BeZero())
		var capacityErr *EphemeralCapacityError
		Expect(errors.As(err, &capacityErr)).To(BeTrue())
		Expect(secondDestination.Len()).To(BeZero())

		close(gated.resume)
		Expect(<-firstDone).To(Succeed())
		Expect(first.Close()).To(Succeed())
		Expect(second.Close()).To(Succeed())
	})

	It("rolls back bytes the destination writer does not accept", func() {
		root := GinkgoT().TempDir()
		guard, err := NewEphemeralCapacityGuard([]string{root}, 5, 0)
		Expect(err).NotTo(HaveOccurred())
		writer, err := guard.NewWriter(filepath.Join(root, "payload.bin"), capacityShortWriter{})
		Expect(err).NotTo(HaveOccurred())

		n, err := writer.Write([]byte("123"))
		Expect(n).To(Equal(1))
		Expect(err).To(MatchError(io.ErrShortWrite))
		Expect(writer.Close()).To(Succeed())
		Expect(guard.Reserve(filepath.Join(root, "other.bin"), 4)).To(Succeed())
	})

	It("rejects paths outside roots and through symlinks", func() {
		root := GinkgoT().TempDir()
		outside := GinkgoT().TempDir()
		guard, err := NewEphemeralCapacityGuard([]string{root}, 100, 0)
		Expect(err).NotTo(HaveOccurred())

		Expect(guard.Reserve(filepath.Join(outside, "payload.bin"), 1)).To(
			MatchError(ContainSubstring("outside registered ephemeral roots")),
		)
		Expect(os.Symlink(outside, filepath.Join(root, "escape"))).To(Succeed())
		Expect(guard.Reserve(filepath.Join(root, "escape", "payload.bin"), 1)).To(
			MatchError(ContainSubstring("symlink")),
		)
	})

	It("supports recovery tree accounting without dropping active reservations", func() {
		root := GinkgoT().TempDir()
		guard, err := NewEphemeralCapacityGuard([]string{root}, 10, 0)
		Expect(err).NotTo(HaveOccurred())
		active := filepath.Join(root, "active", "payload.bin")
		stale := filepath.Join(root, "stale", "payload.bin")

		Expect(guard.Reserve(active, 4)).To(Succeed())
		Expect(guard.Account(stale, 3)).To(Succeed())
		Expect(guard.HasActiveReservation(root)).To(BeTrue())
		Expect(guard.ReleaseTree(filepath.Join(root, "stale"))).To(Succeed())
		Expect(guard.Reserve(filepath.Join(root, "replacement.bin"), 6)).To(Succeed())
		Expect(guard.ReleaseTree(root)).To(Succeed())
		Expect(guard.HasActiveReservation(root)).To(BeFalse())
	})
})
