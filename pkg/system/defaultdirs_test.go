package system

import (
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("resolveDefaultDirs", func() {
	var (
		cwd, home, local, programData string
		env                           map[string]string
	)

	BeforeEach(func() {
		root := GinkgoT().TempDir()
		cwd = filepath.Join(root, "work")
		home = filepath.Join(root, "home")
		local = filepath.Join(root, "local")
		programData = filepath.Join(root, "programdata")
		for _, d := range []string{cwd, home, local, programData} {
			Expect(os.MkdirAll(d, 0o750)).To(Succeed())
		}
		env = map[string]string{}
	})

	host := func(goos string) dirsHost {
		return dirsHost{
			goos:   goos,
			cwd:    cwd,
			home:   home,
			getenv: func(k string) string { return env[k] },
			folder: func(f knownFolder) (string, error) {
				if f == folderProgramData {
					return programData, nil
				}
				return local, nil
			},
		}
	}

	write := func(rel string) {
		p := filepath.Join(cwd, rel)
		Expect(os.MkdirAll(filepath.Dir(p), 0o750)).To(Succeed())
		Expect(os.WriteFile(p, []byte("x"), 0o600)).To(Succeed())
	}

	Context("on Linux and macOS", func() {
		for _, goos := range []string{"linux", "darwin"} {
			It("keeps the defaults of earlier releases on "+goos, func() {
				d := resolveDefaultDirs(host(goos))
				Expect(d.Base).To(Equal(cwd))
				Expect(d.Origin).To(Equal(OriginWorkingDir))
				Expect(d.SystemBackends).To(Equal("/var/lib/local-ai/backends"))
				Expect(d.EnvFiles).To(Equal([]string{
					".env", "localai.env",
					filepath.Join(home, "localai.env"), filepath.Join(home, ".config/localai.env"),
					"/etc/localai.env",
				}))
			})
		}

		It("ignores LocalAI files in the current folder", func() {
			write(filepath.Join("models", "a.yaml"))
			d := resolveDefaultDirs(host("linux"))
			Expect(d.Base).To(Equal(cwd))
			Expect(d.Origin).To(Equal(OriginWorkingDir))
		})

		It("leaves the home files out when the home folder is unknown", func() {
			h := host("linux")
			h.home = ""
			Expect(resolveDefaultDirs(h).EnvFiles).To(Equal([]string{".env", "localai.env", "/etc/localai.env"}))
		})
	})

	Context("on Windows", func() {
		It("uses the local application data folder", func() {
			d := resolveDefaultDirs(host("windows"))
			Expect(d.Base).To(Equal(filepath.Join(local, "LocalAI")))
			Expect(d.Origin).To(Equal(OriginStandard))
			Expect(d.SystemBackends).To(Equal(filepath.Join(programData, "LocalAI", "backends")))
		})

		It("reads the user and machine env files instead of /etc/localai.env", func() {
			Expect(resolveDefaultDirs(host("windows")).EnvFiles).To(Equal([]string{
				".env", "localai.env",
				filepath.Join(home, "localai.env"), filepath.Join(home, ".config/localai.env"),
				filepath.Join(local, "LocalAI", "localai.env"),
				filepath.Join(programData, "LocalAI", "localai.env"),
			}))
		})

		DescribeTable("keeps a current folder that holds LocalAI files",
			func(marker string) {
				write(marker)
				d := resolveDefaultDirs(host("windows"))
				Expect(d.Base).To(Equal(cwd))
				Expect(d.Origin).To(Equal(OriginLegacy))
				Expect(d.Reason).To(Equal(marker))
				Expect(d.Standard).To(Equal(filepath.Join(local, "LocalAI")))
			},
			Entry("an installed backend", filepath.Join("backends", "cpu-llama-cpp", "metadata.json")),
			Entry("a model config", filepath.Join("models", "phi.yaml")),
			Entry("a model config with .yml", filepath.Join("models", "phi.yml")),
			Entry("a model file", filepath.Join("models", "Phi.GGUF")),
			Entry("the database", filepath.Join("data", "database.db")),
			Entry("runtime settings", filepath.Join("configuration", "runtime_settings.json")),
			Entry("API keys", filepath.Join("configuration", "api_keys.json")),
		)

		It("does not take folders of other programs for LocalAI files", func() {
			write(filepath.Join("data", "train.csv"))
			write(filepath.Join("models", "notes.txt"))
			write(filepath.Join("backends", "readme.md"))
			Expect(resolveDefaultDirs(host("windows")).Origin).To(Equal(OriginStandard))
		})

		It("does not call the standard folder a legacy folder", func() {
			cwd = filepath.Join(local, "LocalAI")
			write(filepath.Join("models", "phi.yaml"))
			d := resolveDefaultDirs(host("windows"))
			Expect(d.Origin).To(Equal(OriginStandard))
			Expect(d.Base).To(Equal(filepath.Join(local, "LocalAI")))
		})

		It("falls back to the current folder when the data folder is unknown", func() {
			h := host("windows")
			h.folder = func(knownFolder) (string, error) { return "", errors.New("no profile") }
			d := resolveDefaultDirs(h)
			Expect(d.Base).To(Equal(cwd))
			Expect(d.Origin).To(Equal(OriginFallback))
			Expect(d.Reason).To(ContainSubstring("no profile"))
			Expect(d.SystemBackends).To(Equal("/var/lib/local-ai/backends"))
			Expect(d.EnvFiles).To(Equal([]string{
				".env", "localai.env",
				filepath.Join(home, "localai.env"), filepath.Join(home, ".config/localai.env"),
			}))
		})
	})

	Context("with LOCALAI_BASE_PATH", func() {
		for _, goos := range []string{"linux", "darwin", "windows"} {
			It("moves the base folder on "+goos, func() {
				env[BasePathEnv] = filepath.Join(home, "big-disk")
				write(filepath.Join("models", "phi.yaml"))
				d := resolveDefaultDirs(host(goos))
				Expect(d.Base).To(Equal(filepath.Join(home, "big-disk")))
				Expect(d.Origin).To(Equal(OriginEnv))
			})
		}

		It("makes a relative value absolute from the current folder", func() {
			env[BasePathEnv] = "store"
			Expect(resolveDefaultDirs(host("linux")).Base).To(Equal(filepath.Join(cwd, "store")))
		})

		It("expands a leading ~ to the home folder", func() {
			env[BasePathEnv] = "~/store"
			Expect(resolveDefaultDirs(host("linux")).Base).To(Equal(filepath.Join(home, "store")))
		})

		It("does not move the system backends or the env files", func() {
			env[BasePathEnv] = filepath.Join(home, "big-disk")
			d := resolveDefaultDirs(host("linux"))
			Expect(d.SystemBackends).To(Equal("/var/lib/local-ai/backends"))
			Expect(d.EnvFiles).To(ContainElement("/etc/localai.env"))
		})
	})
})

var _ = Describe("ResolveDefaultDirs", func() {
	It("uses the current folder on this host unless LOCALAI_BASE_PATH is set", func() {
		if os.Getenv(BasePathEnv) != "" {
			Skip("LOCALAI_BASE_PATH is set")
		}
		if isWindows() {
			Skip("Windows uses the local application data folder")
		}
		wd, err := filepath.Abs(".")
		Expect(err).NotTo(HaveOccurred())
		Expect(ResolveDefaultDirs().Base).To(Equal(wd))
	})
})

var _ = Describe("LongPath", func() {
	It("flags Windows paths longer than the warning length", func() {
		long := "C:\\" + string(make([]byte, LongPathWarnLength))
		Expect(LongPath("windows", long)).To(BeTrue())
		Expect(LongPath("windows", "C:\\LocalAI\\models")).To(BeFalse())
	})

	It("never flags a path on other systems", func() {
		long := "/" + string(make([]byte, LongPathWarnLength*2))
		Expect(LongPath("linux", long)).To(BeFalse())
	})
})

var _ = Describe("standardDataDir", func() {
	local := func(knownFolder) (string, error) { return "/x/AppData/Local", nil }

	It("is the LocalAI folder in the local application data folder on Windows", func() {
		Expect(standardDataDir("windows", local)).To(Equal(filepath.Join("/x/AppData/Local", "LocalAI")))
	})

	It("does not exist on other systems", func() {
		_, err := standardDataDir("linux", local)
		Expect(err).To(HaveOccurred())
	})

	It("fails when the folder is unknown", func() {
		_, err := standardDataDir("windows", func(knownFolder) (string, error) { return "", errors.New("no profile") })
		Expect(err).To(MatchError(ContainSubstring("no profile")))
	})
})
