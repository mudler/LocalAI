package xsysinfo

import (
	"github.com/jaypipes/ghw/pkg/gpu"
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
	// #nosec G304 -- path is the DRM root (a package constant in production,
	// a temp dir under test) joined with a ReadDir entry name and a fixed
	// attribute filename; no external input reaches it. gosec suggests
	// os.Root, which cannot work here: /sys/class/drm/cardN symlinks out to
	// the PCI device tree, and os.Root rejects that as "path escapes from
	// parent".
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	return parseHexUint(string(raw))
}

// parseHexUint reads a hex PCI ID as written by either source: sysfs
// spells it "0x8086\n", ghw's modalias parse yields a bare "8086".
func parseHexUint(raw string) (uint64, bool) {
	text := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "0x")
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
		for _, name := range names {
			if nameMatchesVendor(name, want) {
				return want
			}
		}
	}
	return ""
}

// nameMatchesVendor reports whether a PCI vendor name belongs to the
// requested vendor. The comparison is case-insensitive: pci.ids spells
// the vendor "NVIDIA Corporation" while callers pass the lowercase
// vendor constants.
func nameMatchesVendor(name, vendor string) bool {
	return strings.Contains(strings.ToUpper(name), strings.ToUpper(vendor))
}

// vendorFromGHW resolves the vendor of the highest-priority card ghw
// enumerated.
//
// It prefers the numeric PCI vendor ID over the pci.ids vendor name:
// ghw reads the ID from the kernel's modalias and only the name from
// the database, so a card absent from an outdated pci.ids still carries
// a usable ID while its name reads "unknown". The name is kept as a
// fallback for devices that expose no parseable ID.
func vendorFromGHW(cards []*gpu.GraphicsCard) string {
	var ids, names []string
	for _, c := range cards {
		if c == nil || c.DeviceInfo == nil || c.DeviceInfo.Vendor == nil {
			continue
		}
		ids = append(ids, c.DeviceInfo.Vendor.ID)
		names = append(names, c.DeviceInfo.Vendor.Name)
	}
	if vendor := vendorFromPCIIDs(ids); vendor != "" {
		return vendor
	}
	return vendorFromNames(names)
}

// vendorFromPCIIDs maps hex PCI vendor IDs onto a vendor constant,
// applying vendorPriority so enumeration order can't decide the answer.
func vendorFromPCIIDs(ids []string) string {
	found := map[string]bool{}
	for _, raw := range ids {
		if id, ok := parseHexUint(raw); ok {
			if vendor, known := pciVendorIDs[id]; known {
				found[vendor] = true
			}
		}
	}
	for _, want := range vendorPriority {
		if found[want] {
			return want
		}
	}
	return ""
}

// ghwHasVendor reports whether any card ghw enumerated belongs to the
// given vendor. Unlike vendorFromGHW this is not a priority pick: a
// hybrid-graphics host must answer yes for both its integrated and its
// discrete GPU.
func ghwHasVendor(cards []*gpu.GraphicsCard, vendor string) bool {
	for _, c := range cards {
		if c == nil || c.DeviceInfo == nil || c.DeviceInfo.Vendor == nil {
			continue
		}
		if id, ok := parseHexUint(c.DeviceInfo.Vendor.ID); ok {
			if pciVendorIDs[id] == vendor {
				return true
			}
			// A parseable ID that maps elsewhere is authoritative; don't
			// let the name fall through and contradict it.
			continue
		}
		if nameMatchesVendor(c.DeviceInfo.Vendor.Name, vendor) {
			return true
		}
	}
	return false
}
