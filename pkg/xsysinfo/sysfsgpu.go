package xsysinfo

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jaypipes/ghw/pkg/gpu"
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

// readAMDSysfsGPUMemory reports AMD GPU memory from the amdgpu driver's
// mem_info_* attributes under the DRM sysfs tree.
//
// It is the fallback for images without rocm-smi: ROCm 7+ images ship
// only amd-smi, so a working Strix Halo reported "No GPU found" and
// gpu_count 0 while inference ran on the GPU. The kernel exposes the
// same VRAM and GTT figures rocm-smi reads, so the shared-memory APU
// rule (#12058) applies unchanged.
func readAMDSysfsGPUMemory(root string) []GPUMemoryInfo {
	type amdCard struct {
		sysfsGPU
		vramTotal, vramUsed, gttTotal, gttUsed uint64
		name                                   string
	}
	var cards []*amdCard
	for _, card := range scanSysfsGPUs(root) {
		if card.Vendor != VendorAMD {
			continue
		}
		deviceDir := filepath.Join(root, card.Card, "device")
		vramTotal, ok := readSysfsDecUint(filepath.Join(deviceDir, "mem_info_vram_total"))
		if !ok {
			continue
		}
		c := &amdCard{sysfsGPU: card, vramTotal: vramTotal}
		c.vramUsed, _ = readSysfsDecUint(filepath.Join(deviceDir, "mem_info_vram_used"))
		c.gttTotal, _ = readSysfsDecUint(filepath.Join(deviceDir, "mem_info_gtt_total"))
		c.gttUsed, _ = readSysfsDecUint(filepath.Join(deviceDir, "mem_info_gtt_used"))
		c.name = readSysfsString(filepath.Join(deviceDir, "product_name"))
		cards = append(cards, c)
	}

	// The amdgpu driver only exposes product_name on some boards. Ask
	// Vulkan for the rest, and only then, since it runs a subprocess.
	var unnamed []*amdCard
	for _, c := range cards {
		if c.name == "" {
			unnamed = append(unnamed, c)
		}
	}
	if len(unnamed) > 0 {
		addrs := make([]string, len(unnamed))
		for i, c := range unnamed {
			addrs[i] = pciAddress(filepath.Join(root, c.Card, "device"))
		}
		for i, name := range matchVulkanAMDNames(addrs, listVulkanDevices()) {
			unnamed[i].name = name
		}
		for _, c := range unnamed {
			if c.name != "" {
				continue
			}
			if id, ok := readSysfsHexUint(filepath.Join(root, c.Card, "device", "device")); ok {
				c.name = lookupAMDPCIName(id)
			}
		}
	}

	gpus := make([]GPUMemoryInfo, 0, len(cards))
	for _, c := range cards {
		index := parseAMDDeviceIndex(c.Card)
		if c.name == "" {
			c.name = fmt.Sprintf("%s %d", amdDefaultName, index)
		}
		gpus = append(gpus, amdGPUMemoryInfo(index, c.name,
			c.vramTotal, c.vramUsed, c.gttTotal, c.gttUsed))
	}
	return gpus
}

// amdDefaultName is the name given to an AMD GPU when nothing better is
// known. The sysfs path appends the card index to it.
const amdDefaultName = "AMD GPU"

// vulkanDevice is one physical device reported by vulkaninfo.
type vulkanDevice struct {
	name     string
	vendorID uint64
	// busAddr is the PCI address ("0000:03:00.0") when the driver exposes
	// VK_EXT_pci_bus_info, else empty.
	busAddr string
}

// listVulkanDevices runs vulkaninfo. It is a variable so tests can
// supply a fixed list.
var listVulkanDevices = func() []vulkanDevice {
	if _, err := exec.LookPath("vulkaninfo"); err != nil {
		return nil
	}
	out, err := exec.Command("vulkaninfo", "--text").Output()
	if err != nil {
		return nil
	}
	return parseVulkanDevices(strings.NewReader(string(out)))
}

// parseVulkanDevices reads name, vendor and PCI address of every
// non-CPU device from `vulkaninfo --text` output.
func parseVulkanDevices(r io.Reader) []vulkanDevice {
	var devs []vulkanDevice
	var cur *vulkanDevice
	var isCPU bool
	var domain, bus, dev, fn uint64
	var havePCI bool
	flush := func() {
		if cur == nil || cur.name == "" || isCPU {
			return
		}
		if havePCI {
			cur.busAddr = fmt.Sprintf("%04x:%02x:%02x.%x", domain, bus, dev, fn)
		}
		devs = append(devs, *cur)
	}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if _, ok := parseVulkanGPUHeader(line); ok {
			flush()
			cur, isCPU, havePCI = &vulkanDevice{}, false, false
			domain, bus, dev, fn = 0, 0, 0, 0
			continue
		}
		if cur == nil {
			continue
		}
		key, _, _ := strings.Cut(line, "=")
		switch strings.TrimSpace(key) {
		case "deviceName":
			cur.name = parseVulkanValue(line)
		case "deviceType":
			isCPU = parseVulkanValue(line) == "PHYSICAL_DEVICE_TYPE_CPU"
		case "vendorID":
			cur.vendorID, _ = parseHexUint(parseVulkanValue(line))
		case "pciDomain":
			domain, _ = parseVulkanUintValue(line)
		case "pciBus":
			bus, _ = parseVulkanUintValue(line)
			havePCI = true
		case "pciDevice":
			dev, _ = parseVulkanUintValue(line)
		case "pciFunction":
			fn, _ = parseVulkanUintValue(line)
		}
	}
	flush()
	return devs
}

// matchVulkanAMDNames returns the Vulkan name for each PCI address
// (index-aligned, "" when unmatched). Devices match by PCI address; any
// left over are paired in order, but only when the counts agree and the
// remaining Vulkan devices report no address, so a wrong guess cannot
// swap the names of two cards.
func matchVulkanAMDNames(addrs []string, devices []vulkanDevice) []string {
	names := make([]string, len(addrs))
	var amd []vulkanDevice
	for _, d := range devices {
		if d.vendorID == 0x1002 {
			amd = append(amd, d)
		}
	}
	used := make([]bool, len(amd))
	for i, addr := range addrs {
		for j, d := range amd {
			if !used[j] && addr != "" && strings.EqualFold(d.busAddr, addr) {
				names[i], used[j] = d.name, true
				break
			}
		}
	}
	var freeCards, freeDevs []int
	for i, n := range names {
		if n == "" {
			freeCards = append(freeCards, i)
		}
	}
	for j, d := range amd {
		if !used[j] {
			if d.busAddr != "" {
				return names
			}
			freeDevs = append(freeDevs, j)
		}
	}
	if len(freeCards) == len(freeDevs) {
		for k, i := range freeCards {
			names[i] = amd[freeDevs[k]].name
		}
	}
	return names
}

// pciIDsPaths are the places a pci.ids database is installed. It is a
// variable so tests can point it at a fake file.
var pciIDsPaths = []string{
	"/usr/share/hwdata/pci.ids",
	"/usr/share/misc/pci.ids",
	"/usr/share/pci.ids",
	"/usr/share/hwdata/pci.ids.gz",
	"/usr/share/misc/pci.ids.gz",
	"/usr/share/pci.ids.gz",
}

// lookupAMDPCIName returns the pci.ids name of an AMD (ATI, 0x1002)
// device, or "" when no database or entry exists. Unlike ghw it never
// fails the caller: a missing database just means no name.
func lookupAMDPCIName(deviceID uint64) string {
	for _, path := range pciIDsPaths {
		// #nosec G304 -- fixed system paths, or a temp file under test.
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		var r io.Reader = f
		if strings.HasSuffix(path, ".gz") {
			zr, err := gzip.NewReader(f)
			if err != nil {
				_ = f.Close()
				continue
			}
			r = zr
		}
		name := scanPCIIDsDevice(r, 0x1002, deviceID)
		_ = f.Close()
		if name != "" {
			return amdMarketingName(name)
		}
	}
	return ""
}

// scanPCIIDsDevice streams a pci.ids file and returns the device name
// for the vendor/device pair, stopping at the match or at the end of the
// vendor's block. Format: "vvvv  Vendor" at column 0, "\tdddd  Device"
// with one tab, and subsystem lines with two tabs, which are skipped.
func scanPCIIDsDevice(r io.Reader, vendor, device uint64) string {
	scanner := bufio.NewScanner(r)
	inVendor := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] != '\t' {
			if inVendor {
				return ""
			}
			id, _, _ := strings.Cut(line, " ")
			if v, ok := parseHexUint(id); ok && len(id) == 4 && v == vendor {
				inVendor = true
			}
			continue
		}
		if !inVendor || strings.HasPrefix(line, "\t\t") {
			continue
		}
		id, name, _ := strings.Cut(strings.TrimPrefix(line, "\t"), " ")
		if v, ok := parseHexUint(id); ok && len(id) == 4 && v == device {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

// amdMarketingName turns a pci.ids device name such as
// "Strix Halo [Radeon Graphics / Radeon 8060S Graphics]" into the
// bracketed marketing text when present, prefixed with "AMD " unless it
// already names the vendor.
func amdMarketingName(name string) string {
	if start := strings.LastIndex(name, "["); start >= 0 && strings.HasSuffix(name, "]") {
		if inner := strings.TrimSpace(name[start+1 : len(name)-1]); inner != "" {
			name = inner
		}
	}
	upper := strings.ToUpper(name)
	if strings.HasPrefix(upper, "AMD") || strings.HasPrefix(upper, "ADVANCED MICRO DEVICES") {
		return name
	}
	return "AMD " + name
}

// pciAddress returns the PCI address a sysfs device directory resolves
// to, or "" when it does not point at one.
func pciAddress(deviceDir string) string {
	target, err := filepath.EvalSymlinks(deviceDir)
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

func readSysfsString(path string) string {
	// #nosec G304 -- same provenance as readSysfsHexUint.
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func readSysfsDecUint(path string) (uint64, bool) {
	// #nosec G304 -- same provenance as readSysfsHexUint.
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}
