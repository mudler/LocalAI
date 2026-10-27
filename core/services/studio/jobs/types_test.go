package jobs_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/jobs"
)

func TestValidateRefusesWithTheReason(t *testing.T) {
	good := jobs.Task{ID: "t", Owner: "u", Name: "N", Model: "m", Prompt: "Say {{.word}}"}
	if err := jobs.Validate(good); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*jobs.Task)
		want   string
	}{
		{"no name", func(t *jobs.Task) { t.Name = " " }, "name is required"},
		{"no prompt", func(t *jobs.Task) { t.Prompt = "" }, "prompt is required"},
		{"no model and no agent", func(t *jobs.Task) { t.Model = "" }, "model is required"},
		{"bad template", func(t *jobs.Task) { t.Prompt = "{{.word" }, "not a valid template"},
		{"webhook without url", func(t *jobs.Task) { t.Webhooks = []jobs.Webhook{{}} }, "no URL"},
		{"webhook bad method", func(t *jobs.Task) { t.Webhooks = []jobs.Webhook{{URL: "https://a.test", Method: "GET"}} }, "POST, PUT or PATCH"},
		{"webhook bad payload", func(t *jobs.Task) {
			t.Webhooks = []jobs.Webhook{{URL: "https://a.test", PayloadTemplate: "{{"}}
		}, "payload template is not valid"},
		{"too many webhooks", func(t *jobs.Task) { t.Webhooks = make([]jobs.Webhook, jobs.MaxWebhooks+1) }, "at most"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			task := good
			tc.mutate(&task)
			err := jobs.Validate(task)
			if !errors.Is(err, jobs.ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to wrap ErrInvalid and name %q", err, tc.want)
			}
		})
	}
}

func TestPayloadFuncsKeepTheServerEnvironmentOut(t *testing.T) {
	f := jobs.PayloadFuncs()
	for _, name := range []string{"env", "expandenv", "getHostByName"} {
		if _, ok := f[name]; ok {
			t.Errorf("%s is available to a payload template", name)
		}
	}
	for _, name := range []string{"json", "upper", "default"} {
		if _, ok := f[name]; !ok {
			t.Errorf("%s is missing", name)
		}
	}
}
