# Preparing a Release

A release is not finished when the tag is pushed. The GitHub release, the blog post and the demo clips ship together, because the changelog says what moved and the post and the clips are what make anyone care.

## What a release must include

1. **Labels on the merged PRs.** GitHub generates the raw notes from PR labels, so label first, generate second. Wrong labels mean a miscategorised changelog that has to be edited by hand.
2. **`RELEASE_NOTES_vX.Y.Z.md`** at the repository root, in the house style: what changed, why it matters, PR numbers so people can read the diffs.
3. **A blog post under `website/content/blog/`.** One post per release, front matter with `title`, `date`, `author`, `category: "Release"`, `tags`, `summary` and `extracss: ["blog.css"]`. Cover the two or three changes that alter what a user does day to day, not the whole changelog, and link the PR numbers. See `website/content/blog/what-landed-in-localai-4-8.md` for the shape.
4. **Demo clips for the notable features.** Anything visible (a new backend, a UI change, a new endpoint, a measured speedup) gets a short screen recording. Put the file in `website/static/media/`, reference it from the blog post, and reuse it on the marketing pages where it fits.

A release without a post and without clips is incomplete, in the same way a user-facing code change without a docs update is incomplete.

## Clip conventions

- MP4, H.264, no audio track unless the feature is about audio. Keep them short (10 to 30 seconds) and loopable.
- Record the real thing. A clip from the engine's own benchmark suite or a real session, never a mockup.
- Where the change is a speedup, record both sides on the same machine on the same input, so the comparison is honest.
- Name the file after the feature, not the release (`vllm-race.mp4`, not `v4-8-demo.mp4`), so it stays reusable once the release is old.
- The marketing site plays clips with `muted loop playsinline preload="none"` and a `data-lazy` attribute, which the site's IntersectionObserver uses to play and pause them on scroll. Follow that pattern for anything you add.

## Order of work

Label the PRs, generate and edit the release notes, cut the draft release, record the clips while the branch is still fresh in your head, then write the post against the notes and the clips. Publishing the release and merging the post should happen on the same day.

The `creating-localai-releases` skill drives steps 1 to 3 and captures the React UI screenshots that go into the notes.
