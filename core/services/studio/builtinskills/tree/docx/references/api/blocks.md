<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.blocks`

Anchor-relative block operations and clause-level redlines (paper-docx).

## `BlockEditResult`

```python
BlockEditResult(story: str, inserted_blocks: int, deleted_blocks: int, deleted_text: Tuple[str, ...], revision_ids: Tuple[int, ...]) -> None
```

Outcome of a block-level edit.

### `deleted_blocks`

```python
deleted_blocks: int
```

### `deleted_text`

```python
deleted_text: Tuple[str, ...]
```

### `inserted_blocks`

```python
inserted_blocks: int
```

### `revision_ids`

```python
revision_ids: Tuple[int, ...]
```

### `story`

```python
story: str
```

### `to_dict`

```python
to_dict() -> dict
```

## `BlockTarget`

```python
BlockTarget = Union[str, Block, Span]
```

## `ListBlock`

```python
ListBlock(items: Sequence[str], kind: str = 'bullet', level: int = 0) -> None
```

A real bullet/decimal list (backed by a real numbering definition).

### `items`

```python
items: Sequence[str]
```

### `kind`

```python
kind: str = 'bullet'
```

### `level`

```python
level: int = 0
```

## `RichParagraph`

```python
RichParagraph(runs: Sequence[TextRun], style: Optional[str] = None) -> None
```

A paragraph built from styled runs.

### `runs`

```python
runs: Sequence[TextRun]
```

### `style`

```python
style: Optional[str] = None
```

## `TableBlock`

```python
TableBlock(rows: Sequence[Sequence[str]]) -> None
```

A simple rectangular table of plain-text cells.

### `rows`

```python
rows: Sequence[Sequence[str]]
```

## `TextRun`

```python
TextRun(text: str, bold: bool = False, italic: bool = False) -> None
```

One run of a rich paragraph.

### `bold`

```python
bold: bool = False
```

### `italic`

```python
italic: bool = False
```

### `text`

```python
text: str
```

## `check_install`

```python
check_install() -> None
```

Refuse when paper-docx and python-docx are both installed.

## `insert_blocks_after`

```python
insert_blocks_after(document: 'Document', anchor: BlockTarget, *, blocks: 'Sequence[object]', tracked: bool = False, author: Optional[str] = None, date: Optional[dt.datetime] = None) -> BlockEditResult
```

Insert a typed block list after `anchor`, and return a `BlockEditResult`.

`blocks` mixes `RichParagraph` (styled runs), `ListBlock` (real bullet or decimal lists,
numbering definition created on demand) and `TableBlock` (rectangular tables). Reach for
this when you want structure rather than raw XML. Refuses a protected document, and an
anchor that is missing, ambiguous, foreign, stale, or spans more than one paragraph.

## `insert_section_after`

```python
insert_section_after(document: 'Document', anchor: BlockTarget, *, heading: str, paragraphs: Sequence[str], heading_style: str = 'Heading2', body_style: Optional[str] = None, tracked: bool = False, author: Optional[str] = None, date: Optional[dt.datetime] = None) -> BlockEditResult
```

Insert a heading plus body paragraphs after `anchor`, and return a `BlockEditResult`.

With `tracked=True` each inserted paragraph is a real Word insertion attributed to
`author` (required) and `date`. Refuses a protected document, an undefined heading
style, and an anchor that is missing, ambiguous, foreign, stale, or spans
more than one paragraph.

## `require_anchor_owner`

```python
require_anchor_owner(document: 'Document', anchor: object, *, argument: str = 'anchor') -> None
```

Check ownership for live anchor forms; value/string anchors are inert.

## `rollback_on_error`

```python
rollback_on_error(document: 'Document', *participants: Any) -> Generator[None, None, None]
```

Restore the live package and named mutable proxies after an error.

## `tracked_delete_paragraphs`

```python
tracked_delete_paragraphs(document: 'Document', start_anchor: BlockTarget, *, end_anchor: Optional[BlockTarget] = None, count: int = 1, author: str, date: Optional[dt.datetime] = None) -> BlockEditResult
```

Mark a paragraph range deleted with real tracked changes; returns a `BlockEditResult`.

Runs move into `w:del` with formatting intact and the paragraph mark is stamped, so
accepting removes the paragraphs and rejecting restores them exactly. Refuses a
protected document, an endpoint spanning multiple paragraphs, a range
crossing stories or parents, a bookmarked or non-plain run, and an open field.

## `tracked_replace_paragraphs`

```python
tracked_replace_paragraphs(document: 'Document', start_anchor: BlockTarget, replacement_paragraphs: Sequence[str], *, end_anchor: Optional[BlockTarget] = None, count: int = 1, body_style: Optional[str] = None, author: str, date: Optional[dt.datetime] = None) -> BlockEditResult
```

Tracked-delete a paragraph range and tracked-insert replacements after it.

Returns a `BlockEditResult`. Replacements land after the last deleted paragraph. Carries
the same refusals as `tracked_delete_paragraphs`: protection, a multi-paragraph
endpoint, a range crossing stories, bookmarked or non-plain runs, an open field.
