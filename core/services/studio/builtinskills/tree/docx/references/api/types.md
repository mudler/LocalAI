<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.types`

Abstract types used by `python-docx`.

## `ProvidesStoryPart`

Bases: `Protocol`

An object that provides access to the StoryPart.

This type is for objects that have a story part like document or header as their
root part.

### `part`

```python
part: StoryPart
```

## `ProvidesXmlPart`

Bases: `Protocol`

An object that provides access to its XmlPart.

This type is for objects that need access to their part but it either isn't a
StoryPart or they don't care, possibly because they just need access to the package
or related parts.

### `part`

```python
part: XmlPart
```
