package localai_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("API Instructions Endpoints", func() {
	var app *echo.Echo

	BeforeEach(func() {
		app = echo.New()
		app.GET("/api/instructions", ListAPIInstructionsEndpoint())
		app.GET("/api/instructions/:name", GetAPIInstructionEndpoint())
	})

	Context("GET /api/instructions", func() {
		It("should return all instruction definitions", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/instructions", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp map[string]any
			err := json.Unmarshal(rec.Body.Bytes(), &resp)
			Expect(err).NotTo(HaveOccurred())

			Expect(resp).To(HaveKey("hint"))
			Expect(resp).To(HaveKey("instructions"))

			instructions, ok := resp["instructions"].([]any)
			Expect(ok).To(BeTrue())
			Expect(instructions).To(HaveLen(9))

			// Verify each instruction has required fields and correct URL format
			for _, s := range instructions {
				inst, ok := s.(map[string]any)
				Expect(ok).To(BeTrue())
				Expect(inst["name"]).NotTo(BeEmpty())
				Expect(inst["description"]).NotTo(BeEmpty())
				Expect(inst["tags"]).NotTo(BeNil())
				Expect(inst["url"]).To(HavePrefix("/api/instructions/"))
				Expect(inst["url"]).To(Equal("/api/instructions/" + inst["name"].(string)))
			}
		})

		It("should include known instruction names", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/instructions", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())

			instructions := resp["instructions"].([]any)
			names := make([]string, len(instructions))
			for i, s := range instructions {
				names[i] = s.(map[string]any)["name"].(string)
			}

			Expect(names).To(ContainElements(
				"chat-inference",
				"config-management",
				"model-management",
				"monitoring",
				"agents",
			))
		})
	})

	Context("GET /api/instructions/:name", func() {
		It("should return 404 for unknown instruction", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/instructions/nonexistent", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusNotFound))

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp["error"]).To(ContainSubstring("instruction not found"))
		})

		It("should return markdown by default", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/instructions/chat-inference", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Header().Get("Content-Type")).To(ContainSubstring("text/markdown"))

			body, err := io.ReadAll(rec.Body)
			Expect(err).NotTo(HaveOccurred())
			md := string(body)

			Expect(md).To(HavePrefix("# chat-inference"))
			// Should contain at least one endpoint heading
			Expect(md).To(MatchRegexp(`## (GET|POST|PUT|PATCH|DELETE) /`))
		})

		It("should include intro text for instructions that have one", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/instructions/chat-inference", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			body, _ := io.ReadAll(rec.Body)
			// chat-inference has an intro about streaming
			Expect(string(body)).To(ContainSubstring("stream"))
		})

		It("should return JSON fragment when format=json", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/instructions/chat-inference?format=json", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp["name"]).To(Equal("chat-inference"))
			Expect(resp["tags"]).To(ContainElements("inference", "embeddings"))

			fragment, ok := resp["swagger_fragment"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(fragment).To(HaveKey("paths"))

			paths, ok := fragment["paths"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(paths).NotTo(BeEmpty())
		})

		It("should include referenced definitions in JSON fragment", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/instructions/chat-inference?format=json", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())

			fragment := resp["swagger_fragment"].(map[string]any)
			Expect(fragment).To(HaveKey("definitions"))

			defs, ok := fragment["definitions"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(defs).NotTo(BeEmpty())
		})

		It("should only include paths matching the instruction tags in JSON fragment", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/instructions/config-management?format=json", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())

			fragment := resp["swagger_fragment"].(map[string]any)
			paths := fragment["paths"].(map[string]any)
			Expect(paths).NotTo(BeEmpty())

			// Every operation in every path should have the "config" tag
			for _, methods := range paths {
				methodMap := methods.(map[string]any)
				for _, opRaw := range methodMap {
					op := opRaw.(map[string]any)
					tags, _ := op["tags"].([]any)
					tagStrs := make([]string, len(tags))
					for i, t := range tags {
						tagStrs[i] = t.(string)
					}
					Expect(tagStrs).To(ContainElement("config"))
				}
			}
		})

		It("should produce stable output across calls", func() {
			req1 := httptest.NewRequest(http.MethodGet, "/api/instructions/chat-inference", nil)
			rec1 := httptest.NewRecorder()
			app.ServeHTTP(rec1, req1)

			req2 := httptest.NewRequest(http.MethodGet, "/api/instructions/chat-inference", nil)
			rec2 := httptest.NewRecorder()
			app.ServeHTTP(rec2, req2)

			body1, _ := io.ReadAll(rec1.Body)
			body2, _ := io.ReadAll(rec2.Body)
			Expect(string(body1)).To(Equal(string(body2)))
		})

		It("should return markdown for every defined instruction", func() {
			// First get the list
			listReq := httptest.NewRequest(http.MethodGet, "/api/instructions", nil)
			listRec := httptest.NewRecorder()
			app.ServeHTTP(listRec, listReq)

			var listResp map[string]any
			Expect(json.Unmarshal(listRec.Body.Bytes(), &listResp)).To(Succeed())

			instructions := listResp["instructions"].([]any)
			for _, s := range instructions {
				name := s.(map[string]any)["name"].(string)
				req := httptest.NewRequest(http.MethodGet, "/api/instructions/"+name, nil)
				rec := httptest.NewRecorder()
				app.ServeHTTP(rec, req)

				Expect(rec.Code).To(Equal(http.StatusOK),
					"instruction %q should return 200", name)
				body, _ := io.ReadAll(rec.Body)
				Expect(strings.TrimSpace(string(body))).NotTo(BeEmpty(),
					"instruction %q should return non-empty markdown", name)
			}
		})
	})
})
