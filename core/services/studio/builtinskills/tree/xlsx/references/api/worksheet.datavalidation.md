<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.worksheet.datavalidation`

## `DataValidation`

```python
DataValidation(type = None, formula1 = None, formula2 = None, showErrorMessage = False, showInputMessage = False, showDropDown = False, allowBlank = False, sqref = (), promptTitle = None, errorStyle = None, error = None, prompt = None, errorTitle = None, imeMode = None, operator = None, allow_blank = None)
```

Bases: `Serialisable`

### `add`

```python
add(cell)
```

Adds a cell or cell coordinate to this validator

### `allowBlank`

```python
allowBlank = allowBlank
```

### `allow_blank`

```python
allow_blank = Alias('allowBlank')
```

### `cells`

```python
cells = Alias('sqref')
```

### `error`

```python
error = error
```

### `errorStyle`

```python
errorStyle = errorStyle
```

### `errorTitle`

```python
errorTitle = errorTitle
```

### `formula1`

```python
formula1 = formula1
```

### `formula2`

```python
formula2 = formula2
```

### `hide_drop_down`

```python
hide_drop_down = Alias('showDropDown')
```

### `imeMode`

```python
imeMode = imeMode
```

### `operator`

```python
operator = operator
```

### `prompt`

```python
prompt = prompt
```

### `promptTitle`

```python
promptTitle = promptTitle
```

### `ranges`

```python
ranges = Alias('sqref')
```

### `showDropDown`

```python
showDropDown = showDropDown
```

### `showErrorMessage`

```python
showErrorMessage = showErrorMessage
```

### `showInputMessage`

```python
showInputMessage = showInputMessage
```

### `sqref`

```python
sqref = sqref
```

### `tagname`

```python
tagname = 'dataValidation'
```

### `type`

```python
type = type
```

### `validation_type`

```python
validation_type = Alias('type')
```

## `DataValidationList`

```python
DataValidationList(disablePrompts = None, xWindow = None, yWindow = None, count = None, dataValidation = ())
```

Bases: `Serialisable`

### `append`

```python
append(dv)
```

### `count`

```python
count
```

### `dataValidation`

```python
dataValidation = dataValidation
```

### `disablePrompts`

```python
disablePrompts = disablePrompts
```

### `tagname`

```python
tagname = 'dataValidations'
```

### `to_tree`

```python
to_tree(tagname = None)
```

Need to skip validations that have no cell ranges

### `xWindow`

```python
xWindow = xWindow
```

### `yWindow`

```python
yWindow = yWindow
```

## `collapse_cell_addresses`

```python
collapse_cell_addresses(cells, input_ranges = ())
```

Collapse a collection of cell co-ordinates down into an optimal
range or collection of ranges.

E.g. Cells A1, A2, A3, B1, B2 and B3 should have the data-validation
object applied, attempt to collapse down to a single range, A1:B3.

Currently only collapsing contiguous vertical ranges (i.e. above
example results in A1:A3 B1:B3).

## `expand_cell_ranges`

```python
expand_cell_ranges(range_string)
```

Expand cell ranges to a sequence of addresses.
Reverse of collapse_cell_addresses
Eg. converts "A1:A2 B1:B2" to (A1, A2, B1, B2)
