// SPDX-License-Identifier: MIT
package packaging

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// The Sandbox executable is stamped with the digests of its guest images, and
// a registry serves a digest only while a tag names it. These specs keep the
// release pipeline from moving a tag that a published package depends on
// (issue 13500: a release event rebuilt the guest images and moved their tags,
// so the published Sandbox pointed at digests the registry no longer served).

const sandboxImagesWorkflow = "../../../.github/workflows/sandbox-images.yml"

// fakeDocker answers `docker buildx imagetools inspect <ref> ...` from a list
// of "<ref>=<digest>" lines in FAKE_TAGS. A ref that contains "unreachable"
// fails as a network error does; any other unknown ref is "not found".
const fakeDocker = `#!/bin/sh
ref=$4
case "$ref" in
*unreachable*) echo "ERROR: failed to do request: dial tcp: no such host" >&2; exit 1 ;;
esac
printf '%s\n' "$FAKE_TAGS" | while IFS='=' read -r tag digest; do
  if [ "$tag" = "$ref" ]; then printf '%s' "$digest"; exit 3; fi
done
[ $? -eq 3 ] && exit 0
echo "ERROR: $ref: not found" >&2
exit 1
`

var _ = Describe("the sandbox guest image workflow", func() {
	var workflow string
	BeforeEach(func() {
		b, err := os.ReadFile(sandboxImagesWorkflow)
		Expect(err).NotTo(HaveOccurred())
		workflow = string(b)
	})

	It("builds an image only when the plan says so, under the tag the plan names", func() {
		var doc struct {
			Jobs map[string]struct {
				Concurrency map[string]any `yaml:"concurrency"`
				Steps       []struct {
					ID   string            `yaml:"id"`
					If   string            `yaml:"if"`
					Uses string            `yaml:"uses"`
					With map[string]string `yaml:"with"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		Expect(yaml.Unmarshal([]byte(workflow), &doc)).To(Succeed())
		job := doc.Jobs["build"]
		builds := map[string]string{"build": "base", "build_media": "media", "build_office": "office"}
		seen := 0
		for _, st := range job.Steps {
			if !strings.HasPrefix(st.Uses, "docker/build-push-action") {
				continue
			}
			t, ok := builds[st.ID]
			Expect(ok).To(BeTrue(), "unknown build step %q", st.ID)
			seen++
			Expect(st.If).To(Equal("steps.plan.outputs.build_"+t+" == 'true'"), "build step %s must run only when the plan builds %s", st.ID, t)
			Expect(st.With["tags"]).To(Equal("${{ env.REPOSITORY }}:${{ steps.plan.outputs.tag_"+t+" }}"), "build step %s must push the tag the plan names", st.ID)
		}
		Expect(seen).To(Equal(3))
		// The runs that push sha-<sha> tags of one commit take turns.
		Expect(job.Concurrency["group"]).To(ContainSubstring("github.sha"))
		Expect(job.Concurrency["cancel-in-progress"]).To(BeFalse())
	})

	It("keeps the runs of one commit in groups of their own event", func() {
		var doc struct {
			Concurrency map[string]any `yaml:"concurrency"`
		}
		Expect(yaml.Unmarshal([]byte(workflow), &doc)).To(Succeed())
		Expect(doc.Concurrency["group"]).To(ContainSubstring("github.event_name"))
	})

	Describe("the plan step", func() {
		type result struct {
			out map[string]string
			log string
			err error
		}
		var bin string
		BeforeEach(func() {
			bin = GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(bin, "docker"), []byte(fakeDocker), 0o755)).To(Succeed())
		})
		plan := func(push, event, ref, sha string, tags map[string]string, repository string) result {
			GinkgoHelper()
			script := workflowStepScript(workflow, "build", "plan")
			dir := GinkgoT().TempDir()
			outFile := filepath.Join(dir, "out")
			Expect(os.WriteFile(outFile, nil, 0o644)).To(Succeed())
			var lines []string
			for k, v := range tags {
				lines = append(lines, k+"="+v)
			}
			cmd := exec.Command("sh", "-c", script)
			cmd.Env = append(os.Environ(),
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"PUSH="+push, "EVENT="+event, "REF="+ref, "SHA="+sha,
				"REPOSITORY="+repository, "RUNNER_TEMP="+dir, "GITHUB_OUTPUT="+outFile,
				"FAKE_TAGS="+strings.Join(lines, "\n"))
			log, err := cmd.CombinedOutput()
			b, rerr := os.ReadFile(outFile)
			Expect(rerr).NotTo(HaveOccurred())
			out := map[string]string{}
			for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				if k, v, ok := strings.Cut(l, "="); ok {
					out[k] = v
				}
			}
			return result{out: out, log: string(log), err: err}
		}
		const (
			repo = "registry.example/sandbox-guest"
			sha  = "0123456789abcdef0123456789abcdef01234567"
			d1   = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
			d2   = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
			d3   = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
		)
		shaTags := map[string]string{
			repo + ":sha-" + sha:        d1,
			repo + ":media-sha-" + sha:  d2,
			repo + ":office-sha-" + sha: d3,
		}

		It("builds every image and looks nothing up when it pushes nothing", func() {
			r := plan("false", "pull_request", "refs/pull/1/merge", sha, nil, repo+"-unreachable")
			Expect(r.err).NotTo(HaveOccurred(), r.log)
			Expect(r.out).To(HaveKeyWithValue("mode", "local"))
			for _, t := range []string{"base", "media", "office"} {
				Expect(r.out).To(HaveKeyWithValue("build_"+t, "true"))
			}
		})

		It("reuses the sha tags that exist and builds only the missing ones", func() {
			partial := map[string]string{repo + ":sha-" + sha: d1, repo + ":office-sha-" + sha: d3}
			r := plan("true", "push", "refs/heads/master", sha, partial, repo)
			Expect(r.err).NotTo(HaveOccurred(), r.log)
			Expect(r.out).To(HaveKeyWithValue("mode", "sha"))
			Expect(r.out).To(HaveKeyWithValue("build_base", "false"))
			Expect(r.out).To(HaveKeyWithValue("build_media", "true"))
			Expect(r.out).To(HaveKeyWithValue("build_office", "false"))
			Expect(r.out).To(HaveKeyWithValue("tag_media", "media-sha-"+sha))
		})

		It("never takes a tag it cannot read as missing", func() {
			r := plan("true", "push", "refs/heads/master", sha, nil, repo+"-unreachable")
			Expect(r.err).To(HaveOccurred())
			Expect(r.out).NotTo(HaveKey("build_base"))
		})

		It("gives a tag push release tags of its own, from the images of the commit when they exist", func() {
			r := plan("true", "push", "refs/tags/v1.2.3", sha, shaTags, repo)
			Expect(r.err).NotTo(HaveOccurred(), r.log)
			Expect(r.out).To(HaveKeyWithValue("mode", "release"))
			Expect(r.out).To(HaveKeyWithValue("tag_base", "v1.2.3"))
			Expect(r.out).To(HaveKeyWithValue("tag_media", "media-v1.2.3"))
			Expect(r.out).To(HaveKeyWithValue("tag_office", "office-v1.2.3"))
			Expect(r.out).To(HaveKeyWithValue("from_base", d1))
			Expect(r.out).To(HaveKeyWithValue("build_base", "false"))

			// No image of the commit: build and push the release tag.
			r = plan("true", "push", "refs/tags/v1.2.3", sha, nil, repo)
			Expect(r.err).NotTo(HaveOccurred(), r.log)
			Expect(r.out).To(HaveKeyWithValue("build_base", "true"))
			Expect(r.out).To(HaveKeyWithValue("from_base", ""))

			// A release tag that exists is kept as it is.
			r = plan("true", "push", "refs/tags/v1.2.3", sha, map[string]string{repo + ":v1.2.3": d3}, repo)
			Expect(r.err).NotTo(HaveOccurred(), r.log)
			Expect(r.out).To(HaveKeyWithValue("build_base", "false"))
			Expect(r.out).To(HaveKeyWithValue("from_base", ""))
		})

		It("refuses the release event, which builds no guest image", func() {
			r := plan("true", "release", "refs/tags/v1.2.3", sha, shaTags, repo)
			Expect(r.err).To(HaveOccurred())
			Expect(r.log).To(ContainSubstring("the release event builds no guest image"))
			Expect(r.out).NotTo(HaveKey("mode"))
			// No step waits for the images of the tag push any more.
			Expect(workflow).NotTo(ContainSubstring("mode == 'wait'"))
			Expect(workflow).NotTo(ContainSubstring("Wait for the images of the tag push"))
		})

		It("refuses a manual run on a tag", func() {
			r := plan("true", "workflow_dispatch", "refs/tags/v1.2.3", sha, shaTags, repo)
			Expect(r.err).To(HaveOccurred())
			Expect(r.log).To(ContainSubstring("not on the tag"))
			Expect(r.out).NotTo(HaveKey("mode"))
		})

		It("refuses a release tag that is not a valid image tag", func() {
			r := plan("true", "push", "refs/tags/v1.2.3+build", sha, shaTags, repo)
			Expect(r.err).To(HaveOccurred())
			Expect(r.log).To(ContainSubstring("not a valid image tag"))
		})
	})
})

var _ = Describe("the release runs of the add-on package workflow", func() {
	var workflow string
	BeforeEach(func() {
		b, err := os.ReadFile("../../../.github/workflows/addons-package.yml")
		Expect(err).NotTo(HaveOccurred())
		workflow = string(b)
	})

	type step struct {
		Name string            `yaml:"name"`
		ID   string            `yaml:"id"`
		Uses string            `yaml:"uses"`
		Run  string            `yaml:"run"`
		With map[string]string `yaml:"with"`
	}
	type job struct {
		If          string         `yaml:"if"`
		Needs       []string       `yaml:"needs"`
		Timeout     any            `yaml:"timeout-minutes"`
		Concurrency map[string]any `yaml:"concurrency"`
		Outputs     map[string]any `yaml:"outputs"`
		Steps       []step         `yaml:"steps"`
	}
	parse := func() (doc struct {
		Concurrency map[string]any `yaml:"concurrency"`
		Jobs        map[string]job `yaml:"jobs"`
	}) {
		GinkgoHelper()
		Expect(yaml.Unmarshal([]byte(workflow), &doc)).To(Succeed())
		return doc
	}

	It("builds, checks and publishes nothing on the release event", func() {
		doc := parse()
		for _, n := range []string{"validate", "sandbox-image", "package", "publish"} {
			Expect(doc.Jobs[n].If).To(ContainSubstring("github.event_name != 'release'"), "job %s runs on the release event", n)
		}
		// The publish leg no longer waits for another run: only a tag push
		// or a master push publishes, and a re-run adopts its own first push.
		script := workflowStepScript(workflow, "publish", "push")
		Expect(script).NotTo(ContainSubstring("EVENT"))
		Expect(script).NotTo(ContainSubstring("waiting for the tag push"))
		Expect(script).To(ContainSubstring("--adopt-existing"))
		Expect(doc.Jobs["publish"].Timeout).To(Equal(60))
		Expect(doc.Jobs["publish"].Concurrency["group"]).NotTo(ContainSubstring("github.event_name"))
		// The release event waits for the tag push run, so the two runs of a
		// release must not share the group of the workflow.
		Expect(doc.Concurrency["group"]).To(ContainSubstring("github.event_name"))
	})

	It("promotes on the release event the index the tag push published, after checking it in the registry", func() {
		doc := parse()
		pr := doc.Jobs["promote-release"]
		Expect(pr.If).To(ContainSubstring("github.event_name == 'release'"))
		Expect(pr.Needs).To(ConsistOf("plan", "tag-run"))
		Expect(doc.Jobs["promote"].If).To(ContainSubstring("github.event_name != 'release'"))
		verified, promoted := -1, -1
		for i, st := range pr.Steps {
			Expect(st.Uses).NotTo(HavePrefix("actions/download-artifact"), "the digest must come from the registry")
			if strings.Contains(st.Run, "addonpkg verify-published") {
				verified = i
				Expect(st.Run).To(ContainSubstring(`--revision "$SHA"`))
				Expect(st.Run).To(ContainSubstring(`--owner-repo "$OWNER_REPO"`))
			}
			if strings.Contains(st.Run, "addonpkg promote") {
				promoted = i
			}
		}
		Expect(verified).To(BeNumerically(">=", 0))
		Expect(promoted).To(BeNumerically(">", verified))
		Expect(workflowStepScript(workflow, "promote-release", "Move the aliases")).To(ContainSubstring("--digest \"$DIGEST\""))
		// The catalog comes from the same green tag push run.
		ri := doc.Jobs["release-index"]
		Expect(ri.Needs).To(ContainElements("tag-run", "promote-release"))
		// A pre-release skips promote-release and still gets its catalog; a
		// failed verification stops the catalog.
		Expect(ri.If).To(ContainSubstring("!cancelled() && !failure()"))
		Expect(ri.If).To(ContainSubstring("needs.tag-run.result == 'success'"))
		Expect(workflowStepScript(workflow, "release-index", "Download the release index of the tag push run")).To(ContainSubstring(`gh run download "$RUN"`))
		Expect(doc.Jobs["tag-run"].Outputs).To(HaveKey("run"))
	})

	Describe("the wait for the tag push run", func() {
		const sha = "0123456789abcdef0123456789abcdef01234567"
		// fakeGh answers `gh run list ... --jq <expr>` from FAKE_RUNS.
		const fakeGh = `#!/bin/sh
expr=
while [ $# -gt 0 ]; do
  [ "$1" = --jq ] && expr=$2
  shift
done
printf '%s' "$FAKE_RUNS" | jq -c "$expr"
`
		// fakeDate advances the clock by DATE_STEP seconds on every call, so
		// the grace period and the deadline pass without waiting.
		const fakeDate = `#!/bin/sh
n=$(cat "$CLOCK" 2>/dev/null || echo 1000000)
n=$((n + DATE_STEP))
echo "$n" > "$CLOCK"
echo "$n"
`
		find := func(runs string, step int) (string, string, error) {
			GinkgoHelper()
			dir := GinkgoT().TempDir()
			bin := filepath.Join(dir, "bin")
			Expect(os.MkdirAll(bin, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGh), 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(bin, "sleep"), []byte("#!/bin/sh\nexit 0\n"), 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(bin, "date"), []byte(fakeDate), 0o755)).To(Succeed())
			output := filepath.Join(dir, "out")
			Expect(os.WriteFile(output, nil, 0o644)).To(Succeed())
			cmd := exec.Command("sh", "-c", workflowStepScript(workflow, "tag-run", "find"))
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"TAG=v1.2.3", "SHA="+sha, "GITHUB_OUTPUT="+output, "FAKE_RUNS="+runs,
				"CLOCK="+filepath.Join(dir, "clock"), fmt.Sprintf("DATE_STEP=%d", step))
			log, err := cmd.CombinedOutput()
			b, rerr := os.ReadFile(output)
			Expect(rerr).NotTo(HaveOccurred())
			return string(b), string(log), err
		}
		BeforeEach(func() {
			if _, err := exec.LookPath("jq"); err != nil {
				Skip("jq is not installed")
			}
		})
		It("takes only a successful run of the same commit", func() {
			out, log, err := find(`[{"databaseId":1,"headSha":"ffffffffffffffffffffffffffffffffffffffff","status":"completed","conclusion":"success"},`+
				`{"databaseId":2,"headSha":"`+sha+`","status":"completed","conclusion":"failure"},`+
				`{"databaseId":3,"headSha":"`+sha+`","status":"completed","conclusion":"success"}]`, 1)
			Expect(err).NotTo(HaveOccurred(), log)
			Expect(out).To(Equal("run=3\n"))
		})
		It("waits the grace period for a re-run of a failed run, then fails", func() {
			out, log, err := find(`[{"databaseId":2,"headSha":"`+sha+`","status":"completed","conclusion":"failure"}]`, 300)
			Expect(err).To(HaveOccurred())
			Expect(log).To(ContainSubstring("waiting for a re-run"))
			Expect(log).To(ContainSubstring("did not succeed"))
			Expect(out).To(BeEmpty())
		})
		It("fails at the deadline when no tag push run ends", func() {
			out, log, err := find(`[{"databaseId":2,"headSha":"`+sha+`","status":"in_progress","conclusion":""}]`, 600)
			Expect(err).To(HaveOccurred())
			Expect(log).To(ContainSubstring("still running"))
			Expect(log).To(ContainSubstring("timed out waiting"))
			Expect(out).To(BeEmpty())
		})
	})

	It("pins the release catalog to the digests in the registry", func() {
		b, err := os.ReadFile("../../../scripts/addons/catalog.sh")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(ContainSubstring("--channel latest --pin-registry"))
		Expect(workflowStepScript(workflow, "catalog", "Render the entries of every add-on")).To(ContainSubstring("--channel release"))
	})

	It("checks every pinned digest and guest image before it attaches the catalog", func() {
		var doc struct {
			Jobs map[string]struct {
				Steps []struct {
					Name string `yaml:"name"`
					Run  string `yaml:"run"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		Expect(yaml.Unmarshal([]byte(workflow), &doc)).To(Succeed())
		gate, upload := -1, -1
		for i, st := range doc.Jobs["release-index"].Steps {
			if st.Name == "Check that the registry serves every pinned digest" {
				gate = i
				Expect(st.Run).To(ContainSubstring("sandbox-guest-images.txt"))
				Expect(st.Run).To(ContainSubstring("imagetools inspect"))
			}
			if strings.Contains(st.Run, "gh release upload") {
				upload = i
			}
		}
		Expect(gate).To(BeNumerically(">=", 0), "release-index has no registry check")
		Expect(upload).To(BeNumerically(">", gate), "the catalog is attached before the registry check")
		Expect(workflowStepScript(workflow, "catalog", "List the guest images of the Sandbox package")).To(ContainSubstring("sandbox-guest-images.txt"))
	})

	Describe("the registry check of the release catalog", func() {
		// gateDocker answers `docker buildx imagetools inspect --raw <ref>`:
		// a ref that contains "flaky" fails twice with a network error and
		// then succeeds, "down" always fails that way, "gone" is not found.
		const gateDocker = `#!/bin/sh
ref=$5
case "$ref" in
*gone*) echo "ERROR: $ref: not found" >&2; exit 1 ;;
*down*) echo "ERROR: dial tcp: i/o timeout" >&2; exit 1 ;;
*flaky*)
  n=$(cat "$STATE" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" > "$STATE"
  [ "$n" -ge 3 ] && exit 0
  echo "ERROR: dial tcp: connection reset" >&2; exit 1 ;;
esac
exit 0
`
		gate := func(index, guests string) (string, error) {
			GinkgoHelper()
			dir := GinkgoT().TempDir()
			bin := filepath.Join(dir, "bin")
			Expect(os.MkdirAll(filepath.Join(dir, "index"), 0o755)).To(Succeed())
			Expect(os.MkdirAll(bin, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(bin, "docker"), []byte(gateDocker), 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(bin, "sleep"), []byte("#!/bin/sh\nexit 0\n"), 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(dir, "index", "addons-index.yaml"), []byte(index), 0o644)).To(Succeed())
			if guests != "" {
				Expect(os.WriteFile(filepath.Join(dir, "index", "sandbox-guest-images.txt"), []byte(guests), 0o644)).To(Succeed())
			}
			cmd := exec.Command("sh", "-c", workflowStepScript(workflow, "release-index", "Check that the registry serves every pinned digest"))
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "STATE="+filepath.Join(dir, "state"))
			out, err := cmd.CombinedOutput()
			return string(out), err
		}
		const digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
		entry := func(repo string) string {
			return "- id: \"x\"\n  artifact: \"oci://" + repo + ":x-1.2.3\"\n  digest: \"" + digest + "\"\n"
		}

		It("passes when every digest is served, after retrying a dropped connection", func() {
			out, err := gate(entry("registry.example/addons")+entry("registry.example/flaky"), "registry.example/guest@"+digest+"\n")
			Expect(err).NotTo(HaveOccurred(), out)
			Expect(out).To(ContainSubstring("ok registry.example/flaky@" + digest))
			Expect(out).To(ContainSubstring("ok registry.example/guest@" + digest))
		})

		It("fails on a guest image the registry does not serve", func() {
			out, err := gate(entry("registry.example/addons"), "registry.example/gone@"+digest+"\n")
			Expect(err).To(HaveOccurred())
			Expect(out).To(ContainSubstring("does not serve registry.example/gone@"))
		})

		It("fails when a digest cannot be checked", func() {
			out, err := gate(entry("registry.example/down"), "")
			Expect(err).To(HaveOccurred())
			Expect(out).To(ContainSubstring("cannot check registry.example/down@"))
		})
	})
})
