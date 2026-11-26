# audio.cpp patches

The `audio.cpp` target of `../Makefile` applies every `*.patch` file in this
directory to the audio.cpp checkout, in name order, right after it checks out
`AUDIO_CPP_VERSION`. The patches are applied with `git apply` from the root of
the checkout, so the paths in them start at the audio.cpp root (for example
`external/ggml/src/...`).

Put a patch here only for a fix that upstream audio.cpp does not have yet.
Name it `NNNN-short-description.patch` and make it with `git format-patch`, so
the commit message says what it fixes and where the upstream fix is.

If a patch does not apply, the build stops with an error. After a bump of
`AUDIO_CPP_VERSION`, update the patch or remove it. Remove each patch as soon
as the pinned audio.cpp commit contains the fix.
