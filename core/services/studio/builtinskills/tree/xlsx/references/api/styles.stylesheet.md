<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.styles.stylesheet`

## `Stylesheet`

```python
Stylesheet(numFmts = None, fonts = (), fills = (), borders = (), cellStyleXfs = None, cellXfs = None, cellStyles = None, dxfs = (), tableStyles = None, colors = None, extLst = None)
```

Bases: `Serialisable`

### `alignments`

```python
alignments = self.cellXfs.alignments
```

### `borders`

```python
borders = borders
```

### `cellStyleXfs`

```python
cellStyleXfs = cellStyleXfs
```

### `cellStyles`

```python
cellStyles = cellStyles
```

### `cellXfs`

```python
cellXfs = cellXfs
```

### `cell_styles`

```python
cell_styles = self.cellXfs._to_array()
```

### `colors`

```python
colors = colors
```

### `custom_formats`

```python
custom_formats
```

### `dxfs`

```python
dxfs = dxfs
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `fills`

```python
fills = fills
```

### `fonts`

```python
fonts = fonts
```

### `from_tree`

```python
from_tree(node)
```

### `named_styles`

```python
named_styles = self._merge_named_styles()
```

### `numFmts`

```python
numFmts = numFmts
```

### `number_formats`

```python
number_formats = IndexedList()
```

### `protections`

```python
protections = self.cellXfs.prots
```

### `tableStyles`

```python
tableStyles = tableStyles
```

### `tagname`

```python
tagname = 'styleSheet'
```

### `to_tree`

```python
to_tree(tagname = None, idx = None, namespace = None)
```

## `apply_stylesheet`

```python
apply_stylesheet(archive, wb)
```

Add styles to workbook if present

## `write_stylesheet`

```python
write_stylesheet(wb)
```
