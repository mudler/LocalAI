# Backend image signing & verification

LocalAI verifies backend OCI images against a per-gallery keyless-cosign
policy. This page documents the trust model, the producer side
(`.github/workflows/backend_merge.yml` in this repo), and the consumer
side (`pkg/oci/cosignverify` plus the gallery YAML).

## Trust model

- **Producer:** `.github/workflows/backend_merge.yml` signs each pushed
  manifest list with `cosign sign --recursive` in keyless mode after
  `docker buildx imagetools create`. The signing cert is issued by
  Fulcio bound to the workflow's OIDC identity. There is no long-lived
  signing key. `--recursive` signs both the manifest list and every
  per-arch entry — needed because our consumer resolves a tag to a
  per-arch manifest before checking signatures.
- **Storage:** Signatures are written as OCI 1.1 referrers
  (`--registry-referrers-mode=oci-1-1`) in the new Sigstore bundle format
  (current cosign releases do this by default; no `--new-bundle-format`
  flag). No `:sha256-<hex>.sig` tag clutter.
- **Consumer:** `pkg/oci/cosignverify` discovers the bundle via the
  referrers API, hands it to `sigstore-go`, and verifies it against the
  policy declared in the gallery YAML (`Gallery.Verification`).
- **Revocation:** Keyless cosign certs are ephemeral (10-minute Fulcio
  validity), so revocation is policy-side, not CA-side. The gallery's
  `verification.not_before` (RFC3339) is the kill-switch — advance it to
  invalidate every signature produced before a known compromise window.

## Producer setup

`backend_merge.yml` is the workflow that joins per-arch digests into the
multi-arch manifest list users actually pull, so it's also the right place
to sign. The job needs:

- `permissions: { id-token: write, contents: read }` at the job level so
  the runner can exchange its GitHub OIDC token for a Fulcio cert.
- `sigstore/cosign-installer@v3` step (current cosign releases already
  default to the new bundle format).
- After each `docker buildx imagetools create`, resolve the resulting
  list digest with `docker buildx imagetools inspect <tag> --format
  '{{.Manifest.Digest}}'` and sign:

```sh
cosign sign --yes --recursive \
  --registry-referrers-mode=oci-1-1 \
  "${REGISTRY_REPO}@${DIGEST}"
```

Sign by digest, never by tag — signing by tag binds the signature to
whatever the tag points at *now*, and a subsequent tag push orphans it.

`--registry-referrers-mode=oci-1-1` is still gated behind
`COSIGN_EXPERIMENTAL=1` in cosign v2.4.x (set at the job env level in
`backend_merge.yml`). Re-evaluate when bumping the pinned cosign release
— newer versions are expected to graduate this flag and the env var can
then be dropped.

`backend_build_darwin.yml` builds and pushes single-arch darwin images
that bypass the manifest-list merge. If/when those entries get a gallery
`verification:` policy, the equivalent cosign step has to land there
too.

## Consumer setup (in `mudler/LocalAI` gallery YAML)

Once CI is signing, add a `verification:` block to the backend gallery
entry (`backend/index.yaml`):

```yaml
- name: localai
  url: github:mudler/LocalAI/backend/index.yaml@master
  verification:
    issuer: "https://token.actions.githubusercontent.com"
    identity_regex: "^https://github\\.com/mudler/LocalAI/\\.github/workflows/backend_merge\\.yml@refs/heads/master$"
    # Optional revocation cutoff; advance during incident response.
    # not_before: "2026-06-01T00:00:00Z"
```

Identity matching pins the OIDC subject Fulcio issued the signing cert
to. Without this, any image signed by *anyone* with a Fulcio cert would
pass — the regex is what makes a signature mean "produced by our CI".

## Strict mode

Default behaviour: OCI backends without a `verification:` block install
with a warning (logs include `installing OCI backend without signature
verification`). Tarball/HTTP backends without a `sha256` field log a
similar warning.

For production, set `LOCALAI_REQUIRE_BACKEND_INTEGRITY=1` (or pass
`--require-backend-integrity` to `local-ai run` / `local-ai backends
install` / `local-ai models install`). The warning becomes a hard error
and unverifiable backends refuse to install.

## Revocation playbook

If `backend_merge.yml` (or any workflow with `id-token: write`) is
compromised and we've shipped malicious signed images:

1. **Identify the compromise window.** Find the earliest IntegratedTime
   from the bad signatures (Rekor search by `subject` filter).
2. **Set `verification.not_before`** in `backend/index.yaml` to a
   timestamp just *after* that window's start.
3. **Push the YAML.** Deployed LocalAI instances pick it up on next
   gallery refresh (1-hour cache in `core/gallery/gallery.go`).
4. **Fix the underlying compromise** in the workflow and re-sign images
   with the new build, which will have IntegratedTime > `not_before`.
5. **Optional:** for absolute decisiveness, also rotate to a new
   workflow path (`backend_merge_v2.yml`) and update `identity_regex`.

## Where the code lives

- `pkg/oci/cosignverify/` — verifier, policy, OCI referrer fetch, NotBefore enforcement.
- `pkg/downloader/uri.go` — `WithImageVerifier` option threaded through `DownloadFileWithContext`.
- `core/gallery/backends.go` — `backendDownloadOptions` builds the verifier from the gallery's policy.
- `core/config/gallery.go` — `Gallery.Verification` YAML schema.
- `core/cli/run.go`, `core/cli/backends.go`, `core/cli/models.go` — `--require-backend-integrity` flag propagation.
- `.github/workflows/backend_merge.yml` — producer-side `cosign sign --recursive` after each multi-arch manifest list push.

## Out of scope (follow-ups)

- **Signing the gallery YAML itself.** The index is fetched over HTTPS
  from GitHub; we trust the host. A cosign blob signature on the YAML
  would close that gap but adds key-management overhead. Revisit this
  page if/when added.
- **Tarball/HTTP backend signing.** Cosign can sign arbitrary blobs, but
  for now non-OCI backends keep using the `sha256:` field in YAML.
