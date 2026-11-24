# apexentries

Generates gallery entries for the `mudler/*-APEX-GGUF` HuggingFace repositories.

Each APEX repo becomes one **family**: one entry per quality rung the repo
publishes and one per quantization rung its unsloth counterpart publishes, all
gathered under the **base model's** entry. LocalAI's variant selector then picks
the build that fits the hardware in front of it.

## The hub is the base model entry, never a generated `*-apex` parent

Somebody looking for `qwen3.6-35b-a3b` must find every build of those weights
under that one name: the APEX imatrix rungs, the unsloth quant rungs and any
speculative build. A separate `qwen3.6-35b-a3b-apex` hub competing with the base
entry would split the family in two and leave whichever half the user did not
search for effectively invisible.

So the generator resolves the hub by stripping the `-APEX`, `-MTP` and `-TQ`
markers and looking the result up in the index, trying both the repo-derived and
the stem-derived candidate the same way `CounterpartCandidates` does. Then:

- **The hub exists** (14 of the 45 repos, resolving to 10 distinct entries).
  Nothing new is emitted for the family root. A `variants:` block is spliced into
  the entry that is already there, textually, leaving its description, icon,
  tags, overrides and files untouched. The line editing is shared with the
  `variantproposals` job via `.github/ci/galleryedit`.
- **The hub is absent** (the other 31). A new hub is emitted, named for the base
  model and never for the APEX repo. It carries one of the discovered builds as
  its own payload so it is a complete installable entry rather than a bare index,
  and that payload is what gives it an `overrides.backend`. Without a declared
  backend the verifier would skip it, so a hub carrying feature tags would escape
  the tagging check in silence.

Several APEX repos routinely resolve to one base model, so both paths accumulate
by hub name rather than assuming one family per hub.

Two references are always filtered out of a hub's list: anything the entry
already declares, and the hub's own name. The self reference is not merely
redundant. An unsloth rung whose weights the gallery already ships under the base
name resolves, through the merge, straight back to the hub, and the verifier
reads a self reference as a variant that declares variants of its own.

The four hand-written `*-apex` entries (`qwen3.6-35b-a3b-apex`,
`gemma-4-26b-a4b-it-apex`, `qwen3.5-35b-a3b-apex`,
`nemotron-3-nano-omni-30b-a3b-reasoning-apex`) are **ordinary builds**, not hubs.
They are referenced from their hub's variants list like any other rung, and are
never deleted or renamed.

## Flags

| Flag | Default | Meaning |
|------|---------|---------|
| `-index <path>` | `gallery/index.yaml` | Gallery index to dedup against. Read only, unless `-apply` is passed. |
| `-only <a,b,c>` | (all) | Comma-separated full repo names (`mudler/Foo-APEX-GGUF`) to restrict generation to. A name that matches nothing is reported as a warning, since it is a typo rather than an empty result. |
| `-out <path>` | (none) | Write the entries to add to this file. Nothing is written to the gallery. |
| `-apply` | `false` | Splice the variants into `-index` and append the new entries to it. |
| `-verify <path>` | (none) | Verify a gallery index and exit. Ignores every other flag. |

Either `-out` or `-apply` is required, otherwise the run has nothing to do.

`-apply` splices variant lines into existing entries and **appends** new ones. It
never re-serialises the index: it is roughly 40,000 lines, and a YAML round trip
would reflow the whole file, drop the anchors and merge keys the gallery relies
on, and produce a diff nobody can review. On the three-family sample the splice
is 24 added lines across 3 hunks with zero deletions.

## Discovery is by filename suffix, never by repo name

Builds come from the files a repo actually publishes. A filename is never
constructed from a repo name, because the two disagree:
`mudler/gemma-4-26B-A4B-it-APEX-GGUF` ships `gemma-4-26B-A4B-APEX-*.gguf`, and
five other repos likewise drop a suffix (`-it`, `-2603`) or a vendor prefix
(`NVIDIA-`) that the repo name carries. Composing a URL from the repo name would
produce a 404 for every one of them, and the 404 would only surface after the
entry shipped.

The quality ladder is matched on the trailing tier marker, `-(I-)?(Quality|
Balanced|Compact|Mini|Nano).gguf`. The `I-` prefix marks the imatrix ladder. The
imatrix ladder is emitted when it is non-empty and the plain ladder is used only
as a fallback, because two of the 45 repos publish no imatrix tiers at all and
must still contribute. Eleven repos carry a fifth `I-Nano` rung, so nothing
assumes a fixed number of rungs.

Every run prints, per repo, the counts that discovery accounted for. If the
number of classified files is short of the number of `.gguf` files the repo
publishes, the shortfall is printed as `UNCLASSIFIED`. That check is a set
difference on counts rather than a second pass over filenames: a second matcher
would duplicate the tier regex and the two copies would drift. The failure it
catches is quiet. A publishing-script typo that breaks every imatrix filename in
a repo does not produce a short ladder; it makes the imatrix ladder empty, and
the fallback then downgrades the whole family to the plain ladder with nothing
said. A downstream HTTP check cannot catch it either, because it validates the
URLs that were emitted, and an undiscovered tier emits none.

The same reasoning applies to `UNACCOUNTED QUANT`, printed when the unsloth
counterpart demonstrably publishes a wanted quant that produced no build. It is
reported at discovery time because a dropped quant leaves no trace at all in the
finished gallery file.

## sha256 always comes from the API

Every file stanza takes its `sha256` from the HuggingFace models API
(`lfs.sha256`). A GGUF the API describes without one is a fatal error for that
family: the repo is reported by name and the run ends non-zero. It is never
substituted from another field, because that is exactly how a Xet hash ends up
masquerading as a content hash.

## The dflash / mtp tagging rule

An entry is tagged `dflash` or `mtp` **if and only if** it configures the
matching `spec_type:draft-<feature>`. Variant ranking reads tags and nothing
else, so a tag that does not match the configuration either promotes a build
that is no faster or hides one that genuinely is.

A repo name is not configuration. `mudler/Qwen3.6-35B-A3B-APEX-MTP-GGUF` ships
weights that carry MTP heads; an entry that does not enable them is not an MTP
entry and is not tagged as one.

A generated hub inherits the tags of the build it carries as its payload, rather
than rebuilding them from the base set, so a hub whose payload configures a
`spec_type` stays tagged consistently with the overrides copied alongside it.

## Reuse reporting: two categories, not one

Generated entries are deduped against the gallery and against the batch itself.
The run prints the result under two separate headings, because the two cases are
not equivalent:

- **URI MATCHES** mean the gallery, or an earlier entry in this batch, already
  ships exactly these weights. Pointing the hub at the existing entry is correct
  and needs no thought.
- **NAME COLLISIONS** mean an entry already owns the name but holds different
  weights. Referencing it would point the hub at a build other than the one
  generated. Every one of these must be inspected by hand.

The run then prints `HUBS SPLICED`, listing every reference that will be added to
an entry the gallery already ships along with the line it will be added at, and
`HUBS CREATED` for the families that get a new hub. The splices are the part a
review has to read closely, because they modify entries somebody else wrote.

Hubs are deliberately kept out of the merge. A new hub carries the family's top
rung as its own payload, so URI dedup would fold the hub into that rung and the
family would lose the very entry point this command exists to create.

## Workflow: sample first, then the full set

Never run the full generation straight into the gallery. Generate a small,
deliberately awkward sample, have it reviewed, then run the rest.

```bash
# 1. Sample three families that between them cover the awkward shapes:
#    a standard four-rung repo, one with the extra I-Nano rung AND a file stem
#    that differs from its repo name, and one whose unsloth counterpart shards
#    its quants across subdirectories.
go run ./.github/ci/apexentries \
  -index gallery/index.yaml \
  -only mudler/Qwen3.6-35B-A3B-APEX-GGUF,mudler/gemma-4-26B-A4B-it-APEX-GGUF,mudler/Step-3.7-Flash-APEX-GGUF \
  -out /tmp/sample.yaml

# 2. Verify the sample against the gallery it would join, splices included. Apply
#    to a COPY, never to the real index, and check that the diff is only the
#    intended variant lines. Compare the verifier output to the gallery's own
#    baseline: what matters is that the sample adds no new problem, not that the
#    total is zero.
cp gallery/index.yaml /tmp/index-copy.yaml
go run ./.github/ci/apexentries -index /tmp/index-copy.yaml -only <same list> -apply
diff -u gallery/index.yaml /tmp/index-copy.yaml   # expect zero deletions

go run ./.github/ci/apexentries -verify gallery/index.yaml > /tmp/baseline.log 2>&1
go run ./.github/ci/apexentries -verify /tmp/index-copy.yaml > /tmp/spliced.log 2>&1
diff /tmp/baseline.log /tmp/spliced.log

# 3. Have a human review /tmp/sample.yaml and every reported name collision.

# 4. Only then, the full set.
go run ./.github/ci/apexentries -index gallery/index.yaml -apply
```

## Tests

```bash
go test ./.github/ci/apexentries/
```

The shared line editor has its own package:

```bash
go test ./.github/ci/galleryedit/
```

`.github/ci/` is invisible to `go list ./...`, so these specs are not covered by
`make lint` or the repository test run. `.github/workflows/ci-tools-tests.yaml`
names the package explicitly; keep that workflow in step with any package added
under `.github/ci/`.
