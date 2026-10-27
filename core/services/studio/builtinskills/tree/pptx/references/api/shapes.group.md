<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.shapes.group`

GroupShape and related objects.

## `GroupShape`

```python
GroupShape(grpSp: CT_GroupShape, parent: ProvidesPart)
```

Bases: `BaseShape`

A shape that acts as a container for other shapes.

### `click_action`

```python
click_action() -> ActionSetting
```

Unconditionally raises `TypeError`.

A group shape cannot have a click action or hover action.

### `has_text_frame`

```python
has_text_frame: bool
```

Unconditionally `False`.

A group shape does not have a textframe and cannot itself contain text. This does not
impact the ability of shapes contained by the group to each have their own text.

### `shadow`

```python
shadow() -> ShadowFormat
```

`ShadowFormat` object representing shadow effect for this group.

A `ShadowFormat` object is always returned, even when no shadow is explicitly defined on
this group shape (i.e. when the group inherits its shadow behavior).

### `shape_type`

```python
shape_type: MSO_SHAPE_TYPE
```

Member of `MsoShapeType` identifying the type of this shape.

Unconditionally `MSO_SHAPE_TYPE.GROUP` in this case

### `shapes`

```python
shapes() -> GroupShapes
```

`GroupShapes` object for this group.

The `GroupShapes` object provides access to the group's member shapes and provides methods
for adding new ones.
