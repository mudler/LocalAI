<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.settings`

Settings object, providing access to document-level settings.

## `Settings`

```python
Settings(element: BaseOxmlElement, parent: t.ProvidesXmlPart | None = None)
```

Bases: `ElementProxy`

Provides access to document-level settings for a document.

Accessed using the `.Document.settings` property.

### `odd_and_even_pages_header_footer`

```python
odd_and_even_pages_header_footer: bool
```

True if this document has distinct odd and even page headers and footers.

Read/write.
