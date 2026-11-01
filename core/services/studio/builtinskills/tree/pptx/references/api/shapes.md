<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.shapes`

Objects used across sub-package.

## `Subshape`

```python
Subshape(parent: ProvidesPart)
```

Bases: `object`

Provides access to the containing part for drawing elements that occur below a shape.

Access to the part is required for example to add or drop a relationship. Provides
`self._parent` attribute to subclasses.

### `part`

```python
part: XmlPart
```

The package part containing this object.
