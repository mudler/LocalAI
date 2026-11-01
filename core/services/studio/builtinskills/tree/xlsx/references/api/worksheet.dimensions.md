<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.worksheet.dimensions`

## `ColumnDimension`

```python
ColumnDimension(worksheet, index = 'A', width = DEFAULT_COLUMN_WIDTH, bestFit = False, hidden = False, outlineLevel = 0, outline_level = None, collapsed = False, style = None, min = None, max = None, customWidth = False, visible = None, auto_size = None)
```

Bases: `Dimension`

Information about the display properties of a column.

### `auto_size`

```python
auto_size = Alias('bestFit')
```

### `bestFit`

```python
bestFit = bestFit
```

### `collapsed`

```python
collapsed = collapsed
```

### `customWidth`

```python
customWidth
```

Always true if there is a width for the column

### `index`

```python
index = String()
```

### `max`

```python
max = max
```

### `min`

```python
min = min
```

### `range`

```python
range
```

Return the range of cells actually covered

### `reindex`

```python
reindex()
```

Set boundaries for column definition

### `to_tree`

```python
to_tree()
```

### `width`

```python
width = width
```

## `Dimension`

```python
Dimension(index, hidden, outlineLevel, collapsed, worksheet, visible = True, style = None)
```

Bases: `Strict`, `StyleableObject`

Information about the display properties of a row or column.

### `collapsed`

```python
collapsed = collapsed
```

### `hidden`

```python
hidden = hidden
```

### `index`

```python
index = index
```

### `outlineLevel`

```python
outlineLevel = outlineLevel
```

### `outline_level`

```python
outline_level = Alias('outlineLevel')
```

### `style`

```python
style = Alias('style_id')
```

## `DimensionHolder`

```python
DimensionHolder(worksheet, reference = 'index', default_factory = None)
```

Bases: `BoundDictionary`

Allow columns to be grouped

### `default_factory`

```python
default_factory = default_factory
```

### `group`

```python
group(start, end = None, outline_level = 1, hidden = False)
```

allow grouping a range of consecutive rows or columns together

**Parameters:**

- **start** – first row or column to be grouped (mandatory)
- **end** – last row or column to be grouped (optional, default to start)
- **outline_level** – outline level
- **hidden** – should the group be hidden on workbook open or not

### `max_outline`

```python
max_outline = None
```

### `to_tree`

```python
to_tree()
```

### `worksheet`

```python
worksheet = worksheet
```

## `RowDimension`

```python
RowDimension(worksheet, index = 0, ht = None, customHeight = None, s = None, customFormat = None, hidden = False, outlineLevel = 0, outline_level = None, collapsed = False, visible = None, height = None, r = None, spans = None, thickBot = None, thickTop = None, **kw)
```

Bases: `Dimension`

Information about the display properties of a row.

### `customFormat`

```python
customFormat
```

Always true if there is a style for the row

### `customHeight`

```python
customHeight
```

Always true if there is a height for the row

### `height`

```python
height = Alias('ht')
```

### `ht`

```python
ht = ht
```

### `r`

```python
r = Alias('index')
```

### `s`

```python
s = Alias('style_id')
```

### `thickBot`

```python
thickBot = thickBot
```

### `thickTop`

```python
thickTop = thickTop
```

## `SheetDimension`

```python
SheetDimension(ref = None)
```

Bases: `Serialisable`

### `boundaries`

```python
boundaries
```

### `ref`

```python
ref = ref
```

### `tagname`

```python
tagname = 'dimension'
```

## `SheetFormatProperties`

```python
SheetFormatProperties(baseColWidth = 8, defaultColWidth = None, defaultRowHeight = 15, customHeight = None, zeroHeight = None, thickTop = None, thickBottom = None, outlineLevelRow = None, outlineLevelCol = None)
```

Bases: `Serialisable`

### `baseColWidth`

```python
baseColWidth = baseColWidth
```

### `customHeight`

```python
customHeight = customHeight
```

### `defaultColWidth`

```python
defaultColWidth = defaultColWidth
```

### `defaultRowHeight`

```python
defaultRowHeight = defaultRowHeight
```

### `outlineLevelCol`

```python
outlineLevelCol = outlineLevelCol
```

### `outlineLevelRow`

```python
outlineLevelRow = outlineLevelRow
```

### `tagname`

```python
tagname = 'sheetFormatPr'
```

### `thickBottom`

```python
thickBottom = thickBottom
```

### `thickTop`

```python
thickTop = thickTop
```

### `zeroHeight`

```python
zeroHeight = zeroHeight
```
