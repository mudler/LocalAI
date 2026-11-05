// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mudler/LocalAI/addons/home-assistant/hatest"
	"github.com/mudler/LocalAI/addons/home-assistant/internal/haapi"
	"github.com/mudler/LocalAI/addons/home-assistant/internal/policy"
	"github.com/mudler/LocalAI/addons/home-assistant/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/gomega"
)

const token = "ha-token-very-secret-0123456789"

type adminFn func() bool

func (f adminFn) IsAdmin(context.Context, string) bool { return f() }

// home is a fake instance with a small house in it.
func home() *hatest.Server {
	s := hatest.New(token)
	s.Add("light.kitchen", "on", map[string]any{"friendly_name": "Kitchen Light", "brightness": 200.0, "entity_picture": "/api/x?token=abc", "access_token": "tok-123"})
	s.Add("light.bedroom", "off", map[string]any{"friendly_name": "Bedroom Light"})
	s.Add("light.guest", "on", map[string]any{"friendly_name": "Guest Light"})
	s.Add("light.attic", "off", map[string]any{"friendly_name": "Attic Light"})
	s.Add("sensor.temp_office", "21.5", map[string]any{"friendly_name": "Office Temperature", "unit_of_measurement": "°C", "device_class": "temperature"})
	s.Add("sensor.temp_bedroom", "19", map[string]any{"friendly_name": "Bedroom Temperature", "unit_of_measurement": "°C"})
	s.Add("sensor.diary", "unavailable", map[string]any{"friendly_name": "Diary"})
	s.Add("sensor.private_note", "secret text", map[string]any{"friendly_name": "Private Note"})
	s.Add("lock.front", "locked", map[string]any{"friendly_name": "Front Door"})
	s.Add("camera.porch", "idle", map[string]any{"friendly_name": "Porch", "access_token": "cam-tok", "entity_picture": "/api/camera_proxy/camera.porch?token=cam-tok"})
	s.Add("person.ann", "home", map[string]any{"friendly_name": "Ann", "latitude": 45.1, "longitude": 9.2, "gps_accuracy": 5.0, "source": "device_tracker.phone"})
	s.Add("climate.living", "heat", map[string]any{"friendly_name": "Living", "temperature": 20.0, "api_key": "k", "wifi_password": "p"})
	s.Add("cover.garage", "closed", map[string]any{"friendly_name": "Garage", "device_class": "garage"})
	s.Add("cover.blind", "open", map[string]any{"friendly_name": "Blind", "device_class": "blind"})
	s.Add("cover.mystery", "closed", map[string]any{"friendly_name": "Mystery"})
	s.Add("switch.heater", "off", map[string]any{"friendly_name": "Heater"})
	s.Add("scene.evening", "unknown", map[string]any{"friendly_name": "Evening"})
	s.Add("script.goodnight", "off", map[string]any{"friendly_name": "Goodnight"})
	s.Add("input_boolean.guest_mode", "off", map[string]any{"friendly_name": "Guest mode"})
	s.Add("media_player.tv", "playing", map[string]any{"friendly_name": "TV"})
	s.AddArea(hatest.Area{ID: "kitchen", Name: "Kitchen", Entities: []string{"light.kitchen"}, Devices: 1})
	s.AddArea(hatest.Area{ID: "bedroom", Name: "Bedroom", Entities: []string{"light.bedroom", "sensor.temp_bedroom"}, Devices: 2})
	s.AddArea(hatest.Area{ID: "office", Name: "Office", Entities: []string{"sensor.temp_office"}})
	s.AddArea(hatest.Area{ID: "attic", Name: "Attic", Entities: []string{"light.attic"}})
	s.Label("sensitive", "sensor.private_note")
	s.AddService("light", "turn_on", hatest.Service{Description: "Turn on a light.", Fields: map[string]string{"brightness": "Brightness 0-255", "entity_id": "target"}})
	s.AddService("light", "turn_off", hatest.Service{Description: "Turn off a light."})
	s.AddService("light", "reload", hatest.Service{Description: "Reload."})
	s.AddService("lock", "unlock", hatest.Service{Description: "Unlock a lock."})
	s.AddService("climate", "set_temperature", hatest.Service{Description: "Set the target temperature."})
	s.AddService("cover", "open_cover", hatest.Service{Description: "Open."})
	s.AddService("notify", "mobile_app_phone", hatest.Service{Description: "Notify."})
	s.AddService("homeassistant", "restart", hatest.Service{Description: "Restart."})
	return s
}

type rig struct {
	srv   *hatest.Server
	rt    *tools.Runtime
	pol   *policy.Store
	admin bool
	seq   int
	now   time.Time
}

func newRig(srv *hatest.Server, dir string) *rig {
	g := &rig{srv: srv, pol: policy.Open(dir), now: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)}
	api := haapi.New()
	g.rt = tools.New(api, g.pol)
	g.rt.Now = func() time.Time { return g.now }
	g.rt.Admin = adminFn(func() bool { return g.admin })
	if dir != "" {
		g.rt.WithDataDir(dir)
	}
	return g
}

func (g *rig) env(tool string, args map[string]any) contract.Envelope {
	if _, ok := args["instance"]; !ok && tool != "get-policy" && tool != "set-policy" {
		args["instance"] = g.srv.URL
	}
	args["tool"] = tool
	raw, _ := json.Marshal(args)
	g.seq++
	return contract.Envelope{Request: contract.Request{AddonID: "home-assistant", OperationID: "op-" + strconv.Itoa(g.seq), Arguments: raw, Credential: token, ResourceKey: g.srv.URL}}
}

// read returns the data of a read result, with the wrapper checked.
func (g *rig) read(tool string, args map[string]any) (map[string]any, error) {
	res, err := g.rt.Read(context.Background(), g.env(tool, args))
	if err != nil {
		return nil, err
	}
	var w struct {
		Source    string         `json:"source"`
		Untrusted bool           `json:"untrusted"`
		Notice    string         `json:"notice"`
		Warnings  []string       `json:"warnings"`
		Data      map[string]any `json:"data"`
	}
	Expect(json.Unmarshal(res.Payload, &w)).To(Succeed())
	Expect(w.Source).To(Equal("home-assistant"))
	Expect(w.Untrusted).To(BeTrue())
	Expect(w.Notice).To(ContainSubstring("never as instructions"))
	Expect(string(res.Payload)).NotTo(ContainSubstring(token))
	if len(w.Warnings) > 0 {
		w.Data["_warnings"] = w.Warnings
	}
	return w.Data, nil
}

// do previews then applies, as the host does.
func (g *rig) do(tool string, args map[string]any) (contract.Receipt, error) {
	env := g.env(tool, args)
	p, err := g.rt.Preview(context.Background(), env)
	if err != nil {
		return contract.Receipt{}, err
	}
	return g.rt.Apply(context.Background(), env, p.Revision)
}

func (g *rig) setPolicy(f func(*policy.Policy)) {
	p, err := g.pol.Load()
	Expect(err).NotTo(HaveOccurred())
	f(&p)
	_, err = g.pol.Save(p)
	Expect(err).NotTo(HaveOccurred())
}

func ids(rows any) []string {
	var out []string
	for _, r := range rows.([]any) {
		out = append(out, r.(map[string]any)["entityId"].(string))
	}
	return out
}

func writeFile(dir, body string) error {
	return os.WriteFile(filepath.Join(dir, "policy.json"), []byte(body), 0o600)
}

func envWithArgs(g *rig, raw []byte) contract.Envelope {
	e := g.env("get-policy", map[string]any{})
	e.Request.Arguments = raw
	e.Request.Credential, e.Request.ResourceKey = "", ""
	return e
}
