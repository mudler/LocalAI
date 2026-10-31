<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.chart.title`

## `Title`

```python
Title(tx = None, layout = None, overlay = None, spPr = None, txPr = None, extLst = None)
```

Bases: `Serialisable`

### `body`

```python
body = Alias('txPr')
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `graphicalProperties`

```python
graphicalProperties = Alias('spPr')
```

### `layout`

```python
layout = layout
```

### `overlay`

```python
overlay = overlay
```

### `spPr`

```python
spPr = spPr
```

### `tagname`

```python
tagname = 'title'
```

### `text`

```python
text = Alias('tx')
```

### `tx`

```python
tx = tx
```

### `txPr`

```python
txPr = txPr
```

## `TitleDescriptor`

Bases: `Typed`

### `allow_none`

```python
allow_none = True
```

### `expected_type`

```python
expected_type = Title
```

## `title_maker`

```python
title_maker(text)
```
