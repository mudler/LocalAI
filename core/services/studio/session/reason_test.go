package session

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestARefusedModelRequestGetsAReasonTheUserCanAct(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("call failed: %w", errors.New("Post x: "+MarkNotAllowed)), ReasonNotAllowed},
		{errors.New("agent: " + MarkQuota), ReasonQuota},
		{errors.New("connection reset"), reasonAgent},
	} {
		if got := reasonFor(tc.err); got != tc.want {
			t.Errorf("reasonFor(%v) = %q, want %q", tc.err, got, tc.want)
		}
		if got := fixedReason(reasonFor(tc.err)); got != reasonFor(tc.err) {
			t.Errorf("fixedReason changed %q to %q: a reason must survive the transcript filter", reasonFor(tc.err), got)
		}
	}
}

func TestAModelErrorGetsAReasonThatSaysWhatToDo(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		contains []string
	}{
		{"context too small with numbers", errors.New("the request (9100 tokens) exceeds the available context size (8192)"),
			[]string{"context window is too small", "908 tokens"}},
		{"context too small without numbers", errors.New("prompt is too long for the context"),
			[]string{"context window is too small"}},
		{"the model calls a tool that is not offered", errors.New("failed to select tool: selected tool get-time not found in available tools"),
			[]string{"tool that is not available", "get-time", "<connector>__<tool>"}},
		{"model keeps failing", errors.New("the model failed 4 times in a row (last: HTTP 502 bad gateway upstream.internal:8080)"),
			[]string{"The model failed", "HTTP 502"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := reasonFor(tc.err)
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("reason %q lacks %q", got, want)
				}
			}
			if strings.Contains(got, "upstream.internal") {
				t.Errorf("reason %q leaks an internal address", got)
			}
			if fixedReason(got) != got {
				t.Errorf("the transcript filter rewrote the reason %q", got)
			}
		})
	}
}
