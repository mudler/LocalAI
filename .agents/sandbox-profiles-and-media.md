# Sandbox profiles and media tools

This guide is for work on three linked things: the sandbox profiles of the Sandbox add-on, the media tools of the LocalAI client, and the built-in skills that use both. They share one structure. Follow it, so a new profile, tool or skill does not drift from the others.

Read these first. This guide does not copy them:

- [.agents/localai-assistant-mcp.md](localai-assistant-mcp.md): the client tools, the coverage test, the tool budget, the built-in skills.
- [.agents/api-endpoints-and-auth.md](api-endpoints-and-auth.md): a tool exists only when an API route exists.
- [.agents/distributed-state.md](distributed-state.md): the images are kept per node, the choice of a chat and the policy are shared.
- [.agents/coding-style.md](coding-style.md): code and docs style.
- [docs/content/addons/contract-reference.md](../docs/content/addons/contract-reference.md), section "Sandbox profiles": the user view of profiles.

The model chain is: the add-on declares profiles, the host shows them and applies the policy, a skill may name the profile it needs, and the client tools make the media that the skill then reworks in the sandbox.

## 1. Add or change a sandbox profile

A profile is a name, a description, limits (CPUs, memory, disk) and a list of tools. One table is the single source: `Declared()` in `addons/sandbox/internal/profiles/profiles.go`. The manifest, the note for the model, the image check and the docs table all come from it. Do not write a program list anywhere else.

Checklist:

- [ ] **Table.** Add the profile to `Declared()` in `addons/sandbox/internal/profiles/profiles.go`. Add an ID constant, and a tool function when the profile adds tools. Each tool has a name, a detail and at least one check command. The first profile must stay `base`. Build a profile on the one before it (`mediaTools()` extends `baseTools()`). An ID must pass `contract.ValidProfileID` (`pkg/addons/contract/execution.go`).
- [ ] **Limits and disk numbers.** Set `CPUs`, `MemoryMiB` and `DiskMiB` in the same entry. Say in the commit why the numbers fit the tools (for example a profile with a compiler toolchain needs more memory and disk). The host caps (`memory_mib`, `disk_mib` of the policy) only lower them.
- [ ] **Manifest.** Run `UPDATE_MANIFEST=1 go test ./addons/sandbox/internal/tools/` to write the `execution.profiles` list of the `bash` tool into `addons/sandbox/manifest.json` (spec: "declares the tools of the runtime in the manifest"). Never edit the profile list in the manifest by hand. `pkg/addons/contract/execution_test.go` ("profiles") checks the shape.
- [ ] **Docs table.** Run `UPDATE_DOCS=1 go test ./addons/sandbox/internal/profiles/`. This rewrites the block between `<!-- sandbox-profiles:start -->` and `<!-- sandbox-profiles:end -->` in `docs/content/addons/contract-reference.md`. `TestDocumentationListsTheProfilesOfTheManifest` fails when the block is stale. Update the prose of the section "Sandbox profiles" in the same file when behaviour changes (the docs-with-code rule).
- [ ] **Tool note.** Do not write it. `ProfileNote`, `ProfileToolsSentence` and `MissingToolNote` in `core/services/studio/sandbox/profiles.go` build the prompt note, the tool description and the "program not found" line from the declared profile. `core/services/studio/sandbox/profiles_test.go` (`TestProfileNoteNamesTheProfileItsToolsAndTheOthers`, `TestMissingToolNoteNamesTheProfileAndThoseWithMore`) covers them. Keep core free of profile names and tool lists.
- [ ] **Dockerfile target.** Add one userland stage `<id>-tools` built on the stage before it, and one final target `<id>`, in `addons/sandbox/images/guest/Dockerfile`. The target name equals the profile ID. Install packages on the build network, before the guest proxy settings. Every target repeats the short finish block. Pin versions of anything not from the distribution.
- [ ] **Check-tools list.** Each tool in the table needs a check command. `addons/sandbox/images/guest/check-tools.sh <profile>` reads the profile from the manifest and fails the build when a tool has no check or a check fails. Call it in the new stage. `check-tools_test.sh` tests the script with a fake manifest. A tool that needs a check name that is not the binary name must be checked by its real binary name.
- [ ] **package.sh and digest variable.** The release stamps each image digest into the executable. The base image is `SANDBOX_GUEST_IMAGE` (`-X main.guestImage`). Every other profile has `SANDBOX_GUEST_IMAGE_<ID>` and reaches the executable through `-X main.profileImages=<id>=<ref>,...` (parsed by `profiles.ParseImages`, test `TestParseImages`). `scripts/addons/package.sh` lists the variables in a loop (`for pair in media:...`): add the new one there, and a case in `scripts/addons/package_test.sh`. A profile without an image is not offered by that release. Gotcha: `package.sh` and the two workflows hardcode the profile names (today only `media` besides `base`), so a new profile needs an edit in each of them; nothing derives them from the table.
- [ ] **CI workflow.** In `.github/workflows/sandbox-images.yml` add a build step for the target (`target: <id>`, tag `${{ env.REPOSITORY }}-<id>:sha-${{ github.sha }}`), a `steps.<id>` digest check in "Name the images by digest", a job output and a `workflow_call` output (`image_<id>`), and the line in the step summary. In `.github/workflows/addons-package.yml` pass `SANDBOX_GUEST_IMAGE_<ID>` to the package step. The registry repositories must allow anonymous pulls. The workflow header comment lists the variables: update it.
- [ ] **Size and disk numbers.** Measure the pushed image (compressed and unpacked) and the disk a sandbox needs. Put the numbers in the commit message or the PR text. `DiskMiB` must hold the unpacked image plus the work space.
- [ ] **Docs and UI.** The picker and the admin view read the manifest, so they need no change for a new profile. Check `docs/content/addons/contract-reference.md` ("Sandbox profiles", "the guest images") and `docs/content/features/chats.md` still say the truth.

Delivery rules. They live in `addons/sandbox/internal/profiles/delivery.go`. Keep them when you change that code. The tests are in `delivery_test.go`:

| Rule | Test |
|---|---|
| Pull on first use, and report the progress through the status tool | `TestFirstUsePullsAndReportsProgress` |
| A missing or failed profile falls back to `base`, and the output of the call says so | `TestMissingOrFailedProfileFallsBackToBaseWithANotice`, `TestAWaitThatRunsOutFallsBack`, `TestFailsWhenBaseCannotBeHadEither` |
| The cache keeps the current and the previous digest, and deletes older ones once no sandbox runs them | `TestKeepsCurrentAndPreviousDigestAndDeletesOlderOnes` |
| An admin pin runs the previous image | `TestAdminPinRunsThePreviousImage` |
| After an upgrade, pre-pull only the profiles this host used before | `TestPrepullPullsOnlyProfilesUsedBefore` |
| Host caps lower the limits | `TestHostCapsLowerTheLimits` |
| No cache means the host backend and no pull | `TestNoCacheMeansTheHostBackendAndNoPull` |

Access policy. The owner host setting `sandbox_profiles` (`core/services/addons/hostsettings/policy.go`, rules in `pkg/sandboxpolicy/policy.go`) is open by default: with no policy every user may use every profile, and `base` is always allowed. With no `default` in the policy a new chat gets `media` (`sandboxpolicy.InitialProfile`); a missing profile or a failed pull falls back to `base`. A restriction is an opt-in tightening. Do not add a new profile that needs a policy entry to work. The chat side is in `core/services/studio/sandboxprofile.go` and `sandboxtools.go`, tested in `core/services/studio/sandboxprofile_test.go`. The setting is also a settings control, so follow the owner-setting rules of [.agents/distributed-state.md](distributed-state.md). The `sandbox_profile` model tool is one merged tool with the actions `list` and `use`. A new profile adds no tool.

## 2. Add a media-capable tool or argument

The client tools live in `core/services/localaitools/`. The general steps are in [.agents/localai-assistant-mcp.md](localai-assistant-mcp.md), "The client tools" and "Keeping the client in step with the API". This section adds the media rules.

Checklist:

- [ ] **The API comes first.** A tool argument exists only when the HTTP route accepts it. Read the handler and send the field names it reads. `media_parity_test.go` ("media tool arguments match the API") checks the field names against a stand-in server. Add a spec there for each new argument.
- [ ] **Typed file arguments.** An image, audio or video argument is a reference. A `FileStore` (`files.go`) resolves it: in a chat, the fenced store of `core/services/studio/clientfiles.go`. The tool reads the bytes, checks the type and size, and sends them as the route expects (base64 or multipart). Never put file bytes in an argument or a result. Refuse a wrong type, a missing file and too many files with a message that names the argument (`ref_images[0]`).
- [ ] **Results.** The tool writes the result through `FileStore.Write`. In a chat it goes to the `generated/` folder of the workspace (`clientfiles.go`). The tool returns the reference (a workspace path), never raw bytes and never a host path. A skill can then pass that reference to another tool or to the shell.
- [ ] **No raw paths, no free-form `params`.** Do not forward a map from the model. Where the route forwards a `params` map to the backend, the tool takes an allow-list of keys, each with a validator for short scalars (see `transformParamNames` and `checkTransformParams` in `transformaudio.go`). A key outside the list is refused, and the error lists the allowed keys. A value is a bool, a number in a range, a short word or a short text. It is never a path or a URL. When a route returns files by URL (stems), fetch only from the known output folder of the same server, and bound the count.
- [ ] **One tool or an action.** Operations that share state in one domain are one tool with an `action` enum and one flat schema (`voice`, `face`, `voice_profile`). Media tools that are unrelated stay single-purpose (`upscale_image`, `transform_audio`). A route that is a new model use case gets its own tool.
- [ ] **Table row.** Add the `Tool*` constant and a row to `ClientTools` in `core/services/localaitools/clienttools.go`: tool, route, feature, model flag, capability name. `clienttools_test.go` checks the row against `auth.RouteFeatureRegistry`. Add the capability name to the `list_models` synonyms (`inference_media_test.go`, "list_models capability names of the media tools") and keep the `model` argument pointer to `list_models`.
- [ ] **Client, DTOs, handlers.** Add the method to `LocalAIClient` (`client.go`), the DTOs to `dto_client.go`, the shared code to `inference.go` (or its own file as `transformaudio.go`), the registration to `tools_client.go`, and the `inproc` and `httpapi` implementations. Add the dispatch case to `server_test.go`.
- [ ] **Coverage test and exemptions.** The Ginkgo spec "Client tool coverage of the inference routes" (`clientcoverage_test.go`) fails until every inference route has a row or an entry in `clientToolExemptions`. An exemption has a reason. Use "not built yet: ..." for a wanted tool. Remove the entry when the tool lands. A route cannot be both.
- [ ] **Context budget.** The client group is sent on every turn. `clientBudgetBytes` in `clienttools_merge_test.go` caps the bytes of its `tools/list` entries. Short descriptions first. Raise the bound only when the new tool or argument maps to an API field and cannot be merged or trimmed, and write the reason as a paragraph in the comment above the constant: from and to values, what was added, bytes measured, why it is not a merge candidate. Say the same in the commit message. An argument of an existing route costs bytes but no new tool, so prefer it to a new tool.
- [ ] **Docs, skill and swagger.** Update in the same change: `docs/content/features/localai-assistant.md` (tool table and the paragraph of the tool), the built-in skill `core/services/studio/builtinskills/tree/localai/SKILL.md` (the spec "the localai skill" in `localaiskill_test.go` checks every tool and parameter against `tools/list`, and caps the size of the skill at 6,600 bytes), and `core/services/localaitools/prompts/20_tools.md` when it lists the tool. If you change an HTTP route or its request, update its swagger annotations and run `make swagger`. Commit the generated files.
- [ ] **Non-admin test.** A normal user must be able to use the tool. Write a spec with `auth.RoleUser` (see `media_parity_test.go` and the "tells a user without the images permission which capability is refused" spec in `inference_media_test.go`). The tool must name the capability when the feature is refused.
- [ ] **Read-only flavour.** A tool that writes files or makes new content must be left out of the read-only flavour (`--read-only`). See the spec "registers no client tool that writes files or makes content in the read-only flavour" in `clienttools_test.go`. Add the tool to the list in `docs/content/features/localai-assistant.md`.

## 3. Add a built-in skill that needs a profile

Built-in skills are in `core/services/studio/builtinskills/tree/<name>/SKILL.md`. The example is `assemble-media` (profile `media`).

Checklist:

- [ ] **Frontmatter.** `name`, a `description` that says when to use it, and `sandbox: <profile>`. The `sandbox:` value is a hint only. `skills.SandboxHint` (`core/services/studio/skills/parse.go`) tells the model that the skill wants another profile and that it may offer `sandbox_profile` with action `use`. Nothing switches the profile by itself. Do not write a skill that restarts the sandbox, and do not tell the model to switch without asking the person. Tests: `core/services/studio/skills/parse_test.go` (`Sandbox`, `SandboxHint`) and "the sandbox hint of a skill" in `core/services/studio/sandboxprofile_test.go`. An unknown profile gives no hint.
- [ ] **Run every command.** Each command and recipe in the skill must be run in a container built from the image of that profile, and its output checked (for a picture or a video, look at the frame). Write only what passed. State the limits that the guest has (no network except the proxy, no package installs).
- [ ] **Protected name.** Add the name to `protectedSkills` in `pkg/addons/contract/skills.go`, so no add-on replaces it. `pkg/addons/contract/skills_test.go` checks that every built-in skill is protected or replaceable. Add the name to the `names` list in `core/services/studio/builtinskills/builtinskills_test.go`. That list drives the parse, the NOTICE, the "no internal names" and the "resolves for a user" specs.
- [ ] **Size bounds.** A skill file must stay under `MaxFileSize` (64 KiB, `core/services/studio/skills/parse.go`). Keep it short and put long reference material in a separate file the model reads on demand. The skill index is part of the admin turn prompt, so a new skill can raise `adminTurnPromptBudget` in `core/services/studio/localaiskills_test.go` (measure it, write the reason beside the constant). The `localai` skill must point to the new skill in a line, because a chat in the sandbox has no other way to find it; that may raise the size cap in `core/services/localaitools/localaiskill_test.go`. Raise a bound only with the measured numbers in the comment and in the commit.
- [ ] **NOTICE.** `core/services/studio/builtinskills/tree/NOTICE` names every built-in skill. Add yours. For third-party material, keep the copyright line and the full licence text in it, and name the source and what you cut.
- [ ] **Docs lists.** Update: the list of built-in skills and the paragraph about the media skills in `docs/content/features/chats.md`, the reserved-names row in `docs/content/addons/contract-reference.md`, and the comment in `core/services/studio/builtinskills/builtinskills.go`.
- [ ] **Skill text rules.** Refer to another skill by its name, never by a path (a builtinskills spec checks this). No internal names or paths (`aioffice`, `localstudio`, a home path). Plain English, no marketing.

## 4. Verify before you push

Run these in the worktree. Name the packages you changed.

- [ ] `go vet ./...` on the changed packages, and `gofmt -l` on the changed Go files.
- [ ] `go test -count=1 <package>` for each changed package, one package at a time. Typical set: `./addons/sandbox/...`, `./pkg/addons/contract/`, `./pkg/sandboxpolicy/`, `./core/services/localaitools/...`, `./core/services/studio/skills/...`, `./core/services/studio/builtinskills/...`, `./core/services/studio/sandbox/...`.
- [ ] The studio package: `go test -count=1 ./core/services/studio/`. It has the profile, hint and prompt budget specs, and it is slow. Run it when you touch a skill, a profile, a tool group or a budget.
- [ ] Shell tests: `sh addons/sandbox/images/guest/check-tools_test.sh` and `sh scripts/addons/package_test.sh` when you change the image check or `package.sh`.
- [ ] In a fresh worktree, run `make protogen-go` before the Go tests (the generated gRPC code is not committed).
- [ ] Do not pass `-count=3` (or any count above 1) to a Ginkgo suite: Ginkgo refuses it. For a flake, run the spec again once.
- [ ] `TestConnectorSpecs` (`core/services/studio/connectors_inbound_test.go`) is a known flake. If only that test fails, run it once more before you look into it.
- [ ] Rebuild generated output you touched: `UPDATE_MANIFEST=1` and `UPDATE_DOCS=1` runs (section 1), `make swagger` (section 2). Commit the result.
- [ ] Commit with an `Assisted-by:` trailer and a DCO sign-off (`git commit -s`) as in [.agents/ai-coding-assistants.md](ai-coding-assistants.md). No private names, hosts or paths in code, docs, commit text or PR text.
