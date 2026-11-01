<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.chart.text`

## `RichText`

```python
RichText(bodyPr = None, lstStyle = None, p = None)
```

Bases: `Serialisable`

From the specification: 21.2.2.216

This element specifies text formatting. The lstStyle element is not supported.

### `bodyPr`

```python
bodyPr = bodyPr
```

### `lstStyle`

```python
lstStyle = lstStyle
```

### `p`

```python
p = p
```

### `paragraphs`

```python
paragraphs = Alias('p')
```

### `properties`

```python
properties = Alias('bodyPr')
```

### `tagname`

```python
tagname = 'rich'
```

## `Text`

```python
Text(strRef = None, rich = None)
```

Bases: `Serialisable`

The value can be either a cell reference or a text element
If both are present then the reference will be used.

### `rich`

```python
rich = rich
```

### `strRef`

```python
strRef = strRef
```

### `tagname`

```python
tagname = 'tx'
```

### `to_tree`

```python
to_tree(tagname = None, idx = None, namespace = None)
```
