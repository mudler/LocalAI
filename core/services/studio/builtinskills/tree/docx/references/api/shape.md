<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.shape`

Objects related to shapes.

## `InlineShape`

```python
InlineShape(inline: CT_Inline)
```

Proxy for an ``<wp:inline>`` element, representing the container for an inline
graphical object.

### `height`

```python
height: Length
```

Read/write.

The display height of this inline shape as an `Emu` instance.

### `type`

```python
type
```

The type of this inline shape as a member of
``docx.enum.shape.WD_INLINE_SHAPE``, e.g. ``LINKED_PICTURE``.

Read-only.

### `width`

```python
width
```

Read/write.

The display width of this inline shape as an `Emu` instance.

## `InlineShapes`

```python
InlineShapes(body_elm: CT_Body, parent: StoryPart)
```

Bases: `Parented`

Sequence of `InlineShape` instances, supporting len(), iteration, and indexed access.
