// SPDX-License-Identifier: MIT
package tools

import (
	"strings"
)

// AllKey is the entry of the list that lets a connection use the whole
// mailbox. Any other entry is a label, by its ID or its name; a name with
// spaces is written with dashes.
const AllKey = "ALL"

const labelsPath = "/gmail/v1/users/me/labels"

type label struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// labels reads the labels of the mailbox, once per call.
func (c *call) labels() ([]label, error) {
	if c.labelsOK {
		return c.labelList, nil
	}
	var out struct {
		Labels []label `json:"labels"`
	}
	if err := c.r.API.GetJSON(c.ctx, c.token, labelsPath, nil, &out); err != nil {
		return nil, err
	}
	c.labelList, c.labelsOK = out.Labels, true
	return c.labelList, nil
}

// checkLabel checks the label argument: it is the key the host allowed for
// this call.
func (c *call) checkLabel(arg string) error {
	if strings.TrimSpace(arg) == "" || len(arg) > 256 {
		return bad("label is required")
	}
	if c.key == "" || !strings.EqualFold(c.key, arg) {
		return bad("label is not the one the host allowed")
	}
	return nil
}

func (c *call) wholeMailbox() bool { return strings.EqualFold(c.key, AllKey) }

// find returns the label a key or a name stands for.
func (c *call) find(key string) (label, bool, error) {
	ls, err := c.labels()
	if err != nil {
		return label{}, false, err
	}
	for _, match := range []func(label) bool{
		func(l label) bool { return strings.EqualFold(l.ID, key) },
		func(l label) bool { return strings.EqualFold(l.Name, key) },
		func(l label) bool { return strings.EqualFold(strings.ReplaceAll(l.Name, " ", "-"), key) },
	} {
		for _, l := range ls {
			if match(l) {
				return l, true, nil
			}
		}
	}
	return label{}, false, nil
}

// scopeLabel returns the ID of the label that holds every message of the
// call, or "" for the whole mailbox. A label that does not exist is a
// refusal, so a typo never widens the reach.
func (c *call) scopeLabel() (string, error) {
	if c.wholeMailbox() {
		return "", nil
	}
	l, ok, err := c.find(c.key)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", bad("the allowed label does not exist in this mailbox")
	}
	return l.ID, nil
}

// inScope reports whether a message with these labels is inside the reach of
// the connection. It fails closed.
func (c *call) inScope(labelIDs []string) (bool, error) {
	id, err := c.scopeLabel()
	if err != nil {
		return false, err
	}
	if id == "" {
		return true, nil
	}
	for _, l := range labelIDs {
		if l == id {
			return true, nil
		}
	}
	return false, nil
}
