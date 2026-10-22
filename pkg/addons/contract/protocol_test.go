// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"reflect"
	"strings"
	"time"
)

func envelope() Envelope {
	return Envelope{Request: Request{AddonID: "sample", ResourceID: "resource-1", OperationID: "operation-1", Generation: 1, Action: "query", Arguments: json.RawMessage(`{"limit":1}`)}, Authority: Authority{Actor: Actor{UserID: "user-1", CredentialID: "key-1", Mode: "named-user"}, Scope: "read", ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}}
}

var _ = Describe("Protocol", func() {
	It("uses canonical version one wire fixtures", func() {
		b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/envelope.json")
		Expect(e).NotTo(HaveOccurred())
		var v Envelope
		Expect(v.UnmarshalJSON(b)).To(Succeed())
		Expect(v).To(Equal(envelope()))
		out, e := json.Marshal(v)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(out)).To(Equal(strings.TrimSpace(string(b))))
		for _, v := range []any{Preview{OperationID: "operation-1", Revision: Revision{Schema: 1, Data: 2}, ExpiresAt: envelope().Authority.ExpiresAt, Summary: json.RawMessage(`{"count":1}`)}, Receipt{OperationID: "operation-1", State: ReceiptCommitted, Revision: Revision{Schema: 1, Data: 3}, Result: Result{Payload: json.RawMessage(`{"count":1}`), Outputs: []OutputDescription{{Handle: "output-1", Name: "export.csv", MediaType: "text/csv", Size: 10}}}}} {
			b, e := json.Marshal(v)
			Expect(e).NotTo(HaveOccurred())
			copy := reflect.New(reflect.TypeOf(v))
			Expect(json.Unmarshal(b, copy.Interface())).To(Succeed())
			Expect(copy.Elem().Interface()).To(Equal(v))
		}
	})
	It("rejects incomplete expired or forged authority shapes", func() {
		now := time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)
		for _, change := range []func(*Envelope){func(v *Envelope) { v.Authority.Actor.UserID = "" }, func(v *Envelope) { v.Authority.Actor.CredentialID = "" }, func(v *Envelope) { v.Authority.Actor.Mode = "anonymous" }, func(v *Envelope) { v.Authority.Scope = "admin" }, func(v *Envelope) { v.Authority.ExpiresAt = now }, func(v *Envelope) { v.Request.OperationID = "" }, func(v *Envelope) { v.Request.Generation = 0 }, func(v *Envelope) { v.Request.AddonID = "../x" }, func(v *Envelope) { v.Request.ResourceID = "/x" }, func(v *Envelope) { v.Request.Arguments = json.RawMessage(`null`) }} {
			v := envelope()
			change(&v)
			Expect(v.Validate(now, DefaultLimits())).NotTo(Succeed())
		}
		Expect(envelope().Validate(now, DefaultLimits())).To(Succeed())
		b, _ := json.Marshal(envelope())
		b = []byte(strings.Replace(string(b), `"scope":"read"`, `"scope":"read","approved":true`, 1))
		var v Envelope
		Expect(json.Unmarshal(b, &v)).NotTo(Succeed())
	})
	It("rejects unknown and oversized wire payloads", func() {
		for _, target := range []any{&Preview{}, &Receipt{}, &Result{}} {
			Expect(json.Unmarshal([]byte(`{"unexpected":true}`), target)).NotTo(Succeed())
		}

		v := envelope()
		v.Request.Arguments = json.RawMessage(`{"x":"` + strings.Repeat("x", int(DefaultLimits().PayloadBytes)) + `"}`)
		Expect(v.Validate(time.Now(), DefaultLimits())).NotTo(Succeed())
		b, _ := json.Marshal(v)
		var other Envelope
		Expect(json.Unmarshal(b, &other)).NotTo(Succeed())
		for _, s := range []string{`null`, `{}`, `{"request":{},"request":{}}`, `{"authority":{"actor":{"unexpected":true}}}`} {
			Expect(json.Unmarshal([]byte(s), &other)).NotTo(Succeed())
		}
	})
	It("validates finite defaults overrides and cross field bounds", func() {
		l := DefaultLimits()
		Expect(l.Validate()).To(Succeed())
		rv := reflect.ValueOf(&l).Elem()
		for i := 0; i < rv.NumField(); i++ {
			old := rv.Field(i).Int()
			rv.Field(i).SetInt(0)
			Expect(l.Validate()).NotTo(Succeed(), rv.Type().Field(i).Name)
			rv.Field(i).SetInt(-1)
			Expect(l.Validate()).NotTo(Succeed())
			rv.Field(i).SetInt(old)
		}
		l.QueryRows = 50
		l.Concurrency = 2
		Expect(l.Validate()).To(Succeed())
		l.BackoffInitial = l.BackoffMax + 1
		Expect(l.Validate()).NotTo(Succeed())
		l = DefaultLimits()
		l.ArtifactBytes = l.ArtifactQuotaBytes + 1
		Expect(l.Validate()).NotTo(Succeed())
		l = DefaultLimits()
		l.PayloadBytes = l.EnvelopeBytes + 1
		Expect(l.Validate()).NotTo(Succeed())
	})
	It("validates preview receipt states and output descriptions", func() {
		p := Preview{OperationID: "operation-1", Revision: Revision{1, 1}, ExpiresAt: envelope().Authority.ExpiresAt, Summary: json.RawMessage(`{}`)}
		Expect(p.Validate(time.Now(), DefaultLimits())).To(Succeed())
		p.OperationID = ""
		Expect(p.Validate(time.Now(), DefaultLimits())).NotTo(Succeed())
		for _, s := range []ReceiptState{ReceiptPending, ReceiptCommitted, ReceiptRejected, ReceiptUnknown} {
			r := Receipt{OperationID: "operation-1", State: s, Revision: Revision{1, 1}}
			Expect(r.Validate(DefaultLimits())).To(Succeed())
		}
		r := Receipt{OperationID: "operation-1", State: "success"}
		Expect(r.Validate(DefaultLimits())).NotTo(Succeed())
		for _, o := range []OutputDescription{{Handle: "../file", Name: "x", MediaType: "text/csv", Size: 1}, {Handle: "output", Name: "../x", MediaType: "text/csv", Size: 1}, {Handle: "output", Name: "x", MediaType: "bad", Size: 1}, {Handle: "output", Name: "x", MediaType: "text/csv", Size: -1}} {
			Expect((Result{Outputs: []OutputDescription{o}}).Validate(DefaultLimits())).NotTo(Succeed())
		}
	})
})
