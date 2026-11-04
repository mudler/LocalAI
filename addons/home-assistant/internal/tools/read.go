// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/home-assistant/internal/policy"
)

type haState struct {
	EntityID    string         `json:"entity_id"`
	State       string         `json:"state"`
	Attributes  map[string]any `json:"attributes"`
	LastChanged string         `json:"last_changed"`
	LastUpdated string         `json:"last_updated"`
}

// secretAttr names attributes that are removed from every result: addresses to
// pictures and streams that carry an access token, secrets, and where a person
// or a device is.
var secretAttr = map[string]bool{
	"entity_picture": true, "entity_picture_local": true, "access_token": true, "token": true,
	"latitude": true, "longitude": true, "gps_accuracy": true, "altitude": true, "vertical_accuracy": true,
	"course": true, "speed": true, "ip": true, "ip_address": true, "mac": true, "mac_address": true,
	"ssid": true, "bssid": true, "still_image_url": true, "stream_url": true, "video_url": true, "snapshot_url": true,
	"last_known_location": true, "location": true, "gps": true,
}

func secretKey(k string) bool {
	l := strings.ToLower(k)
	if secretAttr[l] {
		return true
	}
	for _, w := range []string{"token", "password", "passwd", "secret", "api_key", "apikey", "credential"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

// leaky reports a string value that carries an access token in an address.
func leaky(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "access_token=") || strings.Contains(l, "token=") || strings.Contains(l, "authsig=") || strings.Contains(l, "bearer ")
}

// cleanAttrs returns the attributes without the secret ones, each value
// bounded, and the whole bounded. want, when not empty, names the ones to keep.
func cleanAttrs(in map[string]any, want []string) (out map[string]any, omitted int) {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pick := map[string]bool{}
	for _, w := range want {
		pick[strings.ToLower(w)] = true
	}
	out = map[string]any{}
	size := 0
	for _, k := range keys {
		if secretKey(k) || (len(pick) > 0 && !pick[strings.ToLower(k)]) {
			omitted++
			continue
		}
		v := boundValue(in[k])
		if v == nil && in[k] != nil {
			omitted++
			continue
		}
		b, _ := json.Marshal(v)
		if size+len(b)+len(k) > maxAttrBytes {
			omitted++
			continue
		}
		size += len(b) + len(k)
		out[k] = v
	}
	return out, omitted
}

// boundValue keeps a scalar, cuts a long string, and keeps a small list or
// map as it is. A value that is too large or leaks a token returns nil.
func boundValue(v any) any {
	switch x := v.(type) {
	case nil, bool, float64:
		return x
	case string:
		if leaky(x) {
			return nil
		}
		return short(x, 200)
	default:
		b, err := json.Marshal(x)
		if err != nil || len(b) > 400 || leaky(string(b)) {
			return nil
		}
		var again any
		_ = json.Unmarshal(b, &again)
		return again
	}
}

func friendly(a map[string]any, id string) string {
	if n, ok := a["friendly_name"].(string); ok && n != "" {
		return short(n, 100)
	}
	return id
}

type areaArgs struct {
	Instance string `json:"instance"`
}

func (c *call) getConfig(args map[string]json.RawMessage) (any, error) {
	var a areaArgs
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if err := c.instance(a.Instance); err != nil {
		return nil, err
	}
	var cfg struct {
		Version    string            `json:"version"`
		Location   string            `json:"location_name"`
		TimeZone   string            `json:"time_zone"`
		Units      map[string]string `json:"unit_system"`
		Currency   string            `json:"currency"`
		Country    string            `json:"country"`
		Language   string            `json:"language"`
		State      string            `json:"state"`
		SafeMode   bool              `json:"safe_mode"`
		Components []string          `json:"components"`
	}
	if err := c.get("/api/config", nil, &cfg); err != nil {
		return nil, err
	}
	units := map[string]string{}
	for k, v := range cfg.Units {
		units[short(k, 32)] = short(v, 32)
	}
	return map[string]any{
		"ok": true, "version": short(cfg.Version, 32), "name": short(cfg.Location, 64), "timeZone": short(cfg.TimeZone, 64), "units": units,
		"currency": short(cfg.Currency, 8), "country": short(cfg.Country, 8), "language": short(cfg.Language, 16),
		"state": short(cfg.State, 16), "safeMode": cfg.SafeMode, "integrations": len(cfg.Components),
	}, nil
}

func (c *call) states() ([]haState, error) {
	var all []haState
	if err := c.get("/api/states", nil, &all); err != nil {
		return nil, err
	}
	return all, nil
}

func (c *call) listAreas(args map[string]json.RawMessage) (any, error) {
	var a areaArgs
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if err := c.instance(a.Instance); err != nil {
		return nil, err
	}
	hid, err := c.hidden()
	if err != nil {
		return nil, err
	}
	all, err := c.states()
	if err != nil {
		return nil, err
	}
	domains := map[string]int{}
	unavailable := 0
	for _, s := range all {
		if !c.visible(s.EntityID, hid) {
			continue
		}
		d, _, _ := strings.Cut(s.EntityID, ".")
		domains[d]++
		if s.State == "unavailable" || s.State == "unknown" {
			unavailable++
		}
	}
	areas := []map[string]any{}
	var rows []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Entities int    `json:"entities"`
		Devices  int    `json:"devices"`
	}
	tpl := `{% set ns = namespace(r=[]) %}{% for a in areas() %}{% set ns.r = ns.r + [{"id": a, "name": area_name(a), "entities": area_entities(a) | length, "devices": area_devices(a) | length}] %}{% endfor %}{{ ns.r | to_json }}`
	note := ""
	if err := c.render(tpl, &rows); err != nil {
		if !isInvalid(err) {
			return nil, err
		}
		note = "this Home Assistant did not answer the area question"
	}
	for _, r := range rows {
		if c.pol.AreaExcluded(r.ID) || c.pol.AreaExcluded(r.Name) {
			continue
		}
		if len(areas) >= 100 {
			break
		}
		areas = append(areas, map[string]any{"id": short(r.ID, 64), "name": short(r.Name, 64), "entities": r.Entities, "devices": r.Devices})
	}
	out := map[string]any{"areas": areas, "entitiesByDomain": domains, "unavailable": unavailable}
	if note != "" {
		out["note"] = note
	}
	return out, nil
}

type searchArgs struct {
	Instance string `json:"instance"`
	Query    string `json:"query"`
	Domain   string `json:"domain"`
	Area     string `json:"area"`
	State    string `json:"state"`
	Limit    int    `json:"limit"`
}

func (c *call) searchEntities(args map[string]json.RawMessage) (any, error) {
	var a searchArgs
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if err := c.instance(a.Instance); err != nil {
		return nil, err
	}
	if err := checkText("query", a.Query, 100); err != nil {
		return nil, err
	}
	a.Domain = strings.ToLower(a.Domain)
	if a.Domain != "" && !policy.EntityID.MatchString(a.Domain+".x") {
		return nil, bad("domain is a word like light")
	}
	if err := checkText("state", a.State, 64); err != nil {
		return nil, err
	}
	limit := a.Limit
	if limit < 1 {
		limit = 25
	}
	limit = min(limit, maxSearchResults)
	hid, err := c.hidden()
	if err != nil {
		return nil, err
	}
	var inArea map[string]bool
	if a.Area != "" {
		if c.pol.AreaExcluded(a.Area) {
			return nil, bad("this area is excluded by the policy of the add-on")
		}
		ids, err := c.areaEntities([]string{a.Area})
		if err != nil {
			return nil, err
		}
		inArea = map[string]bool{}
		for _, id := range ids {
			inArea[strings.ToLower(id)] = true
		}
	}
	all, err := c.states()
	if err != nil {
		return nil, err
	}
	words := strings.Fields(strings.ToLower(a.Query))
	var hits []haState
	for _, s := range all {
		id := strings.ToLower(s.EntityID)
		if !c.visible(id, hid) {
			continue
		}
		if a.Domain != "" && !strings.HasPrefix(id, a.Domain+".") {
			continue
		}
		if inArea != nil && !inArea[id] {
			continue
		}
		if a.State != "" && !strings.EqualFold(s.State, a.State) {
			continue
		}
		hay := id + " " + strings.ToLower(friendly(s.Attributes, ""))
		ok := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				ok = false
				break
			}
		}
		if ok {
			hits = append(hits, s)
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].EntityID < hits[j].EntityID })
	total := len(hits)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.EntityID)
	}
	areaOf := c.areasOf(ids)
	rows := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		row := map[string]any{"entityId": h.EntityID, "name": friendly(h.Attributes, h.EntityID), "state": short(h.State, 100)}
		if u, ok := h.Attributes["unit_of_measurement"].(string); ok && u != "" {
			row["unit"] = short(u, 16)
		}
		if d, ok := h.Attributes["device_class"].(string); ok && d != "" {
			row["deviceClass"] = short(d, 32)
		}
		if ar := areaOf[h.EntityID]; ar != "" {
			row["area"] = short(ar, 64)
		}
		rows = append(rows, row)
	}
	return map[string]any{"total": total, "returned": len(rows), "truncated": total > len(rows), "entities": rows}, nil
}

// areasOf asks for the area name of up to 50 entities. A failure leaves the
// area out; it is a convenience and not a check.
func (c *call) areasOf(ids []string) map[string]string {
	out := map[string]string{}
	if len(ids) == 0 {
		return out
	}
	for _, id := range ids {
		if !policy.EntityID.MatchString(id) {
			return out
		}
	}
	var pairs [][]any
	tpl := `{% set ns = namespace(r=[]) %}{% for e in ` + quoteNames(ids) + ` %}{% set ns.r = ns.r + [[e, area_name(e)]] %}{% endfor %}{{ ns.r | to_json }}`
	if c.render(tpl, &pairs) != nil {
		return out
	}
	for _, p := range pairs {
		if len(p) == 2 {
			id, _ := p[0].(string)
			name, _ := p[1].(string)
			if id != "" && name != "" && !c.pol.AreaExcluded(name) {
				out[id] = name
			}
		}
	}
	return out
}

type stateArgs struct {
	Instance   string   `json:"instance"`
	EntityID   string   `json:"entityId"`
	Attributes []string `json:"attributes"`
}

func (c *call) visibleEntity(id string) error {
	if err := checkEntity(id); err != nil {
		return err
	}
	hid, err := c.hidden()
	if err != nil {
		return err
	}
	if !c.visible(id, hid) {
		return bad("this entity is excluded by the policy of the add-on")
	}
	return nil
}

func (c *call) getState(args map[string]json.RawMessage) (any, error) {
	var a stateArgs
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if err := c.instance(a.Instance); err != nil {
		return nil, err
	}
	if len(a.Attributes) > 20 {
		return nil, bad("attributes names at most 20")
	}
	if err := c.visibleEntity(a.EntityID); err != nil {
		return nil, err
	}
	var s haState
	if err := c.get("/api/states/"+url.PathEscape(a.EntityID), nil, &s); err != nil {
		return nil, err
	}
	attrs, omitted := cleanAttrs(s.Attributes, a.Attributes)
	out := map[string]any{"entityId": s.EntityID, "name": friendly(s.Attributes, s.EntityID), "state": short(s.State, 255), "attributes": attrs,
		"lastChanged": short(s.LastChanged, 40), "lastUpdated": short(s.LastUpdated, 40)}
	if omitted > 0 {
		out["attributesOmitted"] = omitted
	}
	if ar := c.areasOf([]string{s.EntityID})[s.EntityID]; ar != "" {
		out["area"] = ar
	}
	return out, nil
}

type windowArgs struct {
	Instance string `json:"instance"`
	EntityID string `json:"entityId"`
	Hours    int    `json:"hours"`
	End      string `json:"end"`
}

// window returns the start and end of a history or logbook window.
func (c *call) window(a windowArgs) (time.Time, time.Time, error) {
	hours := a.Hours
	if hours == 0 {
		hours = 24
	}
	if hours < 1 || hours > maxHistoryHours {
		return time.Time{}, time.Time{}, bad("hours is 1 to %d", maxHistoryHours)
	}
	end := c.r.now().UTC()
	if a.End != "" {
		t, err := time.Parse(time.RFC3339, a.End)
		if err != nil {
			return time.Time{}, time.Time{}, bad("end is a time like 2026-11-04T10:00:00Z")
		}
		end = t.UTC()
	}
	return end.Add(-time.Duration(hours) * time.Hour), end, nil
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

func (c *call) getHistory(args map[string]json.RawMessage) (any, error) {
	var a windowArgs
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if err := c.instance(a.Instance); err != nil {
		return nil, err
	}
	if err := c.visibleEntity(a.EntityID); err != nil {
		return nil, err
	}
	start, end, err := c.window(a)
	if err != nil {
		return nil, err
	}
	q := url.Values{"filter_entity_id": {a.EntityID}, "end_time": {stamp(end)}, "minimal_response": {""}, "no_attributes": {""}}
	var rows [][]struct {
		State       string `json:"state"`
		LastChanged string `json:"last_changed"`
	}
	if err := c.get("/api/history/period/"+stamp(start), q, &rows); err != nil {
		return nil, err
	}
	type point struct {
		T string `json:"t"`
		S string `json:"s"`
	}
	pts := []point{}
	if len(rows) > 0 {
		for _, r := range rows[0] {
			pts = append(pts, point{T: short(r.LastChanged, 40), S: short(r.State, 100)})
		}
	}
	total := len(pts)
	if len(pts) > maxHistoryPoints {
		keep := make([]point, 0, maxHistoryPoints)
		for i := 0; i < maxHistoryPoints; i++ {
			keep = append(keep, pts[i*(total-1)/(maxHistoryPoints-1)])
		}
		pts = keep
	}
	out := map[string]any{"entityId": a.EntityID, "from": stamp(start), "to": stamp(end), "changes": total, "points": pts, "sampled": total > len(pts)}
	var nums []float64
	for _, r := range rows {
		for _, p := range r {
			if f, err := strconv.ParseFloat(p.State, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
				nums = append(nums, f)
			}
		}
	}
	if len(nums) > 0 {
		lo, hi, sum := nums[0], nums[0], 0.0
		for _, f := range nums {
			lo, hi, sum = math.Min(lo, f), math.Max(hi, f), sum+f
		}
		out["numeric"] = map[string]any{"count": len(nums), "min": lo, "max": hi, "mean": math.Round(sum/float64(len(nums))*100) / 100}
	}
	return out, nil
}

func (c *call) getLogbook(args map[string]json.RawMessage) (any, error) {
	var a windowArgs
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if err := c.instance(a.Instance); err != nil {
		return nil, err
	}
	if err := c.visibleEntity(a.EntityID); err != nil {
		return nil, err
	}
	start, end, err := c.window(a)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		When    string `json:"when"`
		Name    string `json:"name"`
		Message string `json:"message"`
		State   string `json:"state"`
	}
	if err := c.get("/api/logbook/"+stamp(start), url.Values{"entity": {a.EntityID}, "end_time": {stamp(end)}}, &rows); err != nil {
		return nil, err
	}
	total := len(rows)
	if len(rows) > maxLogbookEntries {
		rows = rows[len(rows)-maxLogbookEntries:]
	}
	entries := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		e := map[string]any{"when": short(r.When, 40), "name": short(r.Name, 100)}
		if r.Message != "" {
			e["message"] = short(r.Message, 200)
		}
		if r.State != "" {
			e["state"] = short(r.State, 100)
		}
		entries = append(entries, e)
	}
	return map[string]any{"entityId": a.EntityID, "from": stamp(start), "to": stamp(end), "total": total, "entries": entries, "truncated": total > len(entries)}, nil
}

type servicesArgs struct {
	Instance string `json:"instance"`
	Domain   string `json:"domain"`
}

func (c *call) listServices(args map[string]json.RawMessage) (any, error) {
	var a servicesArgs
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if err := c.instance(a.Instance); err != nil {
		return nil, err
	}
	a.Domain = strings.ToLower(a.Domain)
	var all []struct {
		Domain   string `json:"domain"`
		Services map[string]struct {
			Description string `json:"description"`
			Fields      map[string]struct {
				Description string `json:"description"`
				Required    bool   `json:"required"`
			} `json:"fields"`
		} `json:"services"`
	}
	if err := c.get("/api/services", nil, &all); err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	truncated := false
	for _, d := range all {
		if a.Domain != "" && d.Domain != a.Domain {
			continue
		}
		names := make([]string, 0, len(d.Services))
		for n := range d.Services {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if !c.pol.ServiceAllowed(d.Domain, n) {
				continue
			}
			if len(rows) >= maxServiceRows {
				truncated = true
				break
			}
			sv := d.Services[n]
			fnames := make([]string, 0, len(sv.Fields))
			for f := range sv.Fields {
				fnames = append(fnames, f)
			}
			sort.Strings(fnames)
			if len(fnames) > 12 {
				fnames = fnames[:12]
			}
			fields := make([]map[string]any, 0, len(fnames))
			for _, f := range fnames {
				fields = append(fields, map[string]any{"name": short(f, 48), "description": short(sv.Fields[f].Description, 100), "required": sv.Fields[f].Required})
			}
			tool := ToolCallService
			if policy.Physical(d.Domain, n) {
				tool = ToolCallPhysical
			}
			rows = append(rows, map[string]any{"domain": d.Domain, "service": n, "description": short(sv.Description, 160), "fields": fields, "tool": tool})
		}
	}
	return map[string]any{"services": rows, "truncated": truncated,
		"note": "Only the services the policy of the add-on allows are listed. The tool column says which write tool calls the service."}, nil
}
