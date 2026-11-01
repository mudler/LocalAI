<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.parts.styles`

Provides StylesPart and related objects.

## `StylesPart`

Bases: `XmlPart`

Proxy for the styles.xml part containing style definitions for a document or
glossary.

### `default`

```python
default(package: OpcPackage) -> StylesPart
```

Return a newly created styles part, containing a default set of elements.

### `styles`

```python
styles
```

The `_Styles` instance containing the styles (<w:style> element proxies) for
this styles part.
