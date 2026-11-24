// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"

	"github.com/mudler/LocalAI/addons/pikvm/internal/kvmd"
	"github.com/mudler/LocalAI/addons/pikvm/internal/settings"
	"github.com/mudler/LocalAI/addons/pikvm/internal/tools"
	"github.com/mudler/LocalAI/addons/pikvm/pikvmtest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/gomega"
)

const (
	devUser = "kvmuser"
	// A password with colons, as the credential format allows.
	devPass = "S3CRET:pass:word-0123456789"
	cred    = devUser + ":" + devPass
)

type admin bool

func (a admin) IsAdmin(context.Context, string) bool { return bool(a) }

type fixture struct {
	srv   *pikvmtest.Server
	store *settings.Store
	rt    *tools.Runtime
	dev   string
}

func newFixture(srv *pikvmtest.Server, dir string) *fixture {
	store := settings.Open(dir)
	rt := tools.New(kvmd.New(), store).WithDataDir(dir)
	rt.Admin = admin(true)
	return &fixture{srv: srv, store: store, rt: rt, dev: srv.URL}
}

// allow switches the per-device settings on.
func (f *fixture) allow(hard, typing bool) {
	d, _ := kvmd.ParseDevice(f.dev)
	_, err := f.store.Set(d.Key(), settings.Device{AllowHardPower: hard, AllowTyping: typing, MaxTypeChars: 200})
	Expect(err).NotTo(HaveOccurred())
}

func (f *fixture) env(op, tool string, args map[string]any) contract.Envelope {
	if args == nil {
		args = map[string]any{}
	}
	args["tool"], args["device"] = tool, f.dev
	raw, _ := json.Marshal(args)
	return contract.Envelope{Request: contract.Request{AddonID: "pikvm", OperationID: op, Arguments: raw, Credential: cred, ResourceKey: f.dev}}
}

type reply struct {
	Source    string         `json:"source"`
	Untrusted bool           `json:"untrusted"`
	Notice    string         `json:"notice"`
	Data      map[string]any `json:"data"`
}

// noLeak fails when the credential, the serial or the host name of the device appears.
func noLeak(b []byte) {
	for _, leak := range []string{devPass, "S3CRET", "SERIAL-NUMBER-0001", "secret-hostname"} {
		Expect(string(b)).NotTo(ContainSubstring(leak))
	}
}

func (f *fixture) read(tool string) (reply, error) {
	res, err := f.rt.Read(context.Background(), f.env("op-read", tool, nil))
	if err != nil {
		return reply{}, err
	}
	Expect(len(res.Payload)).To(BeNumerically("<=", tools.MaxPayload))
	noLeak(res.Payload)
	var r reply
	Expect(json.Unmarshal(res.Payload, &r)).To(Succeed())
	return r, nil
}
