package routes

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	studioendpoint "github.com/mudler/LocalAI/core/http/endpoints/studio"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("studio model checker", func() {
	It("accepts a resolvable alias and refuses unknown, dangling and chained ones", func() {
		dir := GinkgoT().TempDir()
		cl := config.NewModelConfigLoader(dir)
		for name, body := range map[string]string{
			"target":   "backend: llama-cpp\n",
			"alias":    "alias: target\n",
			"dangling": "alias: missing\n",
			"chain":    "alias: alias\n",
		} {
			path := filepath.Join(dir, name+".yaml")
			Expect(os.WriteFile(path, []byte("name: "+name+"\n"+body), 0600)).To(Succeed())
			Expect(cl.ReadModelConfig(path)).To(Succeed())
		}
		Expect(checkStudioModelResolves(cl, "target")).To(Succeed())
		Expect(checkStudioModelResolves(cl, "alias")).To(Succeed())
		for _, name := range []string{"nope", "dangling", "chain"} {
			Expect(checkStudioModelResolves(cl, name)).To(MatchError(studioendpoint.ErrUnknownModel), name)
		}
	})
})
