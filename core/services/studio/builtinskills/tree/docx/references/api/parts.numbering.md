<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.parts.numbering`

`NumberingPart` and closely related objects.

## `NumberingPart`

Bases: `XmlPart`

Proxy for the numbering.xml part containing numbering definitions for a document
or glossary.

### `new`

```python
new() -> NumberingPart
```

Newly created numbering part, containing only the root ``<w:numbering>`` element.

### `numbering_definitions`

```python
numbering_definitions()
```

The `_NumberingDefinitions` instance containing the numbering definitions
(<w:num> element proxies) for this numbering part.
