# Paper DOCX API Companion

This companion covers capabilities beyond familiar `python-docx`. The installed distribution is `paper-docx`, but the import remains `docx`; ordinary `Document`, paragraph, run, style, table, section, picture, header, and footer APIs remain available.

## Capability map

| Public surface | Use it for |
|---|---|
| `docx.story.story_parts`, `iter_blocks`, `outline` | Traverse body, headers, footers, footnotes, endnotes, comments, revisions, controls, and text boxes using `view="current"`, `"original"`, or `"all"`. `outline()` also reports blind regions. |
| `docx.search.find_text`, `find_one`, `replace_all`, `Span.replace`, `Span.comment` | Find text across fragmented runs/stories — exact by default, `match="normalized"` to fold case, typography, and whitespace; replace it without flattening unaffected runs, emit tracked replacements, or anchor a comment. |
| `docx.blocks.insert_section_after`, `tracked_delete_paragraphs`, `tracked_replace_paragraphs`, `insert_blocks_after` | Insert or redline paragraph-level content. `insert_blocks_after` accepts `RichParagraph`, `TextRun`, `ListBlock`, and `TableBlock`. Every anchor is a `BlockTarget`: a text string, a live `Block`, or a `Span`. |
| `docx.tableops.find_table`, `update_cell`, `insert_row_after`, `delete_row` | Make validated table edits that refuse unsafe merged, nested, or tracked structures. `find_table` searches top-level body tables, exact by default (`match="normalized"` folds); a match never crosses cells. `insert_row_after(..., copy_format_from=)` needs a uniform plain-text template row and refuses a complex one rather than flattening it. |
| `docx.numbering.list_numbering`, `apply_numbering`, `apply_list_style`, `ensure_bullet_definition`, `ensure_decimal_definition`, `restart_numbering` | Inspect and author real Word numbering rather than literal bullet characters. |
| `docx.controls.list_controls`, `iter_controls`, `get_control`, `set_control_value` | Read and fill text, checkbox, date, and choice content controls while clearing placeholder state. |
| `docx.bookmarks.list_bookmarks`, `create_bookmark`, `delete_bookmark`; `docx.fields.add_page_number_field`, `add_page_count_field`, `add_date_field`, `add_reference_field`, `add_caption`, `insert_toc_after` | Author bookmark targets and live Word fields. Field results are placeholders until Word opens and updates them. |
| `docx.links.add_hyperlink`; `docx.notes.add_footnote`, `add_endnote` | Wrap an exact span in a hyperlink, or attach a footnote or endnote to it. |
| `Drawing.replace_picture` | Swap an existing image's bytes while its size, position, and other users of the old part stay as they are. |
| `docx.commentops.anchored_text`, `reply`, `resolve`, `is_resolved`, `parent_of`, `comment_thread`, `delete_comment` | Inspect, update, and delete native comment threads. |
| `docx.formatting.format_of`; `docx.protection.protection_status`, `acknowledge_protection`, `set_protection` | Resolve the effective formatting of a `Run`, `Paragraph`, or `Span` with provenance; inspect, acknowledge, or turn on Restrict Editing. Acknowledgement is in-memory and does not remove protection. |
| `docx.document.Document.revisions` | Enumerate tracked changes and accept or reject them atomically, optionally filtered by author. |
| `docx.composition.insert_blocks_from`, `append_document` | Copy content across documents while reconciling styles, numbering, media, hyperlinks, and bookmarks. `append_document(headers="source")` also carries the source letterhead. |
| `docx.package.diagnose`, `diff_package`, `text_diff`, `pending_changes`, `patch_save`, `compare` | Diagnose questionable input, prove package/text changes, save narrowly, or create a native redline between two files. |

## Text targeting

Matching is literal and exact by default; pass `match="normalized"` to fold case, typography, and whitespace. Both policies assemble text across fragmented runs, and exact matching represents each paragraph boundary as one literal `\n`.

`find_text()` returns every matching span. `story=` limits the part, `view=` chooses the revision projection, and `near=` ranks every match by distance without hiding candidates. One-based `nth=` selects a match by document position and cannot be combined with `near=`.

`find_one()` supports `story=`, `view=`, and `nth=`, but not `near=`. It requires the narrowed result to identify exactly one span: zero matches raise `TargetNotFoundError`, and two or more raise `AmbiguousTargetError`. Ambiguity can be resolved with a live span or block, a narrower story, or an explicit `nth`.

```python
from docx.search import find_one

span = find_one(document, "Payment is due in 30 days", view="current")
result = span.replace(
    "Payment is due in 45 days",
    tracked=True,
    author="Reviewer",
)
assert result.tracked and result.revision_ids
```

A `Span` holds live references and revalidates on every operation. Every successful text-changing replacement *consumes* the span; a no-op, a refusal, or a rolled-back mutation leaves it reusable. `ReplaceResult` also carries `preserved_formatting_regions` and `preserved_revision_ids`, which are independent guarantees — `revision_ids` stays reserved for newly authored tracked revisions.

`replace_all()` captures one batch, takes the same `match=` policy, skips matches already equal to `new_text`, and applies in reverse document order so no pending match shifts. A refusal on one match lands in `ReplaceAllResult.refused` and the remaining matches proceed, but a stale span aborts the batch and rolls back every replacement already applied. A non-empty `refused` denotes a partial result; a raised `TargetNotFoundError` denotes no result.

`preserve_revision=True`, on both `Span.replace` and `replace_all`, permits correcting a current-view span wholly owned by one existing `w:ins` without changing that insertion's id, author, date, or accept/reject meaning; the corrected text stays attributed to the original author.

`span.comment(text, author=..., initials=..., date=...)` creates a native comment. Comments can anchor only in the main document story.

## Structural operations

`insert_blocks_after()` accepts typed blocks for real lists and mixed run formatting:

```python
from docx.blocks import ListBlock, RichParagraph, TextRun, insert_blocks_after

receipt = insert_blocks_after(
    document,
    "Recommendations",
    blocks=[
        RichParagraph([TextRun("Priority: ", bold=True), TextRun("Immediate")]),
        ListBlock(["Notify stakeholders", "Update the schedule"], kind="decimal"),
    ],
)
```

A block anchor is a `BlockTarget`: the block's text as a string, a live `Block` from `iter_blocks()` or `outline()`, or a `Span`. A stored `docx.story.Anchor` is inert location evidence for historical search and revision data, not a mutation target. An endpoint spanning more than one paragraph refuses.

`Document.revisions.accept_all(author=None)` and `.reject_all(author=None)` resolve supported revisions atomically and return the resolved count. They validate the whole selected set first, so a set containing types paper-docx cannot resolve refuses without half-resolving. `remaining_unsupported()` reports unresolved revision types.

Content-control lookup is exact and refuses missing or ambiguous targets:

```python
from docx.controls import set_control_value

set_control_value(document, "Approved", tag="status")
```

Cross-document composition returns a `CompositionReport` with inserted blocks, reconciliation maps, findings, and declared changed parts:

```python
from docx.composition import append_document

report = append_document(document, source, section="new_page", styles="match_by_name")
```

`insert_blocks_from()` copies a source range instead. `start_anchor` and `end_anchor` name SOURCE paragraphs whose containing top-level body blocks bound the range, and both endpoints are included by default; `include_start=False` or `include_end=False` drops that endpoint's block, and `include_end=False` without an `end_anchor` is a `ValueError`. With no `end_anchor`, `count` blocks are copied from the start block. Live source endpoints may come from any inspection view, but a live destination anchor must come from `view="current"`.

`headers="source"` carries the source letterhead onto the destination, overwrites the destination
last section's header and footer, and flips the document-wide even/odd setting.
`delete_comment(document, comment)` removes a comment with its replies and their range marks.

## Annotate and illustrate

Hyperlinks and notes attach to an exact `Span`. Both refuse the same
surfaces: a protected document, a stale or foreign span, a field result, and a data-bound,
locked, or plain-text content control. `add_hyperlink` also refuses a span crossing paragraphs
or one already inside a hyperlink; notes also refuse a span outside the main body or inside a
text box.

```python
from docx.links import add_hyperlink
from docx.notes import add_footnote
from docx.search import find_one

span = find_one(document, "quarterly revenue")
add_hyperlink(document, span, "https://example.com/q3")
note_id = add_footnote(document, find_one(document, "adjusted basis"), "Net of returns.")
```

`add_hyperlink` returns the `Hyperlink` and defines the Hyperlink character style when the
document lacks it. `add_footnote` and `add_endnote` return the note id and create the notes
part on first use. Retarget an existing link by assigning `hyperlink.address`, which clears
`.fragment` and so converts an internal bookmark jump into an external link.

`docx.fields.add_caption(paragraph, label="Figure", description="")` appends a SEQ field rather
than a literal number, so Word renumbers on open. It sets the paragraph style to "Caption"
without checking that the style exists.

Replace image bytes through the drawing that holds them, reached from
`run.iter_inner_content()`:

```python
from docx.drawing import Drawing

for item in run.iter_inner_content():
    if isinstance(item, Drawing):
        item.replace_picture("chart-v2.png")
```

`replace_picture` keeps display size and position and always writes a new image part, so other
shapes sharing the old part keep their picture. The old relationship stays in the package.
It refuses a drawing with no picture, a linked picture whose bytes live outside the package,
and a protected document.

## Protection

`docx.protection.set_protection(document, edit=...)` turns Restrict Editing on for delivery.
`edit` takes a Word token: `readOnly`, `comments`, `forms`, or `trackedChanges`. It writes the
setting and never strips one. Paper mutators refuse against a protected document afterwards
until `acknowledge_protection(document)` records the deliberate override in memory.

`ProtectionStatus.blocks_paper_edits` reports enforcement without consulting the operation
class, so it reads True under `trackedChanges` or formatting-only protection where the gate
still allows body edits and comments. It does not predict whether a given call will refuse.

## Compare and save

`Document.save()` uses normal serialization. `patch_save()` retains the original bytes of unchanged package parts:

```python
from docx.package import diff_package, patch_save

save_result = patch_save(source_path, document, output_path)
package_delta = diff_package(source_path, output_path)
```

`patch_save()` serializes normally, restores original bytes for semantically unchanged XML parts, writes atomically, and makes a no-op output a verbatim copy. It reopens what it wrote and raises `MalformedPackageError` rather than leaving an unopenable file; `Document.save()` applies the same reparse gate. `PatchSaveResult` exposes `restored_parts`, `changed_parts`, `added_parts`, `removed_parts`, and `verbatim_copy`. `PackageDiff` exposes `added`, `removed`, `changed`, `semantic_changed_parts()`, and `is_semantically_empty`.

For a Word-native redline, `docx.package.compare(original, revised, author=..., date=None, granularity="word", materialize=None)` returns `CompareResult.document`, `.findings`, `.revision_count`, and `.stories`. It proves accept/reject round trips before returning and refuses differences it cannot represent safely. Save `result.document` normally.

Most Paper receipts and reports provide `.to_dict()` with a stable schema. Package readability does not establish Word pagination or field rendering.

## Refusals

`docx.errors.PaperRefusal` covers safe, atomic refusals. The subclasses are `MalformedPackageError`, `AmbiguousTargetError`, `TargetNotFoundError`, `UnsupportedStructureError`, `BoundaryViolationError`, `RelationshipPolicyError`, and `DocumentProtectedError`. A refusal leaves the in-memory document and files unchanged. `TypeError` and `ValueError` denote caller or input errors rather than document limitations.
