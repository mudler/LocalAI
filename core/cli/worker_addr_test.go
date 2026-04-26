package cli

import (
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WorkerCMD address resolution", func() {
	Describe("effectiveBasePort", func() {
		DescribeTable("returns the correct port",
			func(addr, serve string, want int) {
				cmd := &WorkerCMD{Addr: addr, ServeAddr: serve}
				Expect(cmd.effectiveBasePort()).To(Equal(want))
			},
			Entry("Addr takes priority", "worker1.example.com:60000", "0.0.0.0:50051", 60000),
			Entry("falls back to ServeAddr", "", "0.0.0.0:50051", 50051),
			Entry("returns 50051 when neither set", "", "", 50051),
			Entry("Addr with custom port", "10.0.0.5:7000", "", 7000),
			Entry("invalid port in Addr falls through to ServeAddr", "host:notanumber", "0.0.0.0:9999", 9999),
		)
	})

	Describe("advertiseAddr", func() {
		It("returns AdvertiseAddr when set", func() {
			cmd := &WorkerCMD{
				AdvertiseAddr: "public.example.com:50051",
				Addr:          "10.0.0.5:60000",
			}
			Expect(cmd.advertiseAddr()).To(Equal("public.example.com:50051"))
		})

		It("returns Addr when set", func() {
			cmd := &WorkerCMD{Addr: "worker1.example.com:60000"}
			Expect(cmd.advertiseAddr()).To(Equal("worker1.example.com:60000"))
		})

		It("falls back to hostname:basePort", func() {
			cmd := &WorkerCMD{ServeAddr: "0.0.0.0:50051"}
			got := cmd.advertiseAddr()
			_, port, _ := strings.Cut(got, ":")
			Expect(port).To(Equal("50051"))

			hostname, _ := os.Hostname()
			if hostname != "" {
				host, _, _ := strings.Cut(got, ":")
				Expect(host).To(Equal(hostname))
			}
		})
	})

	Describe("resolveHTTPAddr", func() {
		DescribeTable("returns the correct address",
			func(httpAddr, addr, serve, want string) {
				cmd := &WorkerCMD{HTTPAddr: httpAddr, Addr: addr, ServeAddr: serve}
				Expect(cmd.resolveHTTPAddr()).To(Equal(want))
			},
			Entry("HTTPAddr takes priority", "0.0.0.0:8080", "", "", "0.0.0.0:8080"),
			Entry("derives from Addr port minus 1", "", "worker1:60000", "0.0.0.0:50051", "0.0.0.0:59999"),
			Entry("derives from ServeAddr port minus 1", "", "", "0.0.0.0:50051", "0.0.0.0:50050"),
			Entry("default when nothing set", "", "", "", "0.0.0.0:50050"),
		)
	})

	Describe("advertiseHTTPAddr", func() {
		DescribeTable("returns the correct address",
			func(advertiseHTTP, advertise, addr, serve, want string) {
				cmd := &WorkerCMD{
					AdvertiseHTTPAddr: advertiseHTTP,
					AdvertiseAddr:     advertise,
					Addr:              addr,
					ServeAddr:         serve,
				}
				Expect(cmd.advertiseHTTPAddr()).To(Equal(want))
			},
			Entry("AdvertiseHTTPAddr takes priority", "public.example.com:8080", "", "", "", "public.example.com:8080"),
			Entry("derives from advertiseAddr host + basePort-1", "", "", "worker1.example.com:60000", "", "worker1.example.com:59999"),
			Entry("uses AdvertiseAddr host with basePort-1", "", "public.example.com:60000", "10.0.0.5:60000", "", "public.example.com:59999"),
		)
	})
})
