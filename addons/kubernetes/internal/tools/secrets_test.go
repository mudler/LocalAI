// SPDX-License-Identifier: MIT
package tools_test

import (
	"encoding/json"
	"net/http"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

const secretManifest = `apiVersion: v1
kind: Secret
metadata:
  name: db
  namespace: team-a
stringData:
  password: secret-value
data:
  token: c2VjcmV0LXZhbHVl
`

// clean fails when a Secret value appears in a text.
func clean(what string, b []byte) {
	for _, leak := range []string{"secret-value", "c2VjcmV0LXZhbHVl", "inline-token-value"} {
		Expect(string(b)).NotTo(ContainSubstring(leak), what)
	}
}

var _ = Describe("Secret values", func() {
	It("hides Secret values in every tool that can return them", func() {
		g := newRig(secret("team-a", "db"), deployment("team-a", "web", 1))
		for _, kind := range []string{"secret", "Secret", "secrets", "v1/Secret", "secrets.v1."} {
			for _, tool := range []string{tools.ToolGet, tools.ToolDescribe, tools.ToolList} {
				args := map[string]any{"kind": kind, "namespace": "team-a"}
				if tool != tools.ToolList {
					args["name"] = "db"
				}
				res, err := g.rt.Read(ctx, g.env("op", tool, args))
				Expect(err).NotTo(HaveOccurred(), kind+" "+tool)
				clean(kind+" "+tool, res.Payload)
			}
			p, err := g.preview("op-p-"+kind, tools.ToolPatch, map[string]any{"kind": kind, "namespace": "team-a", "name": "db", "patchType": "merge", "patch": `{"metadata":{"labels":{"a":"b"}}}`})
			Expect(err).NotTo(HaveOccurred(), kind)
			clean(kind+" patch preview", p.Summary)
		}
		out := g.mustRead(tools.ToolGet, map[string]any{"kind": "secret", "namespace": "team-a", "name": "db"})
		Expect(out["yaml"]).To(ContainSubstring("password: <12 bytes>"))

		res, err := g.rt.Read(ctx, g.env("op", tools.ToolGet, map[string]any{"kind": "deploy", "namespace": "team-a", "name": "web"}))
		Expect(err).NotTo(HaveOccurred())
		clean("deployment", res.Payload)
		Expect(string(res.Payload)).To(ContainSubstring("secretKeyRef"))

		for _, op := range []string{"apply-1", "apply-2"} {
			p, err := g.preview(op, tools.ToolApply, map[string]any{"manifest": secretManifest})
			Expect(err).NotTo(HaveOccurred())
			clean("apply preview", p.Summary)
			rc, err := g.apply(op, tools.ToolApply, map[string]any{"manifest": secretManifest}, p)
			Expect(err).NotTo(HaveOccurred())
			clean("apply result", rc.Result.Payload)
		}
		args := map[string]any{"kind": "secrets", "namespace": "team-a", "name": "db", "patchType": "merge", "patch": `{"data":{"token":"c2VjcmV0LXZhbHVl","more":"c2VjcmV0LXZhbHVl"}}`}
		p, err := g.preview("patch", tools.ToolPatch, args)
		Expect(err).NotTo(HaveOccurred())
		clean("patch preview", p.Summary)
		Expect(sampleOf(p)["diff"]).To(ContainSubstring("(new value)"))
		rc, err := g.apply("patch", tools.ToolPatch, args, p)
		Expect(err).NotTo(HaveOccurred())
		clean("patch result", rc.Result.Payload)
	})

	It("shows them when the policy allows it", func() {
		g := newRig(secret("team-a", "db"))
		g.setPolicy(policy.Policy{Namespaces: []string{"*"}, ReadSecretValues: true})
		out := g.mustRead(tools.ToolGet, map[string]any{"kind": "secret", "namespace": "team-a", "name": "db"})
		b, _ := json.Marshal(out)
		Expect(string(b)).To(ContainSubstring("c2VjcmV0LXZhbHVl"))
	})

	It("refuses a json patch of a Secret that copies, moves or tests a value", func() {
		g := newRig(secret("team-a", "db"))
		for name, patch := range map[string]string{
			"copy to annotation": `[{"op":"copy","from":"/data/password","path":"/metadata/annotations/leak"}]`,
			"move to annotation": `[{"op":"move","from":"/data/password","path":"/metadata/annotations/leak"}]`,
			"copy to label":      `[{"op":"copy","from":"/data/password","path":"/metadata/labels/leak"}]`,
			"copy stringData":    `[{"op":"copy","from":"/stringData/x","path":"/metadata/annotations/leak"}]`,
			"copy of the root":   `[{"op":"copy","from":"","path":"/metadata/annotations/leak"}]`,
			"test right value":   `[{"op":"test","path":"/data/password","value":"c2VjcmV0LXZhbHVl"}]`,
			"test wrong value":   `[{"op":"test","path":"/data/password","value":"d3Jvbmc="}]`,
			"test of metadata":   `[{"op":"test","path":"/metadata/name","value":"db"}]`,
			"copy of metadata":   `[{"op":"copy","from":"/metadata/name","path":"/metadata/annotations/n"}]`,
			"replace a value":    `[{"op":"replace","path":"/data/password","value":"eA=="}]`,
			"upper-case keys":    `[{"OP":"COPY","FROM":"/data/password","PATH":"/metadata/annotations/leak"}]`,
			"escaped pointer":    `[{"op":"add","path":"/dat\u0061/x","value":"eA=="}]`,
		} {
			args := map[string]any{"kind": "secrets", "namespace": "team-a", "name": "db", "patchType": "json", "patch": patch}
			_, err := g.preview("op-"+name, tools.ToolPatch, args)
			Expect(err).To(MatchError(ContainSubstring("(readSecretValues)")), name)
			clean(name, []byte(err.Error()))
			_, err = g.apply("op-"+name, tools.ToolPatch, args, contract.Preview{})
			Expect(err).To(MatchError(ContainSubstring("(readSecretValues)")), name)
		}
		Expect(g.k.Writes()).To(BeZero())

		ok := map[string]any{"kind": "secrets", "namespace": "team-a", "name": "db", "patchType": "json",
			"patch": `[{"op":"add","path":"/metadata/labels","value":{"team":"a"}}]`}
		_, err := g.preview("op-label", tools.ToolPatch, ok)
		Expect(err).NotTo(HaveOccurred(), "a json patch of the metadata stays possible")
	})

	It("allows a json patch that copies a Secret value when the policy shows values", func() {
		g := newRig(secret("team-a", "db"))
		g.setPolicy(policy.Policy{Namespaces: []string{"*"}, WriteTools: policy.Defaults().WriteTools, ReadSecretValues: true})
		args := map[string]any{"kind": "secrets", "namespace": "team-a", "name": "db", "patchType": "json",
			"patch": `[{"op":"copy","from":"/data/password","path":"/metadata/annotations/copy"}]`}
		_, err := g.preview("op", tools.ToolPatch, args)
		Expect(err).NotTo(HaveOccurred())
	})

	It("masks the annotations of a Secret, so a value put there earlier stays hidden", func() {
		s := secret("team-a", "db")
		s.Annotations["leak"] = "secret-value"
		g := newRig(s)
		for _, tool := range []string{tools.ToolGet, tools.ToolDescribe} {
			res, err := g.rt.Read(ctx, g.env("op", tool, map[string]any{"kind": "secret", "namespace": "team-a", "name": "db"}))
			Expect(err).NotTo(HaveOccurred())
			clean(tool, res.Payload)
		}
		args := map[string]any{"kind": "secrets", "namespace": "team-a", "name": "db", "patchType": "merge",
			"patch": `{"metadata":{"annotations":{"leak":"other-value"}}}`}
		p, err := g.preview("op-ann", tools.ToolPatch, args)
		Expect(err).NotTo(HaveOccurred())
		clean("annotation diff", p.Summary)
		Expect(string(p.Summary)).NotTo(ContainSubstring("other-value"))
		Expect(sampleOf(p)["diff"]).To(ContainSubstring("(new value)"))
	})

	It("keeps only the code and the reason of an API error about a Secret", func() {
		g := newRig(secret("team-a", "db"), deployment("team-a", "web", 1))
		echo := func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetResource().Resource != "secrets" {
				return false, nil, nil
			}
			return true, nil, &apierrors.StatusError{ErrStatus: metav1.Status{Status: metav1.StatusFailure, Code: http.StatusUnprocessableEntity,
				Reason: metav1.StatusReasonInvalid, Message: `Secret "db" is invalid: data[password]: Invalid value: "secret-value"`}}
		}
		g.k.Prepend("get", "secrets", echo)
		args := map[string]any{"kind": "secrets", "namespace": "team-a", "name": "db", "patchType": "merge", "patch": `{"metadata":{"labels":{"a":"b"}}}`}
		_, err := g.preview("op", tools.ToolPatch, args)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("422 Invalid"))
		clean("secret error", []byte(err.Error()))
	})
})
