<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.edit`

Structural-anchor write APIs: formatting-preserving text replacement (paper-pptx addition).

## `RESULT_SCHEMA_NAME`

```python
RESULT_SCHEMA_NAME = 'paper-replace-result'
```

## `RESULT_SCHEMA_VERSION`

```python
RESULT_SCHEMA_VERSION = 2
```

## `ReplaceResult`

```python
ReplaceResult(replacements: int, blocks: Tuple[BlockAnchor, ...] = tuple()) -> None
```

Outcome of a text replacement.

Fields:

* ``replacements`` -- total number of occurrences replaced across the deck.
* ``blocks`` -- POST-edit `pptx.inspect.BlockAnchor` for each block that was
  touched (their content hashes reflect the new text).

### `blocks`

```python
blocks: Tuple[BlockAnchor, ...] = field(default_factory=tuple)
```

### `replacements`

```python
replacements: int
```

### `to_dict`

```python
to_dict() -> dict
```

Return the replace result as a JSON-ready dict stamped with its schema and version.

## `refind`

```python
refind(prs: 'Presentation', anchor: BlockAnchor) -> BlockAnchor
```

Return a fresh current anchor for an exact unique fingerprint match.

Current anchors search only their structurally identified shape or table cell. Legacy
three-field anchors search the whole named part by their pinned short text hash. Neither path
uses ordinal preference, shape names, geometry, or approximate text.

## `replace_text`

```python
replace_text(prs: 'Presentation', find: str, replace: str, *, include_notes: bool = False) -> ReplaceResult
```

Replace literal `find` with `replace` across `prs`; return a `ReplaceResult`.

Matching is case-sensitive, left-to-right, and non-overlapping. A match may span consecutive
text runs in one paragraph, but never a paragraph boundary or an intervening non-run element
such as a line break or field. `find` must be a non-empty string; `replace` may be empty. Tabs
are accepted, while line breaks, XML control characters, and non-encodable text are refused.

Boundary fragments retain their original run properties, and replacement text receives the
run properties of the run where its match starts. Untouched runs remain byte-identical. Any
run left with no text after replacement is removed, so formatting found only on that run is
not retained.

Traversal covers slide shapes, grouped shapes, and table cells, plus existing notes slides
when `include_notes=True`. An unsupported blind region refuses the whole operation before any
write. Zero matches is a successful result with ``replacements == 0`` and no block anchors.
Each returned block anchor describes the post-edit text of a block that changed.

## `replace_text_at`

```python
replace_text_at(prs: 'Presentation', anchor: BlockAnchor, find: str, replace: str) -> ReplaceResult
```

Replace literal `find` in the block at `anchor`; return a `ReplaceResult`.

Matching, validation, run-boundary formatting, and non-run boundaries are the same as
`replace_text`. Current anchors resolve the exact shape or table cell first, then require
a unique matching full fingerprint in that container. Changed content raises
`StaleAnchorError`; missing or ambiguous structure refuses without mutation. Legacy
three-field anchors search only for an exact unique short hash in their named part. `find`
absent from the resolved block, or present only across a boundary a match cannot cross, raises
`TargetNotFoundError` rather than returning a zero count.

On success the result contains the number of occurrences replaced in that block and its one
post-edit anchor.
