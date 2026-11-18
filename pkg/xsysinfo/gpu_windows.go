//go:build windows

package xsysinfo

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/mudler/xlog"
	"golang.org/x/sys/windows"
)

// Windows GPU detection without external tools. DXGI lists the adapters with
// their name, PCI vendor, LUID and memory sizes. The "GPU Adapter Memory"
// performance counters (Windows 10 1709 and later) give the memory in use per
// adapter LUID, for all processes. Both are called through their system DLLs,
// so no cgo and no extra program is needed.

var (
	modDXGI                = windows.NewLazySystemDLL("dxgi.dll")
	procCreateDXGIFactory1 = modDXGI.NewProc("CreateDXGIFactory1")

	modPDH                           = windows.NewLazySystemDLL("pdh.dll")
	procPdhOpenQueryW                = modPDH.NewProc("PdhOpenQueryW")
	procPdhAddEnglishCounterW        = modPDH.NewProc("PdhAddEnglishCounterW")
	procPdhCollectQueryData          = modPDH.NewProc("PdhCollectQueryData")
	procPdhGetFormattedCounterArrayW = modPDH.NewProc("PdhGetFormattedCounterArrayW")
	procPdhCloseQuery                = modPDH.NewProc("PdhCloseQuery")
)

// IID_IDXGIFactory1 {770aae78-f26f-4dba-a829-253c83d1b387}
var iidIDXGIFactory1 = windows.GUID{
	Data1: 0x770aae78, Data2: 0xf26f, Data3: 0x4dba,
	Data4: [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87},
}

const (
	dxgiErrorNotFound     = 0x887A0002
	dxgiAdapterFlagSW     = 0x2
	vtblRelease           = 2
	vtblFactoryEnumAdapt1 = 12 // IDXGIFactory1::EnumAdapters1
	vtblAdapterGetDesc1   = 10 // IDXGIAdapter1::GetDesc1

	pdhFmtLarge  = 0x00000400
	pdhMoreData  = 0x800007D2
	pdhCStatusOK = 0x0 // PDH_CSTATUS_VALID_DATA
	pdhCStatusNw = 0x1 // PDH_CSTATUS_NEW_DATA
)

// comObject is the memory layout of any COM interface pointer: its first word
// points to the method table.
type comObject struct {
	vtbl *uintptr
}

func (o *comObject) call(index int, args ...uintptr) uintptr {
	fn := *(*uintptr)(unsafe.Add(unsafe.Pointer(o.vtbl), index*int(unsafe.Sizeof(uintptr(0)))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return r
}

func (o *comObject) release() { o.call(vtblRelease) }

// dxgiAdapterDesc1 is DXGI_ADAPTER_DESC1.
type dxgiAdapterDesc1 struct {
	Description           [128]uint16
	VendorID              uint32
	DeviceID              uint32
	SubSysID              uint32
	Revision              uint32
	DedicatedVideoMemory  uintptr
	DedicatedSystemMemory uintptr
	SharedSystemMemory    uintptr
	AdapterLuidLow        uint32
	AdapterLuidHigh       int32
	Flags                 uint32
}

func dxgiAdapters() ([]osAdapter, error) {
	if err := procCreateDXGIFactory1.Find(); err != nil {
		return nil, err
	}
	var factory *comObject
	hr, _, _ := procCreateDXGIFactory1.Call(
		uintptr(unsafe.Pointer(&iidIDXGIFactory1)),
		uintptr(unsafe.Pointer(&factory)),
	)
	if int32(hr) < 0 || factory == nil {
		return nil, fmt.Errorf("CreateDXGIFactory1 failed: 0x%08x", uint32(hr))
	}
	defer factory.release()

	var adapters []osAdapter
	for i := uint32(0); i < 64; i++ {
		var adapter *comObject
		hr := factory.call(vtblFactoryEnumAdapt1, uintptr(i), uintptr(unsafe.Pointer(&adapter)))
		if uint32(hr) == dxgiErrorNotFound {
			break
		}
		if int32(hr) < 0 || adapter == nil {
			return adapters, fmt.Errorf("EnumAdapters1(%d) failed: 0x%08x", i, uint32(hr))
		}
		var desc dxgiAdapterDesc1
		hr = adapter.call(vtblAdapterGetDesc1, uintptr(unsafe.Pointer(&desc)))
		adapter.release()
		if int32(hr) < 0 {
			continue
		}
		adapters = append(adapters, osAdapter{
			Name:                 windows.UTF16ToString(desc.Description[:]),
			VendorID:             desc.VendorID,
			LUID:                 luidKey(desc.AdapterLuidLow, desc.AdapterLuidHigh),
			DedicatedVideoMemory: uint64(desc.DedicatedVideoMemory),
			SharedSystemMemory:   uint64(desc.SharedSystemMemory),
			Software:             desc.Flags&dxgiAdapterFlagSW != 0,
		})
	}
	return adapters, nil
}

// pdhCounterItem is PDH_FMT_COUNTERVALUE_ITEM_W with a LONGLONG value. Name
// points into the same buffer; it is kept as an address, not a Go pointer, so
// the garbage collector never scans the buffer.
type pdhCounterItem struct {
	Name    uintptr
	CStatus uint32
	_       uint32
	Value   int64
}

// adapterMemoryUsage returns the dedicated and shared memory in use per
// adapter LUID, summed over all processes. It returns nil when the counters
// are not available.
func adapterMemoryUsage() map[uint64]osAdapterUsage {
	if err := procPdhOpenQueryW.Find(); err != nil {
		return nil
	}
	var query uintptr
	if r, _, _ := procPdhOpenQueryW.Call(0, 0, uintptr(unsafe.Pointer(&query))); r != 0 {
		return nil
	}
	defer func() { _, _, _ = procPdhCloseQuery.Call(query) }()

	addCounter := func(path string) uintptr {
		p, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return 0
		}
		var counter uintptr
		if r, _, _ := procPdhAddEnglishCounterW.Call(query, uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&counter))); r != 0 {
			return 0
		}
		return counter
	}
	dedicated := addCounter(`\GPU Adapter Memory(*)\Dedicated Usage`)
	shared := addCounter(`\GPU Adapter Memory(*)\Shared Usage`)
	if dedicated == 0 && shared == 0 {
		return nil
	}
	if r, _, _ := procPdhCollectQueryData.Call(query); r != 0 {
		return nil
	}

	usage := map[uint64]osAdapterUsage{}
	readCounter(dedicated, func(luid, v uint64) {
		u := usage[luid]
		u.Dedicated += v
		usage[luid] = u
	})
	readCounter(shared, func(luid, v uint64) {
		u := usage[luid]
		u.Shared += v
		usage[luid] = u
	})
	return usage
}

func readCounter(counter uintptr, add func(luid, value uint64)) {
	if counter == 0 {
		return
	}
	var size, count uint32
	r, _, _ := procPdhGetFormattedCounterArrayW.Call(counter, pdhFmtLarge,
		uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
	if uint32(r) != pdhMoreData || size == 0 {
		return
	}
	// A []uint64 buffer is 8-byte aligned and holds no Go pointers.
	buf := make([]uint64, size/8+1)
	r, _, _ = procPdhGetFormattedCounterArrayW.Call(counter, pdhFmtLarge,
		uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buf[0])))
	if r != 0 {
		return
	}
	bufBytes := uintptr(len(buf) * 8)
	itemSize := unsafe.Sizeof(pdhCounterItem{})
	if uintptr(count)*itemSize > bufBytes {
		return
	}
	base := uintptr(unsafe.Pointer(&buf[0]))
	items := unsafe.Slice((*pdhCounterItem)(unsafe.Pointer(&buf[0])), count)
	text := unsafe.Slice((*uint16)(unsafe.Pointer(&buf[0])), len(buf)*4)
	for _, item := range items {
		if item.CStatus != pdhCStatusOK && item.CStatus != pdhCStatusNw {
			continue
		}
		if item.Value < 0 || item.Name < base || item.Name >= base+bufBytes || (item.Name-base)%2 != 0 {
			continue
		}
		start := int((item.Name - base) / 2)
		end := start
		for end < len(text) && text[end] != 0 {
			end++
		}
		luid, ok := parseAdapterCounterInstance(windows.UTF16ToString(text[start:end]))
		if !ok {
			continue
		}
		add(luid, uint64(item.Value))
	}
}

// platformGPUMemory lists the hardware GPUs of a Windows host with their
// memory. It returns nil when DXGI is not available.
func platformGPUMemory() []GPUMemoryInfo {
	adapters, err := dxgiAdapters()
	if err != nil {
		xlog.Debug("DXGI adapter enumeration failed", "error", err)
	}
	if len(adapters) == 0 {
		return nil
	}
	usage := adapterMemoryUsage()
	if usage == nil {
		xlog.Debug("GPU memory counters are not available; reporting GPU memory usage as unknown")
	}
	return adapterGPUInfos(adapters, usage)
}
