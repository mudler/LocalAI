<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.revision`

Revision enumeration and resolution (paper-docx, `Document.revisions`).

## `RESOLVABLE_TYPES`

```python
RESOLVABLE_TYPES = frozenset({'insertion', 'deletion', 'format_change', 'row_insertion', 'row_deletion', 'move_from', 'move_to'})
```

## `Revision`

```python
Revision(revision_type: str, author: str, date: Optional[dt.datetime], text: str, story: str, anchor: Anchor, is_paragraph_mark: bool, _element: '_Element', _document: 'Optional[Document]' = None, _snapshot_signature: 'Tuple[_Element, ...]' = ()) -> None
```

One tracked change, addressable — and resolvable when supported.

Resolvable `revision_type`s: "insertion", "deletion", "format_change"
(`w:rPrChange`/`w:pPrChange`, run or paragraph mark), "row_insertion",
"row_deletion" (`w:trPr` row markers), and — as paired units —
"move_from"/"move_to". The exotic remainder ("table_property_change",
"cell_revision", "section_property_change", "numbering_change",
"custom_xml_revision") is enumerated and counted but resolution is
refused by name — claiming to resolve them would report false state.

### `accept`

```python
accept() -> None
```

Apply this change to the document.

Tracked moves resolve as a PAIR: accepting either site accepts both. Resolution can
remove paragraphs, rows, tables, comments and note bodies along with the markup.
Refuses a protected document, and revision types this package cannot resolve, leaving
the document untouched.

### `anchor`

```python
anchor: Anchor
```

### `author`

```python
author: str
```

### `date`

```python
date: Optional[dt.datetime]
```

### `is_paragraph_mark`

```python
is_paragraph_mark: bool
```

### `is_resolvable`

```python
is_resolvable: bool
```

### `reject`

```python
reject() -> None
```

Undo this change, restoring the pre-change content.

Tracked moves resolve as a PAIR: rejecting either site rejects both. Restores `w:delText`
back to `w:t`. Refuses a protected document, and revision types this package cannot
resolve, leaving the document untouched.

### `revision_type`

```python
revision_type: str
```

### `story`

```python
story: str
```

### `text`

```python
text: str
```

### `to_dict`

```python
to_dict() -> dict
```

## `Revisions`

```python
Revisions(document: 'Document') -> None
```

Bases: `Sequence[Revision]`

All tracked changes in a document, across every story part.

A fresh snapshot is enumerated on each `Document.revisions` access;
mutation through another snapshot invalidates this snapshot and its
previously-held `Revision` objects. Stale resolution attempts refuse.

### `accept_all`

```python
accept_all(*, author: Optional[str] = None) -> int
```

Apply every selected revision (optionally only `author`'s) and return the count.

Validates the whole selected set first, so a set holding types this package cannot
resolve refuses atomically instead of half-resolving and reporting success while Word
still shows changes. Refuses a protected document and a stale snapshot. Check
`remaining_unsupported()` before concluding the document is clean.

### `reject_all`

```python
reject_all(*, author: Optional[str] = None) -> int
```

Undo every selected revision (optionally only `author`'s) and return the count.

Same whole-set validation, refusals and census semantics as `accept_all`.

### `remaining_unsupported`

```python
remaining_unsupported() -> dict
```

{revision_type: count} for enumerated-but-unresolvable revisions.

### `to_dict`

```python
to_dict() -> dict
```

## `check_install`

```python
check_install() -> None
```

Refuse when paper-docx and python-docx are both installed.

## `rollback_on_error`

```python
rollback_on_error(document: 'Document', *participants: Any) -> Generator[None, None, None]
```

Restore the live package and named mutable proxies after an error.
