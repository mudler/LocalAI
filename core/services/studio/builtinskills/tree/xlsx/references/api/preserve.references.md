<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.references`

Modeled reference surfaces shared by structural edits and renames.

## `FormulaSurface`

```python
FormulaSurface(sheet, owner, attribute, label, *, index = None, name = False, prefix = False, cell = False, range_ref = False)
```

A formula-like string with enough context to rewrite it safely.

### `attribute`

```python
attribute = attribute
```

### `cell`

```python
cell = cell
```

### `index`

```python
index = index
```

### `label`

```python
label = label
```

### `name`

```python
name = name
```

### `owner`

```python
owner = owner
```

### `prefix`

```python
prefix = prefix
```

### `range_ref`

```python
range_ref = range_ref
```

### `replace`

```python
replace(value)
```

### `sheet`

```python
sheet = sheet
```

### `value`

```python
value
```

## `apply_rewrites`

```python
apply_rewrites(rewrites)
```

## `chart_source_ref_objects`

```python
chart_source_ref_objects(chart)
```

Yield every modeled chart reference, including titles and axes.

## `formula_surfaces`

```python
formula_surfaces(wb)
```

Yield all formula-like references represented by the live model.

## `plan_rename`

```python
plan_rename(wb, old_title, new_title)
```

Return all modeled reference rewrites for a worksheet rename.

## `plan_shift`

```python
plan_shift(wb, target_sheet, operation, index, amount)
```

Validate and return every modeled formula rewrite for one shift.
