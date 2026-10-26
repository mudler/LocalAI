package session

import (
	"errors"
	"fmt"
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
