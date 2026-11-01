<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.notes`

Create footnotes and endnotes, including the note body and the mark.

## `add_endnote`

```python
add_endnote(document: 'Document', span: 'Span', text: str) -> int
```

Attach an endnote to `span` and return its note id.

Creates the endnotes part on first use. Same refusals as `add_footnote`: protection, a
stale or foreign span, a span outside the main body, and one inside a text box, a field
result, or a data-bound, locked or plain-text control.

## `add_footnote`

```python
add_footnote(document: 'Document', span: 'Span', text: str) -> int
```

Attach a footnote to `span` and return its note id.

Creates the footnotes part on first use. Refuses a protected document, a stale or foreign
span, a span outside the main body, and one inside a text box, a field result, or a
data-bound, locked or plain-text control.

## `check_install`

```python
check_install() -> None
```

Refuse when paper-docx and python-docx are both installed.

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
