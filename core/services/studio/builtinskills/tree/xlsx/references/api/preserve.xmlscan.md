<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.xmlscan`

Namespace-tracking streaming scanner over ORIGINAL worksheet XML bytes.

## `CellSpan`

```python
CellSpan(row, column, start)
```

### `array_ref`

```python
array_ref = None
```

### `attrs`

```python
attrs
```

### `column`

```python
column = column
```

### `end`

```python
end = None
```

### `has_extlst`

```python
has_extlst = False
```

### `has_formula`

```python
has_formula = False
```

### `has_unowned_children`

```python
has_unowned_children = False
```

### `row`

```python
row = row
```

### `shared_ref`

```python
shared_ref = None
```

### `shared_si`

```python
shared_si = None
```

### `start`

```python
start = start
```

## `RegionSpan`

```python
RegionSpan(tag, start)
```

### `end`

```python
end = None
```

### `raw`

```python
raw = None
```

### `start`

```python
start = start
```

### `tag`

```python
tag = tag
```

## `RowSpan`

```python
RowSpan(index, start)
```

### `attrs`

```python
attrs = {}
```

### `cells`

```python
cells = {}
```

### `content_end`

```python
content_end = None
```

### `content_start`

```python
content_start = None
```

### `end`

```python
end = None
```

### `index`

```python
index = index
```

### `self_closing`

```python
self_closing = False
```

### `start`

```python
start = start
```

## `ScanRefusal`

Bases: `UnsupportedStructureError`

The original sheet XML uses a construction the splice cannot edit
safely; the save refuses before writing anything.

## `SheetScan`

```python
SheetScan(data)
```

Result of scanning one worksheet part.

### `array_bounds`

```python
array_bounds = []
```

### `array_refs`

```python
array_refs = []
```

### `cache_names`

```python
cache_names = {}
```

### `data`

```python
data = data
```

### `formula_names`

```python
formula_names = {}
```

### `region_order`

```python
region_order = []
```

### `regions`

```python
regions = {}
```

### `root_end_offset`

```python
root_end_offset = None
```

### `row_order`

```python
row_order = []
```

### `rows`

```python
rows = {}
```

### `rows_monotonic`

```python
rows_monotonic = True
```

### `shared_groups`

```python
shared_groups = {}
```

### `shared_members`

```python
shared_members = {}
```

### `sheetdata`

```python
sheetdata = None
```

### `sheetdata_content`

```python
sheetdata_content = None
```

## `scan_sheet`

```python
scan_sheet(data)
```

Scan one worksheet part's bytes into a `SheetScan`.

Raises `ScanRefusal` for constructions the splice must not touch.
