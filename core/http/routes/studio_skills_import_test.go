// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"archive/zip"
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type zipFile struct{ name, body string }

func zipOf(files ...zipFile) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.Create(f.name)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Write([]byte(f.body))
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(zw.Close()).To(Succeed())
	return buf.Bytes()
}

func skillText(name string) string {
	return "---\nname: " + name + "\ndescription: Summarize a report. Use when asked.\nlicense: MIT\n---\nRead it.\n"
}

var _ = Describe("Skill import endpoint", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	upload := func(user, layer string, data []byte) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		if layer != "" {
			Expect(mw.WriteField("layer", layer)).To(Succeed())
		}
		fw, err := mw.CreateFormFile("file", "skills.zip")
		Expect(err).NotTo(HaveOccurred())
		_, err = fw.Write(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(mw.Close()).To(Succeed())
		req := httptest.NewRequest(http.MethodPost, "/api/chats/skills/import", &buf)
		req.Header.Set(echo.HeaderContentType, mw.FormDataContentType())
		if user != "" {
			req.AddCookie(&http.Cookie{Name: "session", Value: s.sessions[user]})
		}
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		return rec
	}

	It("requires authentication", func() {
		Expect(upload("", "", zipOf(zipFile{"SKILL.md", skillText("a")})).Code).To(Equal(http.StatusUnauthorized))
	})

	It("imports a skill, keeps its files, and lists it for the owner only", func() {
		rec := upload("alice", "", zipOf(
			zipFile{"summarize/SKILL.md", skillText("summarize")},
			zipFile{"summarize/scripts/run.sh", "echo hi"},
			zipFile{"summarize/references/guide.md", "Guide."},
		))
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		res := decodeJSON(rec)
		item := res["skills"].([]any)[0].(map[string]any)
		Expect(item).To(And(HaveKeyWithValue("name", "summarize"), HaveKeyWithValue("files", BeNumerically("==", 3)), HaveKeyWithValue("scripts", BeNumerically("==", 1))))

		list := skillNames(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/skills", nil)))
		Expect(list["summarize"]).To(And(HaveKeyWithValue("layer", "personal"), HaveKeyWithValue("description", "Summarize a report. Use when asked.")))
		Expect(list["summarize"]).NotTo(HaveKey("invalid"))
		Expect(skillNames(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/skills", nil)))).NotTo(HaveKey("summarize"))
	})

	It("imports several skills from one archive", func() {
		rec := upload("alice", "personal", zipOf(zipFile{"a/SKILL.md", skillText("a")}, zipFile{"b/SKILL.md", skillText("b")}))
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		list := skillNames(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/skills", nil)))
		Expect(list).To(And(HaveKey("a"), HaveKey("b")))
	})

	It("refuses a name that exists, and stores nothing of the archive", func() {
		Expect(upload("alice", "", zipOf(zipFile{"SKILL.md", skillText("taken")})).Code).To(Equal(http.StatusCreated))
		rec := upload("alice", "", zipOf(zipFile{"fresh/SKILL.md", skillText("fresh")}, zipFile{"taken/SKILL.md", skillText("taken")}))
		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("taken"))
		Expect(skillNames(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/skills", nil)))).NotTo(HaveKey("fresh"))
	})

	It("allows the name again after the skill was deleted", func() {
		Expect(upload("alice", "", zipOf(zipFile{"SKILL.md", skillText("again")})).Code).To(Equal(http.StatusCreated))
		Expect(s.do("alice", http.MethodDelete, "/api/chats/skills/personal/again", nil).Code).To(Equal(http.StatusOK))
		Expect(upload("alice", "", zipOf(zipFile{"SKILL.md", skillText("again")})).Code).To(Equal(http.StatusCreated))
	})

	DescribeTable("refuses an archive that is not safe or not valid",
		func(data []byte, status int, text string) {
			rec := upload("alice", "", data)
			Expect(rec.Code).To(Equal(status), rec.Body.String())
			Expect(rec.Body.String()).To(ContainSubstring(text))
			Expect(skillNames(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/skills", nil)))).NotTo(HaveKey("evil"))
		},
		Entry("path traversal", zipOf(zipFile{"evil/SKILL.md", skillText("evil")}, zipFile{"evil/../../x", "x"}), http.StatusBadRequest, ".."),
		Entry("absolute path", zipOf(zipFile{"/evil/SKILL.md", skillText("evil")}), http.StatusBadRequest, "absolute"),
		Entry("no SKILL.md", zipOf(zipFile{"evil/readme.md", "x"}), http.StatusBadRequest, "no SKILL.md"),
		Entry("bad name", zipOf(zipFile{"SKILL.md", skillText("Evil_Name")}), http.StatusBadRequest, "not valid"),
		Entry("not a zip", []byte("plain text"), http.StatusBadRequest, "zip"),
		Entry("a file that unpacks too large", zipOf(zipFile{"SKILL.md", skillText("evil")}, zipFile{"big", strings.Repeat("\x00", 6<<20)}), http.StatusRequestEntityTooLarge, "larger"),
		Entry("a name that ships with the product", zipOf(zipFile{"SKILL.md", skillText("localai")}), http.StatusBadRequest, "ships with LocalAI"),
	)

	It("lets only an administrator import into the shared layer", func() {
		data := zipOf(zipFile{"SKILL.md", skillText("team-notes")})
		Expect(upload("alice", "shared", data).Code).To(Equal(http.StatusForbidden))
		Expect(upload("root", "shared", data).Code).To(Equal(http.StatusCreated))
		Expect(skillNames(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/skills", nil)))).To(HaveKey("team-notes"))
		Expect(upload("alice", "builtin", data).Code).To(Equal(http.StatusBadRequest))
	})
})
