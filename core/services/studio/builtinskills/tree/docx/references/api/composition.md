<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.composition`

Cross-document composition.

## `CompositionFinding`

```python
CompositionFinding(kind: str, detail: str) -> None
```

### `detail`

```python
detail: str
```

### `kind`

```python
kind: str
```

### `to_dict`

```python
to_dict() -> dict
```

## `CompositionReport`

```python
CompositionReport(inserted_blocks: int = 0, style_map: Dict[str, str] = dict(), imported_styles: List[str] = list(), renamed_styles: Dict[str, str] = dict(), numbering_map: Dict[int, int] = dict(), media_copied: List[str] = list(), bookmarks_renamed: Dict[str, str] = dict(), findings: List[CompositionFinding] = list(), declared_parts: List[str] = list()) -> None
```

Everything one composition call did — the declared changed-part
budget, the reconciliation maps, and what was reported instead of done.

### `bookmarks_renamed`

```python
bookmarks_renamed: Dict[str, str] = field(default_factory=dict)
```

### `declared_parts`

```python
declared_parts: List[str] = field(default_factory=list)
```

### `findings`

```python
findings: List[CompositionFinding] = field(default_factory=list)
```

### `imported_styles`

```python
imported_styles: List[str] = field(default_factory=list)
```

### `inserted_blocks`

```python
inserted_blocks: int = 0
```

### `media_copied`

```python
media_copied: List[str] = field(default_factory=list)
```

### `numbering_map`

```python
numbering_map: Dict[int, int] = field(default_factory=dict)
```

### `renamed_styles`

```python
renamed_styles: Dict[str, str] = field(default_factory=dict)
```

### `style_map`

```python
style_map: Dict[str, str] = field(default_factory=dict)
```

### `to_dict`

```python
to_dict() -> dict
```

## `append_document`

```python
append_document(document: 'Document', source: 'Document', *, section: str = 'new_page', styles: str = 'match_by_name', headers: str = 'destination') -> CompositionReport
```

Append `source`'s whole body to `document`, and return a `CompositionReport`.

Keeps the destination's headers and footers by default and authors no new `w:sectPr`:
`section="new_page"` prefixes a page break, `"continuous"` appends flush.
`headers="source"` overwrites the destination's last-section header and footer and flips
the document-wide even/odd setting. Refuses a protected document and an empty body on
either side. Also refuses when insertion after the destination's final paragraph,
table, or block content control would remain inside an open field result.

## `check_install`

```python
check_install() -> None
```

Refuse when paper-docx and python-docx are both installed.

## `insert_blocks_from`

```python
insert_blocks_from(document: 'Document', source: 'Document', start_anchor: 'Document', *, anchor: 'Document', end_anchor: 'Document' = None, count: int = 1, styles: str = 'match_by_name', include_start: bool = True, include_end: bool = True) -> CompositionReport
```

Copy a block range from `source` after `anchor`, and return a `CompositionReport`.

Reconciles styles, numbering, media and bookmarks so the copy keeps its appearance.
`start_anchor` and `end_anchor` identify SOURCE paragraphs whose containing top-level body
blocks form the range endpoints. A direct body paragraph or a paragraph in a top-level
content control can serve as an endpoint; paragraphs in tables, table cells, and text boxes
cannot. Both endpoints are included by default; setting either inclusion flag false excludes
that endpoint's block. With no `end_anchor`, `count` blocks are copied beginning at the start
block or the following block when `include_start=False`. With an `end_anchor`, `count` is
validated but does not limit the range. `include_end=False` without an end anchor raises
`ValueError`. Refuses an empty adjusted range, a protected destination, an endpoint that is
missing, ambiguous, or spans multiple paragraphs, and source content this package cannot
carry over: revisions, comments, OLE objects, EMF/WMF images. Live source
endpoints may come from any supported inspection view. A live destination
must come from ``view="current"``; reacquire a historical destination before
composing.

## `rollback_on_error`

```python
rollback_on_error(document: 'Document', *participants: Any) -> Generator[None, None, None]
```

Restore the live package and named mutable proxies after an error.
