<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.links`

Create and retarget hyperlinks on existing text.

## `add_hyperlink`

```python
add_hyperlink(document: 'Document', span: 'Span', address: str) -> Hyperlink
```

Wrap `span`'s runs in a hyperlink pointing at `address`.

Visible text stays; Word shows it in the Hyperlink character style, which this defines
when the document lacks it. Refuses a protected document, a stale or foreign span, a span
crossing paragraphs, a field result, an existing hyperlink, and a data-bound, locked or
plain-text control surface.

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
