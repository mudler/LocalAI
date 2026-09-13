//go:build linux

// SPDX-License-Identifier: MIT
package xsysinfo

import (
	"os"
	"path/filepath"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ProcessVRAM", func() {
	var root string
	write := func(path, contents string) {
		Expect(os.MkdirAll(filepath.Dir(path), 0750)).To(Succeed())
		Expect(os.WriteFile(path, []byte(contents), 0600)).To(Succeed())
	}
	addProcess := func(pid int, children string) {
		base := filepath.Join(root, strconv.Itoa(pid))
		Expect(os.MkdirAll(filepath.Join(base, "fd"), 0750)).To(Succeed())
		write(filepath.Join(base, "task", strconv.Itoa(pid), "children"), children)
	}
	addFD := func(pid, fd int, render, info string) {
		base := filepath.Join(root, strconv.Itoa(pid))
		name := strconv.Itoa(fd)
		Expect(os.Symlink("/dev/dri/"+render, filepath.Join(base, "fd", name))).To(Succeed())
		write(filepath.Join(base, "fdinfo", name), info)
	}
	BeforeEach(func() {
		var err error
		root, err = os.MkdirTemp("", "process-vram-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, root)
		addProcess(100, "")
	})

	It("sums resident device memory across GPUs and child processes without duplicate clients", func() {
		write(filepath.Join(root, "100/task/101/children"), "200")
		addProcess(200, "")
		info := "drm-client-id: 7\ndrm-total-local0: 900 MiB\ndrm-resident-local0: 128 MiB\ndrm-resident-system0: 4 GiB\n"
		addFD(100, 3, "renderD128", info)
		addFD(100, 4, "renderD128", info)
		addFD(200, 3, "renderD128", info)
		addFD(200, 4, "renderD129", "drm-client-id: 7\ndrm-resident-vram0: 256 MiB\n")
		used, ok := processVRAM(root, 100)
		Expect(ok).To(BeTrue())
		Expect(used).To(Equal(uint64(384 * 1024 * 1024)))
	})

	It("distinguishes a measured zero from unavailable accounting", func() {
		addFD(100, 3, "renderD128", "drm-client-id: 7\ndrm-resident-local0: 0 B\n")
		used, ok := processVRAM(root, 100)
		Expect(ok).To(BeTrue())
		Expect(used).To(BeZero())
	})

	DescribeTable("does not invent readings from unsupported or invalid accounting",
		func(info string) {
			addFD(100, 3, "renderD128", info)
			_, ok := processVRAM(root, 100)
			Expect(ok).To(BeFalse())
		},
		Entry("no resident keys", "drm-client-id: 7\ndrm-total-vram0: 128 MiB\n"),
		Entry("host memory only", "drm-client-id: 7\ndrm-resident-system0: 128 MiB\n"),
		Entry("no client identity", "drm-resident-vram0: 128 MiB\n"),
		Entry("malformed size", "drm-client-id: 7\ndrm-resident-vram0: unknown KiB\n"),
		Entry("unknown unit", "drm-client-id: 7\ndrm-resident-vram0: 128 widgets\n"),
		Entry("overflow", "drm-client-id: 7\ndrm-resident-vram0: 18446744073709551615 GiB\n"),
	)

	It("omits a partial reading if a child cannot be inspected", func() {
		addFD(100, 3, "renderD128", "drm-client-id: 7\ndrm-resident-vram0: 128 MiB\n")
		write(filepath.Join(root, "100/task/100/children"), "200")
		_, ok := processVRAM(root, 100)
		Expect(ok).To(BeFalse())
	})

	It("omits a partial reading if another DRM client lacks accounting", func() {
		addFD(100, 3, "renderD128", "drm-client-id: 7\ndrm-resident-vram0: 128 MiB\n")
		addFD(100, 4, "renderD129", "drm-client-id: 8\n")
		_, ok := processVRAM(root, 100)
		Expect(ok).To(BeFalse())
	})

	DescribeTable("omits mixed readings with unsupported GPU descriptors",
		func(target string) {
			addFD(100, 3, "renderD128", "drm-client-id: 7\ndrm-resident-vram0: 128 MiB\n")
			Expect(os.Symlink(target, filepath.Join(root, "100/fd/4"))).To(Succeed())
			_, ok := processVRAM(root, 100)
			Expect(ok).To(BeFalse())
		},
		Entry("primary DRM node", "/dev/dri/card0"),
		Entry("NVIDIA device", "/dev/nvidia0"),
	)

	It("returns unavailable for missing processes or no DRM descriptors", func() {
		for _, pid := range []int{-1, 0, 100, 999} {
			_, ok := processVRAM(root, pid)
			Expect(ok).To(BeFalse())
		}
	})
})
