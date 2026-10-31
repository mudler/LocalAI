<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.bookmarks`

Bookmark enumeration, creation, and deletion.

## `BookmarkInfo`

```python
BookmarkInfo(name: str, bookmark_id: int, story: str, text: str) -> None
```

One bookmark: its name, id, story, and the visible text it wraps.

### `bookmark_id`

```python
bookmark_id: int
```

### `is_point`

```python
is_point: bool
```

### `name`

```python
name: str
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

## `DEL_TEXT`

```python
DEL_TEXT = qn('w:delText')
```

## `INSTR_TEXT`

```python
INSTR_TEXT = qn('w:instrText')
```

## `check_install`

```python
check_install() -> None
```

Refuse when paper-docx and python-docx are both installed.

## `create_bookmark`

```python
create_bookmark(document: 'Document', span: 'Span', name: str) -> BookmarkInfo
```

Bookmark exactly `span`'s text under `name`, and return its `BookmarkInfo`.

Use this to give a span a stable target that survives later edits; cross-references and
`add_reference_field` ride on bookmarks. Splits boundary runs so the markers wrap the
span's characters exactly. Refuses a protected document, a foreign or stale span, and a
name Word rejects or the document already uses.

## `delete_bookmark`

```python
delete_bookmark(document: 'Document', name: str) -> None
```

Remove `name`'s markers, leaving the text in place.

Use this to retire a bookmark without touching content. Refuses while a field
instruction still references the name, since a dangling REF renders "Error! Reference
source not found." in Word. Refuses a protected document and an unknown name.

## `is_direct_run_child`

```python
is_direct_run_child(element: '_Element') -> bool
```

Whether ``element`` is a direct child of a ``w:r`` element.

## `list_bookmarks`

```python
list_bookmarks(document: 'Document') -> 'List[BookmarkInfo]'
```

Every bookmark across every story, in document order.

## `project_run_child`

```python
project_run_child(element: '_Element') -> RunChildProjection
```

Return the conservative text projection for a direct ``w:r`` child.

## `require_span_owner`

```python
require_span_owner(document: 'Document', span: 'Span', *, argument: str = 'span') -> None
```

Refuse a live span captured from a different document package.

## `rollback_on_error`

```python
rollback_on_error(document: 'Document', *participants: Any) -> Generator[None, None, None]
```

Restore the live package and named mutable proxies after an error.
