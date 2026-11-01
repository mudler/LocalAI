<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.styles.named_styles`

## `NamedStyle`

```python
NamedStyle(name = 'Normal', font = None, fill = None, border = None, alignment = None, number_format = None, protection = None, builtinId = None, hidden = False)
```

Bases: `Serialisable`

Named and editable styles

### `alignment`

```python
alignment = alignment or Alignment()
```

### `as_name`

```python
as_name()
```

Return relevant named style

### `as_tuple`

```python
as_tuple()
```

Return a style array representing the current style

### `as_xf`

```python
as_xf()
```

Return equivalent XfStyle

### `bind`

```python
bind(wb)
```

Bind a named style to a workbook

### `border`

```python
border = border or Border()
```

### `builtinId`

```python
builtinId = builtinId
```

### `fill`

```python
fill = fill or PatternFill()
```

### `font`

```python
font = font or Font()
```

### `hidden`

```python
hidden = hidden
```

### `name`

```python
name = name
```

### `number_format`

```python
number_format = number_format
```

### `protection`

```python
protection = protection or Protection()
```

## `NamedStyleList`

```python
NamedStyleList(iterable = ())
```

Bases: `list`

Named styles are editable and can be applied to multiple objects

As only the index is stored in referencing objects the order mus
be preserved.

Returns a list of NamedStyles

Allow a list of named styles to be passed in and index them.

### `append`

```python
append(style)
```

### `names`

```python
names
```
