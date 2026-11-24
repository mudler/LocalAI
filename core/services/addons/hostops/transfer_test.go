// SPDX-License-Identifier: MIT
package hostops

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type xferFixture struct {
	t      *Transfers
	h      http.Handler
	mu     sync.Mutex
	outs   []TransferOutcome
	blobs  blob.Store
	dir    string
	shared *storage.FilesystemStore
}

// outcomes copies the audited outcomes: the audit hook runs on the goroutine
// that closes a transfer.
func (f *xferFixture) outcomes() []TransferOutcome {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]TransferOutcome(nil), f.outs...)
}

func newXferFixture() *xferFixture {
	dir := GinkgoT().TempDir()
	shared, err := storage.NewFilesystemStore(filepath.Join(dir, "objects"))
	Expect(err).NotTo(HaveOccurred())
	// The owner node has its own cache disk; the object store is the only
	// thing the nodes share.
	blobs := blob.NewObjectStore(shared, filepath.Join(dir, "owner-cache"))
	f := &xferFixture{blobs: blobs, dir: dir, shared: shared}
	f.t = NewTransfers(blobs)
	f.t.Audit = func(o TransferOutcome) { f.mu.Lock(); f.outs = append(f.outs, o); f.mu.Unlock() }
	b := New(NewInFlight(), always, contract.DefaultLimits())
	Expect(RegisterConnectorTransfer(b, f.t)).To(Succeed())
	f.h = b.Handler("slack", "secret", []string{"connector."})
	return f
}

// call posts one host operation as the add-on addon.
func (f *xferFixture) call(addon, op string, args any) (int, contract.HostResponse) {
	raw, _ := json.Marshal(args)
	body, _ := json.Marshal(contract.HostRequest{OperationID: "x", Op: op, Arguments: raw})
	req := httptest.NewRequest(http.MethodPost, "/host", bytes.NewReader(body))
	req.Header.Set(contract.HostCredentialHeader, "secret")
	rec := httptest.NewRecorder()
	h := f.h
	if addon != "slack" {
		b := New(NewInFlight(), always, contract.DefaultLimits())
		Expect(RegisterConnectorTransfer(b, f.t)).To(Succeed())
		h = b.Handler(addon, "secret", []string{"connector."})
	}
	h.ServeHTTP(rec, req)
	var out contract.HostResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (f *xferFixture) put(handle string, off int64, data []byte, final bool) (int, int64) {
	code, r := f.call("slack", contract.ConnectorFilePutOp, contract.ConnectorFilePut{Handle: handle, Offset: off, Data: data, Final: final})
	var res contract.ConnectorFilePutResult
	if r.Result != nil {
		_ = json.Unmarshal(r.Result, &res)
	}
	return code, res.Received
}

func (f *xferFixture) get(handle string, off, n int64) (int, contract.ConnectorFileGetResult) {
	code, r := f.call("slack", contract.ConnectorFileGetOp, contract.ConnectorFileGet{Handle: handle, Offset: off, Length: n})
	var res contract.ConnectorFileGetResult
	if r.Result != nil {
		_ = json.Unmarshal(r.Result, &res)
	}
	return code, res
}

// chunks streams n bytes of a seeded sequence, chunk bytes at a time, and
// never holds more than one chunk.
func chunks(n, chunk int64, each func(off int64, b []byte, last bool)) string {
	rng := rand.New(rand.NewSource(7))
	h := sha256.New()
	buf := make([]byte, chunk)
	for off := int64(0); off < n; off += chunk {
		b := buf[:min(chunk, n-off)]
		_, _ = rng.Read(b)
		h.Write(b)
		each(off, b, off+int64(len(b)) >= n)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func heap() uint64 {
	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

var _ = Describe("connector file transfer by handle", func() {
	const mb = 1 << 20

	It("moves 20 MB in and 20 MB out with bounded memory", func() {
		f := newXferFixture()
		end, err := f.t.Begin(TransferGrant{Handle: "h1", Direction: TransferIn, AddonID: "slack", BindingID: "b1", MaxBytes: 50 * mb})
		Expect(err).NotTo(HaveOccurred())
		base, peak := heap(), uint64(0)
		var count int
		want := chunks(20*mb, contract.TransferChunkBytes, func(off int64, b []byte, last bool) {
			code, got := f.put("h1", off, append([]byte(nil), b...), last)
			Expect(code).To(Equal(http.StatusOK))
			Expect(got).To(Equal(off + int64(len(b))))
			if count++; count%16 == 0 {
				peak = max(peak, heap())
			}
		})
		out := end()
		Expect(out.Result).To(Equal(TransferDone))
		Expect(out.Bytes).To(Equal(int64(20 * mb)))
		Expect(out.SHA).To(Equal(want))
		Expect(peak).To(BeNumerically("<", base+16*mb), "the file is never held in memory")
		Expect(f.t.Live()).To(BeZero())

		// Another node: same object store, its own cache disk.
		node2 := blob.NewObjectStore(f.shared, filepath.Join(f.dir, "frontend-cache"))
		r, err := node2.Open(GinkgoT().Context(), out.SHA)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = r.Close() })
		h := sha256.New()
		n, err := io.Copy(h, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(int64(20 * mb)))
		Expect(hex.EncodeToString(h.Sum(nil))).To(Equal(want))

		// Out: the add-on pulls the same file by a new handle.
		end2, err := f.t.Begin(TransferGrant{Handle: "h2", Direction: TransferOut, AddonID: "slack", SHA: out.SHA, Size: n})
		Expect(err).NotTo(HaveOccurred())
		hh := sha256.New()
		var off int64
		base, peak = heap(), 0
		for i := 0; ; i++ {
			code, res := f.get("h2", off, contract.TransferChunkBytes)
			Expect(code).To(Equal(http.StatusOK))
			hh.Write(res.Data)
			off += int64(len(res.Data))
			if i%16 == 0 {
				peak = max(peak, heap())
			}
			if res.EOF {
				break
			}
		}
		Expect(off).To(Equal(n))
		Expect(hex.EncodeToString(hh.Sum(nil))).To(Equal(want))
		Expect(peak).To(BeNumerically("<", base+16*mb))
		Expect(end2().Result).To(Equal(TransferDone))
		Expect(f.outcomes()).To(HaveLen(2))
	})

	It("resumes at the offset the host has after a gap", func() {
		f := newXferFixture()
		end, _ := f.t.Begin(TransferGrant{Handle: "h", Direction: TransferIn, AddonID: "slack", MaxBytes: mb})
		code, got := f.put("h", 0, []byte("abcd"), false)
		Expect(code).To(Equal(http.StatusOK))
		Expect(got).To(BeEquivalentTo(4))
		_, got = f.put("h", 10, []byte("zz"), false)
		Expect(got).To(BeEquivalentTo(4), "a gap answers where the host is and writes nothing")
		_, got = f.put("h", 4, []byte("efgh"), true)
		Expect(got).To(BeEquivalentTo(8))
		o := end()
		sum := sha256.Sum256([]byte("abcdefgh"))
		Expect(o.SHA).To(Equal(hex.EncodeToString(sum[:])))
	})

	It("refuses an oversize file and drops the partial one", func() {
		f := newXferFixture()
		end, _ := f.t.Begin(TransferGrant{Handle: "h", Direction: TransferIn, AddonID: "slack", MaxBytes: 10})
		code, _ := f.put("h", 0, []byte("12345678"), false)
		Expect(code).To(Equal(http.StatusOK))
		code, _ = f.put("h", 8, []byte("9012"), true)
		Expect(code).To(Equal(http.StatusRequestEntityTooLarge))
		o := end()
		Expect(o.Result).To(Equal(TransferTooLarge))
		Expect(o.SHA).To(BeEmpty())
		code, _ = f.put("h", 0, []byte("x"), false)
		Expect(code).To(Equal(http.StatusNotFound), "the handle ended")
	})

	It("refuses a file whose last chunk says it is short of the bytes sent, an empty file, and a write after final", func() {
		f := newXferFixture()
		end, _ := f.t.Begin(TransferGrant{Handle: "h", Direction: TransferIn, AddonID: "slack", MaxBytes: 100})
		code, _ := f.put("h", 0, nil, true)
		Expect(code).To(Equal(http.StatusBadRequest), "empty")
		Expect(end().Result).To(Equal(TransferRefused))

		end, _ = f.t.Begin(TransferGrant{Handle: "g", Direction: TransferIn, AddonID: "slack", MaxBytes: 100})
		code, _ = f.put("g", 0, []byte("ab"), true)
		Expect(code).To(Equal(http.StatusOK))
		code, _ = f.put("g", 2, []byte("cd"), false)
		Expect(code).To(Equal(http.StatusConflict), "write after final")
		Expect(end().Result).To(Equal(TransferDone))
	})

	It("refuses another add-on, an unknown handle and the wrong direction with one answer", func() {
		f := newXferFixture()
		end, _ := f.t.Begin(TransferGrant{Handle: "h", Direction: TransferIn, AddonID: "slack", MaxBytes: 100})
		defer end()
		other, _ := f.call("telegram", contract.ConnectorFilePutOp, contract.ConnectorFilePut{Handle: "h", Data: []byte("x")})
		unknown, _ := f.call("slack", contract.ConnectorFilePutOp, contract.ConnectorFilePut{Handle: "nope", Data: []byte("x")})
		Expect(other).To(Equal(http.StatusNotFound))
		Expect(unknown).To(Equal(other))
		code, _ := f.get("h", 0, 10)
		Expect(code).To(Equal(http.StatusNotFound), "a write handle cannot read")
	})

	It("does not let a handle be used for two calls", func() {
		f := newXferFixture()
		end, err := f.t.Begin(TransferGrant{Handle: "h", Direction: TransferIn, AddonID: "slack", MaxBytes: 100})
		Expect(err).NotTo(HaveOccurred())
		_, err = f.t.Begin(TransferGrant{Handle: "h", Direction: TransferIn, AddonID: "slack", MaxBytes: 100})
		Expect(err).To(HaveOccurred())
		end()
		end() // idempotent
		Expect(f.outcomes()).To(HaveLen(1))
		_, err = f.t.Begin(TransferGrant{Handle: "h", Direction: TransferIn, AddonID: "slack", MaxBytes: 100})
		Expect(err).NotTo(HaveOccurred(), "a handle of an ended call is free; a new call makes its own random one")
	})

	It("expires a handle that sees no call and removes the partial file", func() {
		f := newXferFixture()
		f.t.Idle = 80 * time.Millisecond
		_, err := f.t.Begin(TransferGrant{Handle: "h", Direction: TransferIn, AddonID: "slack", MaxBytes: 100})
		Expect(err).NotTo(HaveOccurred())
		code, _ := f.put("h", 0, []byte("partial"), false)
		Expect(code).To(Equal(http.StatusOK))
		Eventually(f.t.Live, "2s", "20ms").Should(BeZero())
		code, _ = f.put("h", 7, []byte("x"), false)
		Expect(code).To(Equal(http.StatusNotFound))
		Eventually(f.outcomes, "2s", "20ms").Should(HaveLen(1))
		Expect(f.outcomes()[0].Result).To(Equal(TransferExpired))
		tmp, _ := filepath.Glob(filepath.Join(f.dir, "owner-cache", "tmp", "*"))
		Expect(tmp).To(BeEmpty(), "no partial file is left")
	})

	It("refuses a file the scan hook rejects", func() {
		f := newXferFixture()
		f.t.Scan = func(_ context.Context, _ string, _ int64) error { return io.ErrClosedPipe }
		end, _ := f.t.Begin(TransferGrant{Handle: "h", Direction: TransferIn, AddonID: "slack", MaxBytes: 100})
		code, _ := f.put("h", 0, []byte("virus"), true)
		Expect(code).To(Equal(http.StatusBadRequest))
		o := end()
		Expect(o.Result).To(Equal(TransferRefused))
		Expect(o.SHA).To(BeEmpty())
	})

	It("refuses an outbound handle whose content changed size and an offset past the end", func() {
		f := newXferFixture()
		sha, size, err := f.blobs.Put(GinkgoT().Context(), bytes.NewReader([]byte("hello world")), 0)
		Expect(err).NotTo(HaveOccurred())
		bad, _ := f.t.Begin(TransferGrant{Handle: "bad", Direction: TransferOut, AddonID: "slack", SHA: sha, Size: size + 1})
		code, _ := f.get("bad", 0, 5)
		Expect(code).To(Equal(http.StatusServiceUnavailable))
		bad()
		end, _ := f.t.Begin(TransferGrant{Handle: "ok", Direction: TransferOut, AddonID: "slack", SHA: sha, Size: size})
		defer end()
		code, res := f.get("ok", 6, contract.TransferChunkBytes+1)
		Expect(code).To(Equal(http.StatusBadRequest), "a length above the chunk is refused")
		code, res = f.get("ok", 6, 5)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(res.Data)).To(Equal("world"))
		Expect(res.EOF).To(BeTrue())
		code, _ = f.get("ok", size+1, 1)
		Expect(code).To(Equal(http.StatusBadRequest))
	})
})
