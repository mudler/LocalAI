<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.fields`

Field authoring — formulas, never values.

## `add_caption`

```python
add_caption(paragraph: 'Paragraph', *, label: str = 'Figure', description: str = '') -> None
```

Append a SEQ caption (`Figure 1`, `Table 1`) to `paragraph`.

Writes the field rather than the displayed integer, so Word recomputes the number on
open. Sets the paragraph style to "Caption" without checking that the style exists.
Refuses a protected document and an unknown label.

## `add_date_field`

```python
add_date_field(paragraph: 'Paragraph', *, date_format: Optional[str] = None) -> None
```

Append a DATE field; `date_format` is Word's \@ picture, for example 'MMMM d, yyyy'.

The result stays a placeholder until a renderer opens the file; this package never
computes a date into the result. Refuses a protected document.

## `add_page_count_field`

```python
add_page_count_field(paragraph: 'Paragraph') -> None
```

Append a NUMPAGES field, typically to a footer paragraph.

Writes the field, not a number, so Word computes the count on open. Refuses a protected
document.

## `add_page_number_field`

```python
add_page_number_field(paragraph: 'Paragraph') -> None
```

Append a PAGE field, typically to a footer paragraph.

Writes the field, not a number, so Word computes the page on open. Refuses a protected
document.

## `add_reference_field`

```python
add_reference_field(paragraph: 'Paragraph', *, bookmark: str, kind: str = 'text') -> None
```

Append a cross-reference to `bookmark`: its text (`kind="text"`), page (`"page"`), or
paragraph number (`"number"`).

Writes a REF field, so Word recomputes it on open. Refuses a protected document and an
unknown bookmark name.

## `check_install`

```python
check_install() -> None
```

Refuse when paper-docx and python-docx are both installed.

## `insert_toc_after`

```python
insert_toc_after(document: 'Document', anchor: 'Document', *, levels: Tuple[int, int] = (1, 3)) -> None
```

Insert a TOC field in a new paragraph after `anchor`.

Marked dirty so the renderer builds the real table on open; heading `levels` maps to the
\o "1-3" switch. Refuses a protected document, and an anchor that is missing, ambiguous
foreign, or spans more than one paragraph.

## `rollback_on_error`

```python
rollback_on_error(document: 'Document', *participants: Any) -> Generator[None, None, None]
```

Restore the live package and named mutable proxies after an error.
