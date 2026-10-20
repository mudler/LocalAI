# First-party add-on catalog

This directory holds the template of the catalog entries of the first-party add-ons.
It holds no live URL, registry or namespace. The registry and repository are inputs of
the release workflow (`ADDON_OCI_REPOSITORY`). Each first-party add-on has its own
template (`tables`, `voice`, `face`, `fine-tuning`, `quantization`) and its own release
platform matrix, `addons/<id>/package/platforms.yaml`. The templates differ in `name:` only.

## Render entries

```sh
go run ./scripts/addons/cmd/addonpkg catalog \
  --config addons/tables/package/platforms.yaml \
  --template gallery/addons/tables.yaml.tmpl \
  --merged <merged layout> \
  --repository <registry>/<namespace>/<repository> \
  --tag <immutable tag: the version or sha-<12 hex>> \
  --channel <latest | development | branch-...> \
  --owner-repo <owner>/<repository> \
  --signer-ref <refs/tags/vX.Y.Z | refs/heads/master> \
  --out <catalog.yaml>
```

To render the entries of another package, name its matrix and its template. For example,
for Voice:

```sh
go run ./scripts/addons/cmd/addonpkg catalog \
  --config addons/voice/package/platforms.yaml \
  --template gallery/addons/voice.yaml.tmpl \
  --merged <merged layout of voice> ... # the other flags as above
```

Use `face`, `fine-tuning` or `quantization` in place of `voice` for the other packages.
Each package has its own repository, so pass the repository of that package.

The add-on packages live in `quay.io/go-skynet/local-ai-addons`; the template itself holds
no registry value. The result is a YAML list with one entry for each enabled platform. Each entry:

- pins the digest of the platform image, so a tag that moves later changes nothing;
- names `oci://<repository>:<tag>` for the immutable tag;
- carries the channel it is current for and the version of the package, so the
  channel is metadata that maps to a digest;
- states the signature policy: signature required, issuer
  `https://token.actions.githubusercontent.com`, identity
  `https://github.com/<owner>/<repository>/.github/workflows/addons-package.yml@<signer ref>`.

The command fails when the repository is empty or a backend image repository, when the
channel is not a channel alias, when a platform of the matrix has no image, or when the
template holds an unknown placeholder or weakens the policy.

## Placeholders

`{{addon}}`, `{{version}}`, `{{tag}}`, `{{channel}}`, `{{repository}}`, `{{digest}}`,
`{{os}}`, `{{arch}}`, `{{issuer}}`, `{{identity}}`, `{{title}}`, `{{description}}`,
`{{main_page}}`, `{{icon}}`. The title, description, main page and icon come from the
verified manifest of the package.

## Signature policy of the library

The host replaces the policy of an entry with the policy of the library (source) that
serves it. Configure the library with signature required, the issuer above and the
identity of the entries. The identity is an exact string and carries the ref of the
signing run, so one library names the identity of one release ref. A pattern is not
supported by the library policy today.
