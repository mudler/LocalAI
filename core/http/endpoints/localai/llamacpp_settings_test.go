package localai_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/pkg/llamacppsettings"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("llama.cpp runtime settings endpoints", func() {
	const inkling = `llama-server -m Inkling-2-Mini-UD-Q3_K_XL.gguf --jinja --ctx-size 18000 --parallel 1 ` +
		`--cache-type-k q8_0 --cache-type-v q8_0 --temp 1.0 --top-p 1.0 --top-k 0 --min-p 0.0 -ub 64 -ngl 99 ` +
		`-fit off -fa on -t 6 -tb 12 --no-op-offload --load-mode none ` +
		`-ot "blk\.([0-6]|1[89]|2[0-3])\.ffn_.*_exps.*=CPU"`

	var app *echo.Echo

	BeforeEach(func() {
		app = echo.New()
		app.POST("/api/llama-cpp/parse-command", LlamaCppParseCommandEndpoint())
		app.POST("/api/llama-cpp/command-preview", LlamaCppCommandPreviewEndpoint())
		app.GET("/api/llama-cpp/settings-schema", LlamaCppSettingsSchemaEndpoint())
		app.POST("/api/llama-cpp/tensor-placement", LlamaCppTensorPlacementEndpoint())
	})

	do := func(method, target, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	jsonBody := func(v any) string {
		b, err := json.Marshal(v)
		Expect(err).ToNot(HaveOccurred())
		return string(b)
	}

	Describe("parse-command", func() {
		It("parses the Inkling command", func() {
			rec := do(http.MethodPost, "/api/llama-cpp/parse-command", jsonBody(map[string]string{"command": inkling}))
			Expect(rec.Code).To(Equal(http.StatusOK))
			var got map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
			Expect(got["unmapped"]).To(BeEmpty())
			Expect(got["warnings"]).To(BeEmpty())
			Expect(got["options"]).To(HaveLen(7))
			fields := got["fields"].(map[string]any)
			Expect(fields).ToNot(HaveKey("options"))
			Expect(fields["context_size"]).To(BeEquivalentTo(18000))
			Expect(fields["mmap"]).To(BeFalse())
			Expect(fields["flash_attention"]).To(Equal("on"))
			params := fields["parameters"].(map[string]any)
			Expect(params["model"]).To(Equal("Inkling-2-Mini-UD-Q3_K_XL.gguf"))
			Expect(params).To(HaveKeyWithValue("top_k", BeEquivalentTo(0)))
			Expect(params).To(HaveKeyWithValue("min_p", BeEquivalentTo(0)))
			if dir := os.Getenv("LLAMACPP_DUMP_DIR"); dir != "" {
				_ = os.WriteFile(dir+"/parse.json", rec.Body.Bytes(), 0o600)
			}
		})

		It("keeps unknown flags and reports them", func() {
			rec := do(http.MethodPost, "/api/llama-cpp/parse-command", `{"command":"llama-server --rope-freq-base 10000"}`)
			Expect(rec.Code).To(Equal(http.StatusOK))
			var got llamacppsettings.ParseResult
			Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
			Expect(got.Options).To(Equal([]string{"--rope-freq-base:10000"}))
			Expect(got.Unmapped).To(HaveLen(1))
		})

		It("never runs the command", func() {
			marker := GinkgoT().TempDir() + "/ran"
			cmd := "llama-server -c 1; touch " + marker + " $(touch " + marker + ") `touch " + marker + "`"
			rec := do(http.MethodPost, "/api/llama-cpp/parse-command", jsonBody(map[string]string{"command": cmd}))
			Expect(rec.Code).To(Equal(http.StatusOK))
			_, err := os.Stat(marker)
			Expect(os.IsNotExist(err)).To(BeTrue())
			Expect(rec.Body.String()).To(ContainSubstring("shell operators"))
		})

		It("rejects bad input", func() {
			Expect(do(http.MethodPost, "/api/llama-cpp/parse-command", `not json`).Code).To(Equal(http.StatusBadRequest))
			Expect(do(http.MethodPost, "/api/llama-cpp/parse-command", ``).Code).To(Equal(http.StatusBadRequest))
			Expect(do(http.MethodPost, "/api/llama-cpp/parse-command", `{"command":"llama-server \"oops"}`).Code).To(Equal(http.StatusBadRequest))
			Expect(do(http.MethodPost, "/api/llama-cpp/parse-command", `{"command":"a\u0000b"}`).Code).To(Equal(http.StatusBadRequest))
		})

		It("limits the size of the command and of the body", func() {
			long := jsonBody(map[string]string{"command": strings.Repeat("a", llamacppsettings.MaxCommandLength+1)})
			Expect(do(http.MethodPost, "/api/llama-cpp/parse-command", long).Code).To(Equal(http.StatusRequestEntityTooLarge))
			huge := `{"command":"` + strings.Repeat("a", 1<<20) + `"}`
			Expect(do(http.MethodPost, "/api/llama-cpp/parse-command", huge).Code).To(Equal(http.StatusRequestEntityTooLarge))
		})

		It("returns errors in the common error format", func() {
			rec := do(http.MethodPost, "/api/llama-cpp/parse-command", `not json`)
			var got map[string]map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
			Expect(got["error"]["message"]).ToNot(BeEmpty())
		})
	})

	Describe("command-preview", func() {
		It("builds the command of the Inkling config and it parses back", func() {
			parsed := do(http.MethodPost, "/api/llama-cpp/parse-command", jsonBody(map[string]string{"command": inkling}))
			var p llamacppsettings.ParseResult
			Expect(json.Unmarshal(parsed.Body.Bytes(), &p)).To(Succeed())
			cfg := p.Fields
			cfg.Options = p.Options

			rec := do(http.MethodPost, "/api/llama-cpp/command-preview", jsonBody(map[string]any{"config": cfg}))
			Expect(rec.Code).To(Equal(http.StatusOK))
			var got llamacppsettings.BuildResult
			Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
			Expect(got.Command).To(HavePrefix("llama-server -m Inkling-2-Mini-UD-Q3_K_XL.gguf "))
			Expect(got.Command).To(ContainSubstring(`-ot "blk\.([0-6]|1[89]|2[0-3])\.ffn_.*_exps.*=CPU"`))
			if dir := os.Getenv("LLAMACPP_DUMP_DIR"); dir != "" {
				_ = os.WriteFile(dir+"/preview.json", rec.Body.Bytes(), 0o600)
			}

			again := do(http.MethodPost, "/api/llama-cpp/parse-command", jsonBody(map[string]string{"command": got.Command}))
			var p2 llamacppsettings.ParseResult
			Expect(json.Unmarshal(again.Body.Bytes(), &p2)).To(Succeed())
			Expect(p2.Fields).To(Equal(p.Fields))
			Expect(p2.Options).To(Equal(p.Options))
		})

		It("accepts an empty config and ignores unknown keys", func() {
			rec := do(http.MethodPost, "/api/llama-cpp/command-preview", `{"config":{"name":"x","backend":"llama-cpp"}}`)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Body.String()).To(ContainSubstring(`"command":"llama-server"`))
		})

		It("rejects bad input", func() {
			Expect(do(http.MethodPost, "/api/llama-cpp/command-preview", `{"config":`).Code).To(Equal(http.StatusBadRequest))
			Expect(do(http.MethodPost, "/api/llama-cpp/command-preview", `{"config":{"context_size":"big"}}`).Code).To(Equal(http.StatusBadRequest))
			huge := `{"config":{"options":["` + strings.Repeat("a", 1<<20) + `"]}}`
			Expect(do(http.MethodPost, "/api/llama-cpp/command-preview", huge).Code).To(Equal(http.StatusRequestEntityTooLarge))
		})
	})

	Describe("settings-schema", func() {
		It("lists every flag with its group and doc", func() {
			rec := do(http.MethodGet, "/api/llama-cpp/settings-schema", "")
			Expect(rec.Code).To(Equal(http.StatusOK))
			var got LlamaCppSettingsSchemaResponse
			Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
			Expect(got.Flags).To(HaveLen(len(llamacppsettings.Flags)))
			groups := map[string]bool{}
			for _, f := range got.Flags {
				Expect(f.Doc).ToNot(BeEmpty(), f.Key)
				groups[string(f.Group)] = true
			}
			Expect(groups).To(HaveKey("memory"))
			Expect(groups).To(HaveKey("speed"))
			Expect(groups).To(HaveKey("sampling"))
			Expect(groups).To(HaveKey("template"))
			Expect(groups).To(HaveKey("placement"))
			Expect(got.LoadModes).To(ContainElement("none"))
		})
	})

	Describe("tensor-placement", func() {
		It("builds an option from layer ranges", func() {
			rec := do(http.MethodPost, "/api/llama-cpp/tensor-placement",
				`{"placement":{"mode":"experts_cpu_layers"},"layers":"0-6,18-23"}`)
			Expect(rec.Code).To(Equal(http.StatusOK))
			var got LlamaCppPlacementResponse
			Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
			Expect(got.Option).To(Equal(`-ot:blk\.([0-6]|1[89]|2[0-3])\.ffn_.*_exps.*=CPU`))
			Expect(got.Placement.Layers).To(Equal([]llamacppsettings.LayerRange{{From: 0, To: 6}, {From: 18, To: 23}}))
		})

		It("recognises a generated option and returns raw for others", func() {
			rec := do(http.MethodPost, "/api/llama-cpp/tensor-placement", jsonBody(map[string]string{"option": `-ot:\.ffn_.*_exps.*=CPU`}))
			var got LlamaCppPlacementResponse
			Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
			Expect(got.Raw).To(BeFalse())
			Expect(got.Placement.Mode).To(Equal(llamacppsettings.ExpertsCPUAll))

			rec = do(http.MethodPost, "/api/llama-cpp/tensor-placement", jsonBody(map[string]string{"option": `-ot:blk\.3\.ffn_.*=CUDA0`}))
			got = LlamaCppPlacementResponse{}
			Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
			Expect(got.Raw).To(BeTrue())
			Expect(got.Placement).To(BeNil())
		})

		It("rejects bad input", func() {
			for _, body := range []string{
				`{}`,
				`{"placement":{"mode":"experts_cpu_layers"},"layers":"9-3"}`,
				`{"placement":{"mode":"nope"}}`,
				`{"placement":{"mode":"first_layers_gpu","first_gpu_layers":5000}}`,
				`{"placement":{"mode":"experts_cpu_all"},"option":"-ot:x=CPU"}`,
			} {
				Expect(do(http.MethodPost, "/api/llama-cpp/tensor-placement", body).Code).To(Equal(http.StatusBadRequest), body)
			}
		})
	})
})
