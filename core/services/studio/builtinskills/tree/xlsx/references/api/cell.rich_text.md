<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.cell.rich_text`

RichText definition

## `CellRichText`

```python
CellRichText(*args)
```

Bases: `list`

Represents a rich text string.

Initialize with a list made of pure strings or `TextBlock` elements
Can index object to access or modify individual rich text elements
it also supports the + and += operators between rich text strings
There are no user methods for this class

operations which modify the string will generally call an optimization pass afterwards,
that merges text blocks with identical formats, consecutive pure text strings,
and remove empty strings and empty text blocks

### `append`

```python
append(arg)
```

### `as_list`

```python
as_list()
```

Returns a list of the strings contained.
The main reason for this is to make editing easier.

### `extend`

```python
extend(arg)
```

### `from_tree`

```python
from_tree(node)
```

### `to_tree`

```python
to_tree()
```

Return the full XML representation

## `TextBlock`

```python
TextBlock(font, text)
```

Bases: `Strict`

Represents text string in a specific format

This class is used as part of constructing a rich text strings.

### `font`

```python
font = font
```

### `text`

```python
text = text
```

### `to_tree`

```python
to_tree()
```
