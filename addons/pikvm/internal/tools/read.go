// SPDX-License-Identifier: MIT
package tools

import "net/url"

// atxState is GET /api/atx.
type atxState struct {
	Enabled bool `json:"enabled"`
	Busy    bool `json:"busy"`
	Leds    struct {
		Power bool `json:"power"`
		HDD   bool `json:"hdd"`
	} `json:"leds"`
}

func (s atxState) power() string {
	if s.Leds.Power {
		return "on"
	}
	return "off"
}

func (s atxState) view() map[string]any {
	return map[string]any{"enabled": s.Enabled, "busy": s.Busy, "power": s.power(), "diskActivity": s.Leds.HDD}
}

func (c *call) atx() (atxState, error) {
	var s atxState
	return s, c.r.API.GetJSON(c.ctx, c.dev, c.cred, []string{"api", "atx"}, nil, &s)
}

// hidState is GET /api/hid.
type hidState struct {
	Enabled  bool `json:"enabled"`
	Online   bool `json:"online"`
	Busy     bool `json:"busy"`
	Keyboard struct {
		Online bool `json:"online"`
		Leds   struct {
			Caps   bool `json:"caps"`
			Num    bool `json:"num"`
			Scroll bool `json:"scroll"`
		} `json:"leds"`
	} `json:"keyboard"`
}

func (s hidState) view() map[string]any {
	return map[string]any{"enabled": s.Enabled, "online": s.Online, "busy": s.Busy, "keyboardOnline": s.Keyboard.Online,
		"leds": map[string]bool{"caps": s.Keyboard.Leds.Caps, "num": s.Keyboard.Leds.Num, "scroll": s.Keyboard.Leds.Scroll}}
}

func (c *call) hid() (hidState, error) {
	var s hidState
	return s, c.r.API.GetJSON(c.ctx, c.dev, c.cred, []string{"api", "hid"}, nil, &s)
}

func str(m map[string]any, path ...string) string {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[p]
	}
	s, _ := cur.(string)
	return short(s, 60)
}

// info returns what identifies the device: model, platform and versions. The
// serial number, the host name and the network are left out.
func (c *call) info() (map[string]any, error) {
	var raw map[string]any
	if err := c.r.API.GetJSON(c.ctx, c.dev, c.cred, []string{"api", "info"}, url.Values{"fields": {"hw,system"}}, &raw); err != nil {
		return nil, err
	}
	out := map[string]any{
		"model":    str(raw, "hw", "platform", "model"),
		"base":     str(raw, "hw", "platform", "base"),
		"type":     str(raw, "hw", "platform", "type"),
		"board":    str(raw, "hw", "platform", "board"),
		"video":    str(raw, "hw", "platform", "video"),
		"version":  str(raw, "system", "kvmd", "version"),
		"streamer": str(raw, "system", "streamer", "version"),
		"kernel":   str(raw, "system", "kernel", "release"),
	}
	if hw, ok := raw["hw"].(map[string]any); ok {
		if h, ok := hw["health"].(map[string]any); ok {
			if t, ok := h["temp"].(map[string]any); ok {
				if cpu, ok := t["cpu"].(float64); ok {
					out["cpuTempC"] = cpu
				}
			}
		}
	}
	return out, nil
}
