package xsysinfo

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// defaultSysfsDRMPath is where the kernel exposes the DRM subsystem.
const defaultSysfsDRMPath = "/sys/class/drm"

// pciVendorIDs maps PCI vendor IDs to the vendor constants used
// throughout this package. AMD graphics devices carry the ATI ID
// (0x1002), not AMD's host-bridge ID.
var pciVendorIDs = map[uint64]string{
	0x10de: VendorNVIDIA,
	0x1002: VendorAMD,
	0x8086: VendorIntel,
}

// vendorPriority is the order DetectGPUVendor reports a vendor in when
// a host has cards from more than one: a discrete accelerator always
// outranks an integrated display adapter.
var vendorPriority = []string{VendorNVIDIA, VendorAMD, VendorIntel}

// sysfsGPU is a GPU discovered by walking the kernel's DRM sysfs tree.
type sysfsGPU struct {
	Card   string
	Vendor string
}

// scanSysfsGPUs enumerates GPUs straight from the kernel's DRM sysfs
// tree, identifying each by its raw PCI vendor ID.
//
// This is deliberately dependency-free. ghw, our richer source of GPU
// information, needs a pci.ids database file on disk to translate
// vendor IDs into names, and ghw.GPU() fails outright when it can't
// find one. Container images that ship no pci.ids (the Intel oneAPI
// image among them) therefore get no GPU detection at all, which is
// what issue #10941 reported: a correctly passed-through Arc A310
// reported as "No GPU detected" with zero VRAM. Reading the vendor ID
// ourselves needs nothing but /sys.
func scanSysfsGPUs(root string) []sysfsGPU {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var gpus []sysfsGPU
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "card") {
			continue
		}
		// cardN-<INTERFACE>-<ID> entries are display connectors hanging
		// off a card, not cards in their own right.
		if strings.ContainsRune(name, '-') {
			continue
		}
		deviceDir := filepath.Join(root, name, "device")
		vendorID, ok := readSysfsHexUint(filepath.Join(deviceDir, "vendor"))
		if !ok {
			continue
		}
		vendor, known := pciVendorIDs[vendorID]
		if !known {
			// Server BMC display adapters (ASPEED, Matrox) and virtual
			// VGA devices land here. They're not usable accelerators.
			continue
		}
		gpus = append(gpus, sysfsGPU{Card: name, Vendor: vendor})
	}
	return gpus
}

// sysfsVendorPriority returns the highest-priority GPU vendor found in
// the DRM tree, or "" when the host has no recognised GPU.
func sysfsVendorPriority(root string) string {
	gpus := scanSysfsGPUs(root)
	for _, want := range vendorPriority {
		for _, g := range gpus {
			if g.Vendor == want {
				return want
			}
		}
	}
	return ""
}

func readSysfsHexUint(path string) (uint64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	text := strings.TrimSpace(string(raw))
	text = strings.TrimPrefix(strings.ToLower(text), "0x")
	value, err := strconv.ParseUint(text, 16, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

// vendorFromNames maps a set of PCI vendor name strings (as ghw
// resolves them from pci.ids) onto a vendor constant, applying
// vendorPriority so enumeration order can't decide the answer.
func vendorFromNames(names []string) string {
	for _, want := range vendorPriority {
		needle := strings.ToUpper(want)
		for _, name := range names {
			if strings.Contains(strings.ToUpper(name), needle) {
				return want
			}
		}
	}
	return ""
}

// vendorMatchesAny reports whether any of the given PCI vendor names
// belongs to the requested vendor. The comparison is case-insensitive:
// pci.ids spells the vendor "NVIDIA Corporation" while callers pass the
// lowercase vendor constants.
func vendorMatchesAny(names []string, vendor string) bool {
	needle := strings.ToUpper(vendor)
	for _, name := range names {
		if strings.Contains(strings.ToUpper(name), needle) {
			return true
		}
	}
	return false
}
