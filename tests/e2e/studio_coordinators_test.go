package e2e_test

import (
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Journeys of the coordinator agents through the whole server, with the mock
// model. The scripted journeys with helpers, contacts and a messaging bot are
// in core/services/studio (coordinatorjourney_test.go); here the question is
// whether the HTTP surface and the real wiring of the application agree.

func coCreate() string {
	code, out := chatsDo(http.MethodPost, chatsBase()+"/coordinators", agObj{
		"name": "Desk", "model": "mock-model", "page": "board",
	})
	ExpectWithOffset(1, code).To(Equal(http.StatusCreated), string(out))
	got := agDecode(out)
	id := got["id"].(string)
	DeferCleanup(func() { chatsDo(http.MethodDelete, chatsBase()+"/agents/"+id, nil) })
	ExpectWithOffset(1, got["coordinator"]).To(BeTrue())
	ExpectWithOffset(1, got["chat_id"]).NotTo(BeEmpty())
	return id
}

func coAudit(id, kind string) []agObj {
	code, out := chatsDo(http.MethodGet, chatsBase()+"/coordinators/"+id+"/audit?kind="+kind, nil)
	ExpectWithOffset(1, code).To(Equal(http.StatusOK), string(out))
	var rows []agObj
	for _, e := range agDecode(out)["entries"].([]any) {
		rows = append(rows, e.(agObj))
	}
	return rows
}

var _ = Describe("Coordinators over the API", func() {
	It("creates a coordinator, pauses it as admin, holds a message, resumes, sees the turn run and the audit rows pause and resume", func() {
		id := coCreate()
		code, out := chatsDo(http.MethodGet, chatsBase()+"/coordinators/"+id, nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		chat := agDecode(out)["chat_id"].(string)

		code, out = chatsDo(http.MethodPost, chatsBase()+"/admin/coordinators/"+id+"/pause", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(agDecode(out)["paused"]).To(BeTrue())

		code, out = chatsDo(http.MethodPost, chatsBase()+"/"+chat+"/messages", agObj{"text": "are you there"})
		Expect(code).To(Equal(http.StatusOK), string(out))
		sent := agDecode(out)
		Expect(sent["started"]).To(BeFalse(), "a paused coordinator runs nothing")
		Expect(sent["pending_id"]).NotTo(BeEmpty(), "the message is held")
		code, out = chatsDo(http.MethodGet, chatsBase()+"/"+chat, nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(agDecode(out)["pending"]).To(HaveLen(1))
		Consistently(func() []chatMessage { return chatsTranscript(chat) }, time.Second, 200*time.Millisecond).Should(BeEmpty())

		code, out = chatsDo(http.MethodPost, chatsBase()+"/admin/coordinators/"+id+"/resume", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(agDecode(out)["paused"]).To(BeFalse())
		Eventually(func() []string { return userTexts(chatsTranscript(chat)) }, 60*time.Second, 200*time.Millisecond).Should(ContainElement("are you there"))
		agWaitIdle(chat)
		code, out = chatsDo(http.MethodGet, chatsBase()+"/"+chat, nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(agDecode(out)["pending"]).To(BeEmpty())

		Expect(coAudit(id, "pause")).To(HaveLen(1))
		Expect(coAudit(id, "resume")).To(HaveLen(1))
	})

	It("shows coordinator true in the agents list and the admin list, and answers 404 for an unknown coordinator", func() {
		id := coCreate()
		code, out := chatsDo(http.MethodGet, chatsBase()+"/agents", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		var found bool
		for _, a := range agDecode(out)["agents"].([]any) {
			if a.(agObj)["id"] == id {
				found = true
				Expect(a.(agObj)["coordinator"]).To(BeTrue())
			}
		}
		Expect(found).To(BeTrue(), string(out))
		code, out = chatsDo(http.MethodGet, chatsBase()+"/admin/coordinators", nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		Expect(string(out)).To(ContainSubstring(id))
		code, _ = chatsDo(http.MethodGet, chatsBase()+"/coordinators/nothing", nil)
		Expect(code).To(Equal(http.StatusNotFound))
		code, _ = chatsDo(http.MethodPost, chatsBase()+"/coordinators/nothing/pause", nil)
		Expect(code).To(Equal(http.StatusNotFound))
	})
})
