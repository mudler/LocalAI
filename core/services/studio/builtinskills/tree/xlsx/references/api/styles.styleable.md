<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.styles.styleable`

## `NamedStyleDescriptor`

### `collection`

```python
collection = '_named_styles'
```

### `key`

```python
key = 'xfId'
```

## `NumberFormatDescriptor`

### `collection`

```python
collection = '_number_formats'
```

### `key`

```python
key = 'numFmtId'
```

## `StyleArrayDescriptor`

```python
StyleArrayDescriptor(key)
```

### `key`

```python
key = key
```

## `StyleDescriptor`

```python
StyleDescriptor(collection, key)
```

### `collection`

```python
collection = collection
```

### `key`

```python
key = key
```

## `StyleableObject`

```python
StyleableObject(sheet, style_array = None)
```

Base class for styleble objects implementing proxy and lookup functions

### `alignment`

```python
alignment = StyleDescriptor('_alignments', 'alignmentId')
```

### `border`

```python
border = StyleDescriptor('_borders', 'borderId')
```

### `fill`

```python
fill = StyleDescriptor('_fills', 'fillId')
```

### `font`

```python
font = StyleDescriptor('_fonts', 'fontId')
```

### `has_style`

```python
has_style
```

### `number_format`

```python
number_format = NumberFormatDescriptor()
```

### `parent`

```python
parent = sheet
```

### `pivotButton`

```python
pivotButton = StyleArrayDescriptor('pivotButton')
```

### `protection`

```python
protection = StyleDescriptor('_protections', 'protectionId')
```

### `quotePrefix`

```python
quotePrefix = StyleArrayDescriptor('quotePrefix')
```

### `style`

```python
style = NamedStyleDescriptor()
```

### `style_id`

```python
style_id
```
