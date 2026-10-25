package main

import (
	"unsafe"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The offsets below were read from the compiler for timestral_forecast_request
// in timestral_capi.h (ABI version 1) on a 64-bit target. If the library moves a
// field, the ABI version must change and this test must fail first.
var _ = Describe("forecastRequest layout", func() {
	It("matches timestral_forecast_request", func() {
		var r forecastRequest
		Expect(unsafe.Sizeof(r)).To(Equal(uintptr(104)))
		for name, want := range map[string][2]uintptr{
			"context":         {unsafe.Offsetof(r.context), 0},
			"observationMask": {unsafe.Offsetof(r.observationMask), 8},
			"B":               {unsafe.Offsetof(r.B), 16},
			"V":               {unsafe.Offsetof(r.V), 20},
			"T":               {unsafe.Offsetof(r.T), 24},
			"predictionLen":   {unsafe.Offsetof(r.predictionLen), 28},
			"pastCov":         {unsafe.Offsetof(r.pastCov), 32},
			"cPast":           {unsafe.Offsetof(r.cPast), 40},
			"knownCov":        {unsafe.Offsetof(r.knownCov), 48},
			"cKnown":          {unsafe.Offsetof(r.cKnown), 56},
			"catPast":         {unsafe.Offsetof(r.catPast), 64},
			"ccPast":          {unsafe.Offsetof(r.ccPast), 72},
			"catKnown":        {unsafe.Offsetof(r.catKnown), 80},
			"ccKnown":         {unsafe.Offsetof(r.ccKnown), 88},
			"symmetricAvg":    {unsafe.Offsetof(r.symmetricAvg), 92},
			"clipNonneg":      {unsafe.Offsetof(r.clipNonneg), 96},
			"maxContext":      {unsafe.Offsetof(r.maxContext), 100},
		} {
			Expect(want[0]).To(Equal(want[1]), "offset of %s", name)
		}
	})
})
