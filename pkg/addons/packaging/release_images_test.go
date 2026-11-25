// SPDX-License-Identifier: MIT
package packaging

import (
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
		// The release event waits in this workflow for the tag push; in one
		// group with it, the tag push would queue behind the wait.
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

		It("builds nothing for the release event and waits for the release tags", func() {
			r := plan("true", "release", "refs/tags/v1.2.3", sha, nil, repo)
			Expect(r.err).NotTo(HaveOccurred(), r.log)
			Expect(r.out).To(HaveKeyWithValue("mode", "wait"))
			for _, t := range []string{"base", "media", "office"} {
				Expect(r.out).To(HaveKeyWithValue("build_"+t, "false"))
			}
			Expect(r.out).To(HaveKeyWithValue("tag_base", "v1.2.3"))
			Expect(workflow).To(ContainSubstring("if: steps.plan.outputs.mode == 'wait'"))
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

	It("lets the release event adopt the index of the tag push and never publish first", func() {
		script := workflowStepScript(workflow, "publish", "push")
		wait := strings.Index(script, `if [ "$EVENT" = release ]; then`)
		inspect := strings.Index(script, `imagetools inspect --raw "$target"`)
		publish := strings.Index(script, "addonpkg publish")
		Expect(wait).To(BeNumerically(">=", 0), "the push step does not wait on a release event")
		Expect(inspect).To(BeNumerically(">", wait))
		Expect(publish).To(BeNumerically(">", inspect), "the release event must wait before it publishes")
		Expect(script).To(ContainSubstring("--adopt-existing"))
	})

	It("keeps the publish legs of the two runs of a release in separate groups", func() {
		var doc struct {
			Jobs map[string]struct {
				Concurrency map[string]any `yaml:"concurrency"`
			} `yaml:"jobs"`
		}
		Expect(yaml.Unmarshal([]byte(workflow), &doc)).To(Succeed())
		// The release leg waits for the tag push leg to publish; in one group
		// the tag push leg would be pending behind that wait.
		Expect(doc.Jobs["publish"].Concurrency["group"]).To(ContainSubstring("github.event_name"))
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
