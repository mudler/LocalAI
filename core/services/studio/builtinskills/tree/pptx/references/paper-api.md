# Paper PPTX API companion

This companion covers capabilities beyond familiar `python-pptx`. The installed distribution is
`paper-pptx`, but the import remains `pptx`; ordinary `python-pptx` APIs remain available.

## Capability map

| Need | Public import or object API |
|---|---|
| Open/create | `from pptx import Presentation` |
| Structure, text, effective values | `pptx.inspect.inspect_deck`, `inspect_text`, `effective_font`, `effective_paragraph_format`, `effective_shape_format` |
| Anchored text edits | `pptx.edit.replace_text`, `replace_text_at`, `refind` |
| Real bullets and explicit autofit | `paragraph.bullet`; `text_frame.normalize_autofit` |
| Unique named targets | `slide.shapes.shape_by_name`, `picture_by_name`, `table_by_name`, `chart_by_name` |
| Slides and owned shapes | `prs.slides.clone/delete/move/reorder`; `slide.shapes.add_copy/delete/move` |
| Layout/deck composition | `slide.rebind_layout`; `prs.import_slide`, `append_deck` |
| Notes and live furniture | `slide.read_notes_text`, `replace_notes_text`; `slide.apply_footers`, `prs.apply_footers` |
| Safe data/media changes | `picture.replace_image`; `chart.replace_data_safe` |
| Table structure | `table.insert_row`, `insert_column`, `delete_row`, `delete_column` |
| Batched validation | `with prs.batch():` |
| Package and semantic comparison | `pptx.package.patch_save`, `diff_package`; `pptx.diff.diff_decks` |
| Conservative stops | `from pptx.errors import PaperRefusal` and its subclasses |

## Inspection and targeted edits

`inspect_deck(prs)` returns a deterministic structural `DeckManifest`; `inspect_text(slide)` returns
text blocks with stable anchors, object identity, container information, and blind-region markers.

A current `BlockAnchor` identifies a block through its story part, exact container
locator, and structural content hash. `block_index` is diagnostic only and never
the write identity. Legacy three-field anchors are resolved within their named
part by an exact unique short-text hash.

Text inspection includes grouped shapes and table cells. Table-cell effective formatting is
reported as blind rather than guessed, and a chart, other graphic frame, or `mc:AlternateContent`
subtree contributes one empty blind block so block indices stay aligned; their text is not
surveyed.

Effective values expose `.value`, `.value_pt`, `.resolved`, and `.provenance`. An unresolved value
does not provide an effective font, color, size, paragraph setting, or shape style. A color carrying
transforms such as `lumMod` reports unresolved and supplies the unapplied XML.

```python
from pptx.edit import replace_text_at
from pptx.inspect import inspect_text

block = next(b for b in inspect_text(prs.slides[0]).blocks if b.text == "Old title")
result = replace_text_at(prs, block.anchor, "Old title", "New title")
assert result.replacements == 1
```

`replace_text(prs, find, replace, *, include_notes=False)` is literal and deck-wide; zero matches is
a normal `ReplaceResult` (`replacements`, `blocks`), not a refusal. Matches never cross a paragraph,
`a:br`, or field boundary.

`replace_text_at` resolves a current anchor's exact shape or table cell first, then requires a unique
matching fingerprint inside it. Changed content raises `StaleAnchorError`; missing or ambiguous
structure refuses without mutating; `find` absent from the resolved block, or present only across a
boundary a match cannot cross, raises `TargetNotFoundError` rather than returning a zero count.
Recover a moved but otherwise unchanged block with `pptx.edit.refind(prs, old_anchor)`. Neither
resolution path uses ordinal preference, shape names, geometry, or approximate text.

`paragraph.bullet` reads `type`, `char`, `number_scheme`, `start_at`, `font_name`, and
`size_percent`, reporting only what is set on that paragraph; a bullet inherited from a layout or
master reads as `None`. `effective_paragraph_format` resolves the rendered `bullet`, `bullet_font`,
and `bullet_size` with provenance.

`set_character(char="•", *, font_name=None, size_percent=None, left_margin=..., hanging_indent=...)`,
`set_numbered(...)`, and `set_none()` author real PowerPoint bullet state. `size_percent` is a
fraction from 0.25 to 4.0 in whole percents. `left_margin` and `hanging_indent` default to writing
`marL`/`indent` so the bullet hangs; pass `None` for either to leave the paragraph attribute alone.

`text_frame.normalize_autofit(*, min_font_size=None, resolve=False)` freezes supported rendered text
metrics and makes autofit explicit; `resolve=True` resolves inherited font sizes and refuses when a
size cannot be resolved.

## Preserve ownership and composition

- `prs.slides.clone(source, *, after=None, policy=None) -> Slide` deep-copies charts and their
  embedded workbooks and notes; media is shared by default. Optional policy type:
  `pptx.slide.SlideClonePolicy`.
- `prs.slides.delete(slide)`, `move(slide, to_index)`, and `reorder(new_order)` maintain supported
  sections, custom shows, and relationships. `new_order` is a permutation of current indices.
- `slide.shapes.add_copy(shape) -> Shape` assigns fresh shape IDs, deep-copies charts/workbooks, and
  shares media. The copy keeps the source shape's name; the `*_by_name` APIs refuse duplicate
  names. `delete(shape)` and `move(shape, to_index)` require a direct
  collection member.
- `slide.rebind_layout(target_layout, *, placeholder_map="auto", orphan_policy="refuse")` is
  same-package only and returns a `RebindReport` with the resolved mapping and run shifts.
  `run_shifts` covers resolved font values, so placeholder geometry and text direction inherited
  from the layout are outside that comparison.
- `prs.import_slide(source_prs, slide, *, mode, position=None, notes=True, section=None,
  section_id=None, target_layout=None, placeholder_map="auto")` returns an `ImportReport` covering
  layout binding, copied parts, dropped content, and run shifts. `mode` is required:
  `adopt_theme`, `keep_appearance`, or `bake`.
- Automatic layout binding matches a unique exact layout name, then a unique exact non-custom layout
  type; `bake` adds a unique blank-layout fallback and `keep_appearance` transplants the source
  layout chain instead. It never falls back to the first layout: no unique match raises
  `UnsupportedStructureError` and more than one candidate at a tier raises `AmbiguousTargetError`,
  both before any write. Pass `target_layout` to settle it explicitly.
- Under `adopt_theme`, placeholder reconciliation keeps exact type+idx matches and accepts a
  same-type or compatible-family fallback only when it is unique. Pass a partial
  `placeholder_map={source_idx: target_idx | None}` to resolve ambiguity, where `None` deliberately
  orphans and bakes that source placeholder; `placeholder_map_used` reports the resolved mapping.
  The argument does not apply to `keep_appearance` or `bake`.
- `section` selects an existing destination section by unique exact name and `section_id` by exact
  stored GUID when names collide. They are mutually exclusive; with neither, the slide enrolls
  adjacent to the insertion point when the deck has sections. A missing selector raises
  `TargetNotFoundError` and a duplicate match raises `AmbiguousTargetError`, before any write.
- `prs.append_deck(source_prs, *, mode, notes=True)` validates the source deck before mutation and
  returns one `ImportReport` per imported slide. Whole-deck append is automatic-only: it takes no
  `target_layout` or `placeholder_map`, so an ambiguity it hits must be settled per slide.
- `table.insert_row(after, *, copy_format_from=None)` and `table.insert_column(after, *, width=None,
  copy_format_from=None)` insert one minimal empty cell per grid position, keeping the grid
  consistent by construction. `copy_format_from` is a pre-insertion row/column index whose template
  cell `a:tcPr` (direct formatting only) is deep-copied into each new cell; text and merge state
  never copy, so a new row or column is always unmerged. `delete_row`/`delete_column` remove the
  cells with the row or column. All four guard merges cell-wise, refusing
  (`UnsupportedStructureError`, tree untouched) only when a merge would be split or would extend
  past the deleted line, so a merged header row never blocks body-row work.

Unknown or unsafe relationship types refuse before mutation. Raw copies of slide, chart, image,
notes, or shape XML are outside the safe contract unless the complete relationship graph is owned.

## Notes and footers

`slide.read_notes_text()` reads only an existing notes body and never creates a notes part.
`slide.replace_notes_text(text)` also requires an existing supported notes body; `\n` starts a new
paragraph and the first paragraph/run formatting is retained where present.

`prs.apply_footers(...)` and `slide.apply_footers(...)` create real date and slide-number fields.
Arguments are keyword-only: `footer=None`, `slide_number=False`, `date_format=None`,
`fixed_date=None`, and `now=None`; presentation scope also accepts `skip_title_slides=False`. Each
call sets the complete footer/date/number state, so omitted elements are removed. `date_format` and
`fixed_date` are mutually exclusive, and footer text is one line: a newline is refused. Missing or
disabled layout furniture can refuse.

## Batch validation

`with prs.batch():` validates the deck once at block exit instead of once per mutating Paper call.
Opt-in; per-edit validation is the default. If the exit check fails, every edit in the block is
discarded. Saving inside an open block refuses with `BoundaryViolationError`. The gain scales with
edits per block, not deck size, and a block around a single edit breaks
even.

## Save, compare, and handle results

```python
from pptx.diff import diff_decks
from pptx.package import patch_save

package_diff = patch_save(source_path, prs, output_path)
deck_diff = diff_decks(source_path, output_path, detail="text")
```

`prs.save(output_path)` uses normalized serialization. Saving to a path is atomic and resolves
symlinks. `patch_save` preserves semantically unchanged original XML bytes; it
writes atomically and returns the residual `PackageDiff` (`deltas`, each a `PartDelta` with
`partname`, `kind`, `change`, `detail`).

`patch_save` narrows the write, so a one-line edit to a large deck can diff as a few parts rather
than all of them. When nothing changed at all, `out_path` is written as
an exact byte copy of the original. Relationship collections compare by their complete bindings and
content-type manifests by each member's effective type, so a producer's serialization choices do not
count as a change and a PowerPoint-authored deck round-trips byte-identically. Well-formed but
ambiguous or unsupported registries get no such normalization and fall back to ordinary XML
comparison.

`diff_package(a, b)` compares XML semantically and binaries by bytes, with the same registry
normalization `patch_save` uses; malformed XML or a prohibited DTD/entity construct raises
`ValueError`.

`diff_decks(..., detail="structure"|"text"|"full")` compares lineage-related decks by permanent
slide ID and reports slide order, shapes, geometry, media, charts, tables, text, notes, effective
formatting, bullets, and package changes at the selected detail. Shapes match on slide-wide shape ID
plus compatible structural kind; display names are labels only, and moving a shape across a group
boundary reads as a removal plus an addition.

Chart data, text, and notes require `detail="text"`; effective and bullet shifts require
`detail="full"`. Paragraph moves and ambiguous identity are not inferred. `package_changes` is the
fallback for semantic changes without a specialized facet. This is not a visual diff.

Paper reports are typed objects. `.to_dict()` is a deterministic JSON-ready payload, and its keys
are not always the attribute
names. It stamps a schema and version, omits empty facets on some reports, and adds derived values
such as `TextInspection.blind_region_count` that are not dataclass fields.

A `PaperRefusal` means the guarded operation declined and left memory and disk unchanged. The
subclasses say what went wrong: `PackageLimitError` (the archive has no single unambiguous reading, or is unsafe to
expand — on intake or on write), `TargetNotFoundError` (addressing matched nothing, or a held proxy went stale),
`StaleAnchorError` (a subclass of it, for an anchor whose content hash no longer matches),
`AmbiguousTargetError` (addressing matched more than one), `UnsupportedStructureError` (structure
this API will not touch safely, or edits that would not reopen), `RelationshipPolicyError` (the
relationship graph cannot be carried across as asked), and `BoundaryViolationError` (an operation
ran outside its safe scope, today only saving inside an open `batch()`). Invalid caller values
remain ordinary `ValueError` or `TypeError`.
