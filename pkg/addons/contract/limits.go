// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"reflect"
	"time"
)

// Limits are host configuration, not addon-controlled manifest claims. Durations
// use Go time.Duration (nanoseconds in JSON). Zero never means unlimited.
type Limits struct {
	ManifestBytes      int64         `json:"manifestBytes"`
	EnvelopeBytes      int64         `json:"envelopeBytes"`
	PayloadBytes       int64         `json:"payloadBytes"`
	ReadTimeout        time.Duration `json:"readTimeout"`
	PreviewTimeout     time.Duration `json:"previewTimeout"`
	ApplyTimeout       time.Duration `json:"applyTimeout"`
	ShutdownTimeout    time.Duration `json:"shutdownTimeout"`
	BackoffInitial     time.Duration `json:"backoffInitial"`
	BackoffMax         time.Duration `json:"backoffMax"`
	BackoffAttempts    int64         `json:"backoffAttempts"`
	BatchSize          int64         `json:"batchSize"`
	QueryDepth         int64         `json:"queryDepth"`
	QueryRows          int64         `json:"queryRows"`
	QueryBytes         int64         `json:"queryBytes"`
	ProposalExpiry     time.Duration `json:"proposalExpiry"`
	ArtifactExpiry     time.Duration `json:"artifactExpiry"`
	ArtifactBytes      int64         `json:"artifactBytes"`
	ArtifactQuotaBytes int64         `json:"artifactQuotaBytes"`
	ArtifactCount      int64         `json:"artifactCount"`
	Concurrency        int64         `json:"concurrency"`
}

func DefaultLimits() Limits {
	return Limits{ManifestBytes: 64 << 10, EnvelopeBytes: 2 << 20, PayloadBytes: 1 << 20, ReadTimeout: 30 * time.Second, PreviewTimeout: 30 * time.Second, ApplyTimeout: 60 * time.Second, ShutdownTimeout: 10 * time.Second, BackoffInitial: 250 * time.Millisecond, BackoffMax: 30 * time.Second, BackoffAttempts: 5, BatchSize: 100, QueryDepth: 8, QueryRows: 1000, QueryBytes: 1 << 20, ProposalExpiry: 5 * time.Minute, ArtifactExpiry: time.Hour, ArtifactBytes: 100 << 20, ArtifactQuotaBytes: 1 << 30, ArtifactCount: 100, Concurrency: 4}
}
func (l Limits) Validate() error {
	v := reflect.ValueOf(l)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).Int() <= 0 {
			return fmt.Errorf("%s must be positive and finite", v.Type().Field(i).Name)
		}
	}
	if l.BackoffInitial > l.BackoffMax || l.ArtifactBytes > l.ArtifactQuotaBytes || l.PayloadBytes > l.EnvelopeBytes || l.QueryBytes > l.PayloadBytes || l.QueryDepth > 64 {
		return fmt.Errorf("inconsistent limits")
	}
	return nil
}
