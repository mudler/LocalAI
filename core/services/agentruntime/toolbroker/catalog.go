// SPDX-License-Identifier: MIT
package toolbroker

import (
	"encoding/json"
	"sort"
)

type ToolClass uint8

const (
	ReadTool ToolClass = iota + 1
	WriteTool
	HostControlTool
)

type ToolSpec struct {
	ID              ToolID
	Name            string
	Class           ToolClass
	Revision, Group string
}
type Catalog struct{ specs map[ToolID]ToolSpec }

func NewCatalog(specs []ToolSpec) (Catalog, error) {
	c := Catalog{specs: map[ToolID]ToolSpec{}}
	names := map[string]bool{}
	for _, s := range specs {
		if s.ID == "" || s.Name == "" || s.Revision == "" || s.Class < ReadTool || s.Class > HostControlTool || names[s.Name] {
			return Catalog{}, ErrInvalid
		}
		if _, ok := c.specs[s.ID]; ok {
			return Catalog{}, ErrInvalid
		}
		c.specs[s.ID] = s
		names[s.Name] = true
	}
	return c, nil
}
func (c Catalog) GroupIDs(group string) []ToolID {
	out := []ToolID{}
	for id, s := range c.specs {
		if s.Group == group {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// HelperGrant is host-registry evidence, never an AgentID supplied by the model.
// The later shell must resolve it afresh; the pure model cannot authenticate it.
type HelperGrant struct {
	Scope Scope
	IDs   []ToolID
}
type Selection struct {
	Selected, Allow, Exclude, ACL, Resources, Artifacts []ToolID
	Scope                                               Scope
	Helper                                              *HelperGrant
}

func contains(ids []ToolID, id ToolID) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
func selected(s Selection, id ToolID) bool {
	return contains(s.Selected, id) && (len(s.Allow) == 0 || contains(s.Allow, id)) && !contains(s.Exclude, id) && contains(s.ACL, id) && contains(s.Resources, id) && (s.Helper == nil || (s.Scope == s.Helper.Scope && contains(s.Helper.IDs, id)))
}
func (c Catalog) Effective(s Selection) []ToolID {
	out := []ToolID{}
	for id := range c.specs {
		if selected(s, id) {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// TB-R24: a tool's resource access is not itself permission to save an artifact.
func CanSaveArtifact(s Selection, id ToolID) bool {
	return selected(s, id) && contains(s.Artifacts, id)
}

type finishStatus uint8

const (
	finishInactive finishStatus = iota
	finishPending
	finishSatisfied
)

type FinishGate struct {
	tool             ToolID
	status           finishStatus
	reminders, limit uint
}

func NewFinishGate(id ToolID, effective []ToolID, limit uint) FinishGate {
	g := FinishGate{tool: id, limit: limit}
	if id != "" && contains(effective, id) {
		g.status = finishPending
	}
	return g
}
func (g FinishGate) Active() bool    { return g.status != finishInactive }
func (g FinishGate) CanFinish() bool { return g.status != finishPending }
func (g FinishGate) Reminders() uint { return g.reminders }
func (g FinishGate) Remind() FinishGate {
	if g.status == finishPending && g.reminders < g.limit {
		g.reminders++
	}
	return g
}

// TB-R23: completion must be journal accepted, matching and explicitly successful.
func (g FinishGate) Observe(id ToolID, p Phase, o Output) FinishGate {
	if _, ok := p.(Completed); ok && g.status == finishPending && id == g.tool && OutputSuccess(o) {
		g.status = finishSatisfied
	}
	return g
}
func OutputSuccess(o Output) bool {
	var v struct {
		OK      *bool                         `json:"ok"`
		IsError bool                          `json:"isError"`
		Content []struct{ Type, Text string } `json:"content"`
	}
	if json.Unmarshal(o.Bytes(), &v) != nil || v.IsError {
		return false
	}
	if v.OK != nil {
		return *v.OK
	}
	// The supported MCP wrapper is exactly one text item containing the JSON result.
	if len(v.Content) != 1 || v.Content[0].Type != "text" {
		return false
	}
	var inner struct {
		OK bool `json:"ok"`
	}
	return json.Unmarshal([]byte(v.Content[0].Text), &inner) == nil && inner.OK
}
