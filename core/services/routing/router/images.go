// SPDX-License-Identifier: MIT
package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/systemone"
)

func (p Probe) decisionRequest() (*schema.SystemOneRequest, error) {
	if p.InputError != nil {
		return nil, p.InputError
	}
	if err := p.collectionBounds(); err != nil {
		return nil, err
	}
	state := p.State
	if len(state) == 0 {
		left := systemone.MaxImageBodyBytes
		if !systemone.SpendJSONString(p.Prompt, &left) {
			return nil, fmt.Errorf("router prompt exceeds serialized decision budget")
		}
		state, _ = json.Marshal(p.Prompt)
	}
	return &schema.SystemOneRequest{State: state, Images: p.Images}, nil
}

// HasImages uses the canonical collector, including malformed image parts.
// Callers must not treat collection failures as text-only input.
func (p Probe) HasImages(ctx context.Context) (bool, error) {
	release, err := systemone.AcquireAdmission(ctx)
	if err != nil {
		return false, err
	}
	defer release()

	r, err := p.decisionRequest()
	if err != nil {
		return false, err
	}
	images, err := systemone.CollectImages(r)
	return len(images) > 0, err
}
func requireTextProbe(ctx context.Context, p Probe) error {
	images, err := p.HasImages(ctx)
	if err != nil {
		return err
	}
	if images {
		return &systemone.ValidationError{Kind: systemone.UnsupportedBackend, Err: errTextImages}
	}
	return nil
}

var errTextImages = errors.New("text-only router classifier does not support image input")

// Check byte lengths before JSON conversion allocates a structured copy.
func (p Probe) collectionBounds() error {
	if len(p.State) > systemone.MaxImageBodyBytes || len(p.Images) > systemone.MaxImageEncodedBytes || len(p.Prompt) > systemone.MaxImageBodyBytes {
		return fmt.Errorf("router probe exceeds decision collection budget")
	}
	return nil
}
