<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.tableops`

Narrow, guarded table operations (paper-docx).

## `check_install`

```python
check_install() -> None
```

Refuse when paper-docx and python-docx are both installed.

## `delete_row`

```python
delete_row(table: 'Table', row: int) -> None
```

Delete row `row` (0-based).

Refuses the last remaining row, since a rowless table is not valid WordprocessingML.
Refuses a protected document, an out-of-range index, and a merged or nested target.

## `find_table`

```python
find_table(document: 'Document', *, near_text: str, match: str = 'exact') -> 'Table'
```

The table with one physical cell containing `near_text`.

Matching is literal and exact by default; ``match="normalized"`` folds case,
typography, and whitespace. Paragraph boundaries within one cell are represented
by literal newlines, but a match never crosses from one cell into another. Only
top-level tables in the main document body are searched.

Zero matching tables raise `TargetNotFoundError`; more than one matching table
raises `AmbiguousTargetError` — make `near_text` more specific.

## `insert_row_after`

```python
insert_row_after(table: 'Table', row: int, values: Sequence[str], *, copy_format_from: Optional[int] = None) -> None
```

Insert a row after `row` (0-based), copying formatting from `copy_format_from` and
filling `values`.

The copied template must have one direct paragraph per physical cell, containing
only plain text runs with identical complete direct run properties. Cell and
paragraph properties are preserved. Complex or conflicting templates refuse
before population with guidance to use a simpler uniform template; they are never
repaired or flattened.

Refuses a protected document, an out-of-range index, and a target row that is merged
or holds a nested table.

## `update_cell`

```python
update_cell(table: 'Table', row: int, column: int, new_text: str, *, tracked: bool = False, author: Optional[str] = None, date: Optional[dt.datetime] = None) -> ReplaceResult
```

Replace one cell's visible text and return a `ReplaceResult` (0-based `row`,
layout-grid `column`).

Runs through `Span.replace`, so a non-empty replacement inherits the target cell text's
starting run properties and reports whether later formatting regions were consumed. Guards
are cell-wise: a merged header elsewhere in the table does not block a plain target cell.
Refuses a protected document, an out-of-range address, and a marker-divided,
wrapper-divided, merged, multi-paragraph, or nested target.
