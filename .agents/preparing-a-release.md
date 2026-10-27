# Preparing a Release

A release is not finished when the tag is pushed. The GitHub release, the blog post and the demo clips ship together, because the changelog says what moved and the post and the clips are what make anyone care.

## What a release must include

1. **Labels on the merged PRs.** GitHub generates the raw notes from PR labels, so label first, generate second. Wrong labels mean a miscategorised changelog that has to be edited by hand.
2. **`RELEASE_NOTES_vX.Y.Z.md`** at the repository root, in the house style: what changed, why it matters, PR numbers so people can read the diffs.
3. **A blog post under `website/content/blog/`.** One post per release, front matter with `title`, `date`, `author`, `category: "Release"`, `tags`, `summary` and `extracss: ["blog.css"]`. Cover the two or three changes that alter what a user does day to day, not the whole changelog, and link the PR numbers. See `website/content/blog/what-landed-in-localai-4-8.md` for the shape.
4. **Demo clips for the notable features.** Anything visible (a new backend, a UI change, a new endpoint, a measured speedup) gets a short screen recording. Put the file in `website/static/media/`, reference it from the blog post, and reuse it on the marketing pages where it fits.

A release without a post and without clips is incomplete, in the same way a user-facing code change without a docs update is incomplete.

## Add-ons and the add-on catalog

The first-party add-ons (Tables, Voice, Face, Fine-tuning, Quantization, Studio, Talk, Office, Sandbox) are separate OCI packages built by `.github/workflows/addons-package.yml`, not part of the LocalAI binary. A release ships them with the tag. A release build of LocalAI reads the add-on index attached to its own release and installs the packages that tag built; it must never fall back to the mutable development builds signed by master.

What happens, and what to check:

1. **Tag push (`v<semver>`).** The workflow packages every add-on for every platform enabled in `addons/<id>/package/platforms.yaml`, pushes `<id>-<version>` to the add-on registry, signs it with the workflow identity of the tag, and the `catalog` job renders `addons-index.yaml` (one pinned entry for each add-on and platform) as the artifact `addons-release-index`. No alias moves. Check that every `publish and sign` leg and the `render the release catalog` job are green, and that the entry count is the number of add-ons times their enabled platforms.
2. **Sandbox guest image.** The `sandbox-image` job builds and signs the guest image; the Sandbox package is stamped with its digest (`SANDBOX_GUEST_IMAGE`). A publishing run fails when the digest is missing. Check the digest in the job summary and that the Sandbox page of a test install shows a guest image.
3. **Release published (stable, not a draft).** The `promote` job moves `<id>-latest`, and the `attach the catalog to the release` job downloads `addons-release-index` from the tag run and uploads it to the release as `addons-index.yaml`. Release builds read that URL: `https://github.com/mudler/LocalAI/releases/download/v<version>/addons-index.yaml`. A release without this asset has no installable add-ons. The artifact is kept for 90 days, so publish the release within that time of the tag, or re-run the tag workflow.
4. **Platforms.** A platform is enabled only after its native check passes on a runner of that platform. `scripts/addons/catalog.sh --check` fails the PR when `gallery/addons/index.yaml` does not list every enabled platform.
5. **Smoke test.** On a clean data directory, start the release binary, open Add-ons, Find add-ons, and install Studio. It must resolve to the `<id>-<version>` tag and verify against the identity of the tag. Repeat on macOS and on linux/arm64 when the release covers them.
6. **Sources.** The release binary and the container image read the model and backend libraries (see the cutover steps in `release.yaml` and the `LOCALAI_*_SOURCE` build arguments in the `Dockerfile`).

The development entries in `gallery/addons/index.yaml` are regenerated with `sh scripts/addons/catalog.sh --channel development --repository quay.io/go-skynet/local-ai-addons --out gallery/addons/index.yaml`; they do not need a change per release.

## Clip conventions

- MP4, H.264, no audio track unless the feature is about audio. Keep them short (10 to 30 seconds) and loopable.
- Record the real thing. A clip from the engine's own benchmark suite or a real session, never a mockup.
- Where the change is a speedup, record both sides on the same machine on the same input, so the comparison is honest.
- Name the file after the feature, not the release (`vllm-race.mp4`, not `v4-8-demo.mp4`), so it stays reusable once the release is old.
- The marketing site plays clips with `muted loop playsinline preload="none"` and a `data-lazy` attribute, which the site's IntersectionObserver uses to play and pause them on scroll. Follow that pattern for anything you add.

## Order of work

Label the PRs, generate and edit the release notes, cut the draft release, record the clips while the branch is still fresh in your head, then write the post against the notes and the clips. Publishing the release and merging the post should happen on the same day.

The `creating-localai-releases` skill drives steps 1 to 3 and captures the React UI screenshots that go into the notes.
