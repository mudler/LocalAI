//go:build windows

package xsysinfo

import "testing"

// TestDXGIEnumeration runs the DXGI and counter calls on a real Windows host.
// A CI runner may have no hardware GPU, so the test checks only that the calls
// work and that every reported GPU is consistent.
func TestDXGIEnumeration(t *testing.T) {
	adapters, err := dxgiAdapters()
	if err != nil {
		t.Fatalf("DXGI enumeration failed: %v", err)
	}
	if len(adapters) == 0 {
		t.Fatal("DXGI reported no adapter; it always reports at least the software renderer")
	}
	_ = adapterMemoryUsage()
	for _, g := range platformGPUMemory() {
		if g.TotalVRAM == 0 || g.UsedVRAM > g.TotalVRAM || g.FreeVRAM != g.TotalVRAM-g.UsedVRAM {
			t.Errorf("inconsistent GPU entry: %+v", g)
		}
		if g.Vendor == "" || g.Name == "" {
			t.Errorf("GPU entry without vendor or name: %+v", g)
		}
	}
}
