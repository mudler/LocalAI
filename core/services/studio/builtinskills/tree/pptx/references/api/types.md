<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.types`

Abstract types used by `python-pptx`.

## `ProvidesExtents`

Bases: `Protocol`

An object that has width and height.

### `height`

```python
height: Length
```

Distance between top and bottom extents of shape in EMUs.

### `width`

```python
width: Length
```

Distance between left and right extents of shape in EMUs.

## `ProvidesPart`

Bases: `Protocol`

An object that provides access to its XmlPart.

This type is for objects that need access to their part, possibly because they need access to
the package or related parts.

### `part`

```python
part: XmlPart
```
