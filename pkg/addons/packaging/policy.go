// SPDX-License-Identifier: MIT
package packaging

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// trustedJobs are the only jobs that may hold registry credentials, the
// OIDC token permission or an environment.
var trustedJobs = map[string]bool{"publish": true, "promote": true}

// deniedInWorkflow are strings that tie a workflow to the backend image
// pipeline. The add-on package workflow has its own registry input, its own
// signer identity and its own jobs.
var deniedInWorkflow = []string{
	"quay.io", "go-skynet", "backend_merge", "backend_build", "backend.yml", "backend_pr",
	"ci-cache", "docker.io", "dockerhub", "quayusername", "quaypassword", "dockerusername", "dockerpassword",
}

// checkWorkflow applies the workflow policy to a parsed workflow and returns
// every violation, each with a stable P_ code.
func checkWorkflow(data []byte) []string {
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return []string{"P_PARSE: " + err.Error()}
	}
	var out []string
	add := func(code, format string, a ...any) { out = append(out, code+": "+fmt.Sprintf(format, a...)) }
	text := func(v any) string { b, _ := yaml.Marshal(v); return string(b) }
	whole := text(doc)
	lower := strings.ToLower(whole)

	on, _ := doc["on"].(map[string]any)
	if on == nil {
		on, _ = doc[fmt.Sprint(true)].(map[string]any)
	}
	if on == nil {
		add("P_TRIGGERS", "workflow has no on: mapping")
	}
	if _, ok := on["pull_request_target"]; ok || strings.Contains(lower, "pull_request_target") {
		add("P_PR_TARGET", "pull_request_target must not appear: it runs untrusted code with repository credentials")
	}
	for _, t := range []string{"push", "pull_request", "release"} {
		if _, ok := on[t]; !ok {
			add("P_TRIGGERS", "trigger %s is required", t)
		}
	}
	for _, d := range deniedInWorkflow {
		if strings.Contains(lower, d) {
			add("P_BACKEND", "workflow references the backend pipeline: %q", d)
		}
	}
	// A literal latest tag would let any run move the stable channel.
	for _, loc := range regexp.MustCompile(`latest`).FindAllStringIndex(whole, -1) {
		before, after := byte(' '), byte(' ')
		if loc[0] > 0 {
			before = whole[loc[0]-1]
		}
		if loc[1] < len(whole) {
			after = whole[loc[1]]
		}
		word := func(c byte) bool {
			return c == '-' || c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		}
		if !word(before) && !word(after) {
			add("P_LATEST", "a literal latest tag appears; only scripts/addons/channel.sh may decide it")
			break
		}
	}
	perms, _ := doc["permissions"].(map[string]any)
	if len(perms) != 1 || perms["contents"] != "read" {
		add("P_PERMISSIONS", "workflow-level permissions must be exactly contents: read")
	}
	jobs, _ := doc["jobs"].(map[string]any)
	job := func(n string) map[string]any { m, _ := jobs[n].(map[string]any); return m }
	for _, n := range []string{"plan", "validate", "package", "publish", "promote"} {
		if job(n) == nil {
			add("P_JOBS", "job %s is required", n)
		}
	}
	for n, v := range jobs {
		j, _ := v.(map[string]any)
		t := text(j)
		if trustedJobs[n] {
			continue
		}
		if regexp.MustCompile(`secrets\.[A-Za-z0-9_]*`).FindString(strings.ReplaceAll(t, "secrets.GITHUB_TOKEN", "")) != "" {
			add("P_UNTRUSTED_SECRETS", "job %s reads secrets: only publish and promote may", n)
		}
		if _, ok := j["environment"]; ok {
			add("P_UNTRUSTED_SECRETS", "job %s names an environment: only publish and promote may", n)
		}
		if p, ok := j["permissions"].(map[string]any); ok {
			for k, val := range p {
				if val != "read" && val != "none" {
					add("P_UNTRUSTED_SECRETS", "job %s has %s: %v; untrusted jobs are read-only and never hold id-token", n, k, val)
				}
			}
		}
		if strings.Contains(t, "id-token") && !strings.Contains(t, "id-token: none") {
			add("P_UNTRUSTED_SECRETS", "job %s mentions id-token", n)
		}
		if strings.Contains(t, "docker/login-action") || strings.Contains(t, "cosign sign") || strings.Contains(t, "addonpkg publish") || strings.Contains(t, "addonpkg promote") {
			add("P_UNTRUSTED_SECRETS", "job %s logs in, signs or publishes: only publish and promote may", n)
		}
	}
	needs := func(n string) map[string]bool {
		out := map[string]bool{}
		switch v := job(n)["needs"].(type) {
		case string:
			out[v] = true
		case []any:
			for _, x := range v {
				out[fmt.Sprint(x)] = true
			}
		}
		return out
	}
	pub, pro := job("publish"), job("promote")
	for _, n := range []string{"plan", "package"} {
		if !needs("publish")[n] {
			add("P_NEEDS", "publish must need %s: every platform leg must pass before anything is published", n)
		}
	}
	if !needs("package")["plan"] {
		add("P_NEEDS", "package must need plan")
	}
	if !needs("promote")["publish"] {
		add("P_NEEDS", "promote must need publish: aliases move only after signing")
	}
	for _, n := range []string{"publish", "promote"} {
		j := job(n)
		cond := fmt.Sprint(j["if"])
		if !strings.Contains(cond, "needs.plan.outputs.publish == 'true'") {
			add("P_TRUSTED_GATE", "%s must run only when the plan says publish == 'true'", n)
		}
		for _, bad := range []string{"always()", "cancelled()", "failure()"} {
			if strings.Contains(cond, bad) {
				add("P_TRUSTED_GATE", "%s must not use %s: a failed leg would still publish", n, bad)
			}
		}
		if j != nil && j["environment"] == nil {
			add("P_TRUSTED_GATE", "%s must run in a protected environment", n)
		}
	}
	if p, _ := pub["permissions"].(map[string]any); p["id-token"] != "write" {
		add("P_SIGNER", "publish needs id-token: write for keyless signing")
	}
	if p, _ := pro["permissions"].(map[string]any); p["id-token"] == "write" {
		add("P_SIGNER", "promote must not hold id-token: write")
	}
	if pro != nil && strings.Contains(fmt.Sprint(pro["if"]), "needs.plan.outputs.aliases") == false {
		add("P_TRUSTED_GATE", "promote must run only when the plan names aliases (a tag push names none)")
	}
	// Matrix comes from the validated platform matrix, never a literal.
	if m, _ := job("package")["strategy"].(map[string]any); m == nil || !strings.Contains(text(m), "needs.plan.outputs.matrix") {
		add("P_MATRIX", "package matrix must come from needs.plan.outputs.matrix")
	}
	// Step order inside publish: merge, push, sign, verify.
	steps, _ := pub["steps"].([]any)
	index := map[string]int{}
	for i, s := range steps {
		st, _ := s.(map[string]any)
		if id, ok := st["id"].(string); ok {
			index[id] = i
		}
		if strings.Contains(text(st), "addonpkg promote") {
			add("P_ORDER", "publish must not move aliases; promote does after signing")
		}
	}
	last := -1
	for _, id := range []string{"merge", "push", "sign", "verify"} {
		i, ok := index[id]
		if !ok {
			add("P_ORDER", "publish has no step with id %s", id)
			continue
		}
		if i < last {
			add("P_ORDER", "step %s runs before an earlier stage of merge, push, sign, verify", id)
		}
		last = i
	}
	if i, ok := index["sign"]; ok {
		s := text(steps[i])
		for _, need := range []string{"cosign sign", "--new-bundle-format", "--registry-referrers-mode=oci-1-1", "@${DIGEST}"} {
			if !strings.Contains(s, need) {
				add("P_SIGNER", "sign step lacks %q", need)
			}
		}
	}
	if i, ok := index["verify"]; ok {
		s := text(steps[i])
		for _, need := range []string{"cosign verify", "--certificate-identity ", "--certificate-oidc-issuer ", SignerWorkflow, "--new-bundle-format"} {
			if !strings.Contains(s, need) {
				add("P_SIGNER", "verify step lacks %q", need)
			}
		}
		if strings.Contains(s, "identity-regexp") || strings.Contains(s, ".*") {
			add("P_SIGNER", "verify step must pin an exact identity, not a pattern")
		}
	}
	// Promote: the alias command, serialized per channel.
	if !strings.Contains(text(pro), "addonpkg promote") {
		add("P_ORDER", "promote job does not run addonpkg promote")
	}
	conc, _ := pro["concurrency"].(map[string]any)
	if !strings.Contains(fmt.Sprint(conc["group"]), "needs.plan.outputs.channel") || conc["cancel-in-progress"] != false {
		add("P_CONCURRENCY", "promote must serialize per channel with cancel-in-progress: false")
	}
	conc, _ = pub["concurrency"].(map[string]any)
	if !strings.Contains(fmt.Sprint(conc["group"]), "needs.plan.outputs.immutable") || conc["cancel-in-progress"] != false {
		add("P_CONCURRENCY", "publish must serialize per immutable tag with cancel-in-progress: false")
	}
	// Plan fails closed on missing configuration.
	plan := text(job("plan"))
	for _, need := range []string{"scripts/addons/channel.sh", "addonpkg matrix", `-z "$ADDON_OCI_REPOSITORY"`} {
		if !strings.Contains(plan, need) {
			add("P_FAIL_CLOSED", "plan job lacks %q", need)
		}
	}
	// Checkout never takes code of a pull request head into a job that could
	// hold credentials, and never persists the token.
	for n, v := range jobs {
		j, _ := v.(map[string]any)
		steps, _ := j["steps"].([]any)
		for _, s := range steps {
			st, _ := s.(map[string]any)
			uses, _ := st["uses"].(string)
			if !strings.HasPrefix(uses, "actions/checkout@") {
				continue
			}
			w, _ := st["with"].(map[string]any)
			if strings.Contains(fmt.Sprint(w["ref"]), "pull_request.head") || strings.Contains(fmt.Sprint(w["ref"]), "head_ref") {
				add("P_CHECKOUT", "job %s checks out a pull request head", n)
			}
			if w["persist-credentials"] != false {
				add("P_CHECKOUT", "job %s must set persist-credentials: false", n)
			}
		}
	}
	return out
}

func cmdPolicy(args []string) error {
	fs := flag.NewFlagSet("policy", flag.ContinueOnError)
	file := fs.String("workflow", ".github/workflows/addons-package.yml", "workflow file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	b, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	problems := checkWorkflow(b)
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	fmt.Fprintf(stdout, "workflow-policy=passed\n")
	return nil
}
