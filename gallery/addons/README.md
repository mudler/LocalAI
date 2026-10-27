# First-party add-on catalog

This directory holds the template of the catalog entries of the first-party add-ons.
It holds no live URL, registry or namespace. The registry and repository are inputs of
the release workflow (`ADDON_OCI_REPOSITORY`). Each first-party add-on has its own
template (`tables`, `voice`, `face`, `fine-tuning`, `quantization`, `studio`, `talk`, `office`, `sandbox`) and its own release
platform matrix, `addons/<id>/package/platforms.yaml`. The templates differ in `name:` only.

## Generate the index

`gallery/addons/index.yaml` is generated from the platform files and the manifests; never edit
it by hand. It has one development entry (mutable, no digest) for each enabled platform of
each add-on:

```sh
sh scripts/addons/catalog.sh --channel development \
  --repository quay.io/go-skynet/local-ai-addons --out gallery/addons/index.yaml
```

CI runs `sh scripts/addons/catalog.sh --channel development --check` and fails when the file is stale.
Run it after you enable a platform in `addons/<id>/package/platforms.yaml`, change a
manifest (title, description, main page, tools) or add an add-on template.

The release index, pinned by digest and signed by the run of the tag, is rendered by the
package workflow from the merged layouts it pushed:

```sh
sh scripts/addons/catalog.sh --channel release --repository <repo> --version <semver> \
  --signer-ref refs/tags/v<semver> --merged-root <dir with one merged layout for each add-on id> \
  --out addons-index.yaml
```

## Render entries of one add-on

With `--from-manifest` in place of `--merged`, the command renders the development channel from the manifest and the enabled platforms, without a built package.

```sh
go run ./scripts/addons/cmd/addonpkg catalog \
  --config addons/tables/package/platforms.yaml \
  --template gallery/addons/tables.yaml.tmpl \
  --merged <merged layout> \
  --repository <registry>/<namespace>/<repository> \
  --tag <immutable tag without the id prefix: the version or sha-<12 hex>> \
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

Use `face`, `fine-tuning`, `quantization`, `studio`, `talk`, `office` or `sandbox` in place of `voice` for the other packages.
All packages share one repository, so pass the same repository for each. The artifact of an
entry is `oci://<repository>:<id>-<tag>`, for example `...:voice-sha-50f0f3de26d2`.

The add-on packages live in `quay.io/go-skynet/local-ai-addons`; the template itself holds
no registry value. The result is a YAML list with one entry for each enabled platform. Each entry:

- pins the digest of the platform image, so a tag that moves later changes nothing;
- names `oci://<repository>:<id>-<tag>` for the immutable tag of that add-on;
- carries the channel it is current for and the version of the package, so the
  channel is metadata that maps to a digest;
- states the signature policy: signature required, issuer
  `https://token.actions.githubusercontent.com`, identity
  `https://github.com/<owner>/<repository>/.github/workflows/addons-package.yml@<signer ref>`.

The command fails when the repository is empty or a backend image repository, when the
channel is not a channel alias, when a platform of the matrix has no image, or when the
template holds an unknown placeholder or weakens the policy.

Every entry also carries `toolCount` and `tools`: the chat tools the package declares in
`capabilities.tools` (admin tools are not counted), at most 32 names, and the full count.
An add-on without chat tools has neither field. To add them to an existing index without a
merged layout, read the manifests of the repository:

```sh
go run ./scripts/addons/cmd/addonpkg catalog-tools --index gallery/addons/index.yaml --addons addons
```

It changes no other field, so no digest or artifact moves.

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
