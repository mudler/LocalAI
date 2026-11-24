package utils

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("findFFmpeg", func() {
	notFound := func(string) (string, error) { return "", errors.New("not on PATH") }

	It("uses the program on PATH", func() {
		got, err := findFFmpeg("windows", "", func(name string) (string, error) {
			Expect(name).To(Equal("ffmpeg"))
			return `C:\tools\ffmpeg.exe`, nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(`C:\tools\ffmpeg.exe`))
	})

	It("prefers the override when it is a file", func() {
		bin := filepath.Join(GinkgoT().TempDir(), "ffmpeg.exe")
		Expect(os.WriteFile(bin, []byte("x"), 0o600)).To(Succeed())
		got, err := findFFmpeg("windows", bin, notFound)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(bin))
	})

	It("names the variable when the override is not a file", func() {
		_, err := findFFmpeg("linux", GinkgoT().TempDir(), notFound)
		Expect(err).To(MatchError(ErrFFmpegNotFound))
		Expect(err.Error()).To(ContainSubstring(FFmpegPathEnv))
	})

	It("tells a Windows user how to install it", func() {
		_, err := findFFmpeg("windows", "", notFound)
		Expect(err).To(MatchError(ErrFFmpegNotFound))
		Expect(err.Error()).To(ContainSubstring("winget install Gyan.FFmpeg"))
		Expect(err.Error()).To(ContainSubstring(FFmpegPathEnv))
	})

	It("gives a hint for each system", func() {
		Expect(FFmpegInstallHint("darwin")).To(ContainSubstring("brew install ffmpeg"))
		Expect(FFmpegInstallHint("linux")).To(ContainSubstring("apt install ffmpeg"))
	})
})

var _ = Describe("ffmpegEnv", func() {
	environ := []string{"PATH=/x", "LOCALAI_API_KEY=secret", "SystemRoot=C:\\Windows", "windir=C:\\Windows", "TEMP=C:\\t", "TMP=C:\\t"}

	It("passes nothing on Unix", func() {
		Expect(ffmpegEnv("linux", environ)).To(BeEmpty())
	})

	It("keeps only the system folders on Windows", func() {
		got := ffmpegEnv("windows", environ)
		Expect(got).To(ConsistOf("SystemRoot=C:\\Windows", "windir=C:\\Windows", "TEMP=C:\\t", "TMP=C:\\t"))
		Expect(strings.Join(got, " ")).NotTo(ContainSubstring("secret"))
	})
})
