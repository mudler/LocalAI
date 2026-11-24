// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"

	"github.com/mudler/LocalAI/addons/ssh/internal/sshclient"
	"github.com/mudler/LocalAI/addons/ssh/internal/tools"
	"github.com/mudler/LocalAI/addons/ssh/sshtest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	user = "deploy"
	// A password with colons, as the credential format allows.
	pass = "S3CRET:pass:word-0123456789"
)

var ctx = context.Background()

type fixture struct {
	srv  *sshtest.Server
	rt   *tools.Runtime
	host string // the address as listed on the connection, with its pin
	cred string
}

// newFixture starts a server that takes the password and pins its host key.
func newFixture(opts sshtest.Options) *fixture {
	opts.User, opts.Password = user, pass
	srv := sshtest.New(opts)
	f := &fixture{srv: srv, rt: tools.New().WithDataDir(GinkgoT().TempDir()), cred: user + ":" + pass}
	f.host = f.addr(sshclient.PinHex(srv.HostKey()))
	return f
}

func (f *fixture) addr(pin string) string {
	a := user + "@" + f.srv.Address()
	if pin != "" {
		a += "#sha256=" + pin
	}
	return a
}

func (f *fixture) env(op, tool string, args map[string]any) contract.Envelope {
	if args == nil {
		args = map[string]any{}
	}
	args["tool"] = tool
	if _, ok := args["host"]; !ok && tool != tools.ToolScanHostKey {
		args["host"] = f.host
	}
	raw, _ := json.Marshal(args)
	return contract.Envelope{Request: contract.Request{AddonID: "ssh", OperationID: op, Arguments: raw, Credential: f.cred, ResourceKey: f.host}}
}

type reply struct {
	Source    string         `json:"source"`
	Untrusted bool           `json:"untrusted"`
	Notice    string         `json:"notice"`
	Data      map[string]any `json:"data"`
}

// noLeak fails when the secret appears in a payload.
func noLeak(b []byte) {
	for _, leak := range []string{pass, "S3CRET"} {
		Expect(string(b)).NotTo(ContainSubstring(leak))
	}
}

func (f *fixture) read(tool string, args map[string]any) (reply, error) {
	res, err := f.rt.Read(ctx, f.env("op-read", tool, args))
	if err != nil {
		return reply{}, err
	}
	Expect(len(res.Payload)).To(BeNumerically("<=", tools.MaxPayload))
	noLeak(res.Payload)
	var r reply
	Expect(json.Unmarshal(res.Payload, &r)).To(Succeed())
	return r, nil
}

func (f *fixture) preview(op string, args map[string]any) (contract.Preview, error) {
	return f.rt.Preview(ctx, f.env(op, tools.ToolRun, args))
}

func (f *fixture) apply(op string, args map[string]any, p contract.Preview) (contract.Receipt, error) {
	return f.rt.Apply(ctx, f.env(op, tools.ToolRun, args), p.Revision)
}

// run previews and applies a command, as the host does for a confirmed one.
func (f *fixture) run(op string, args map[string]any) (contract.Receipt, reply) {
	p, err := f.preview(op, args)
	Expect(err).NotTo(HaveOccurred())
	rc, err := f.apply(op, args, p)
	Expect(err).NotTo(HaveOccurred())
	var r reply
	if len(rc.Result.Payload) > 0 {
		Expect(json.Unmarshal(rc.Result.Payload, &r)).To(Succeed())
		noLeak(rc.Result.Payload)
	}
	return rc, r
}

func sampleOf(p contract.Preview) map[string]any {
	var s struct {
		Sample map[string]any `json:"sample"`
	}
	Expect(json.Unmarshal(p.Summary, &s)).To(Succeed())
	return s.Sample
}
