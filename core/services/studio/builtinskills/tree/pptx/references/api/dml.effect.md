<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.dml.effect`

Visual effects on a shape such as shadow, glow, and reflection.

## `ShadowFormat`

```python
ShadowFormat(spPr)
```

Bases: `object`

Provides access to shadow effect on a shape.

### `inherit`

```python
inherit
```

True if shape inherits shadow settings.

Read/write. An explicitly-defined shadow setting on a shape causes
this property to return `False`. A shape with no explicitly-defined
shadow setting inherits its shadow settings from the style hierarchy
(and so returns `True`).

Assigning `True` causes any explicitly-defined shadow setting to be
removed and inheritance is restored. Note this has the side-effect of
removing **all** explicitly-defined effects, such as glow and
reflection, and restoring inheritance for all effects on the shape.
Assigning `False` causes the inheritance link to be broken and **no**
effects to appear on the shape.
