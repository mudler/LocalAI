<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.formatting.formatting`

## `ConditionalFormatting`

```python
ConditionalFormatting(sqref = (), pivot = None, cfRule = (), extLst = None)
```

Bases: `Serialisable`

### `cells`

```python
cells = Alias('sqref')
```

### `cfRule`

```python
cfRule = cfRule
```

### `pivot`

```python
pivot = pivot
```

### `rules`

```python
rules = Alias('cfRule')
```

### `sqref`

```python
sqref = sqref
```

### `tagname`

```python
tagname = 'conditionalFormatting'
```

## `ConditionalFormattingList`

```python
ConditionalFormattingList()
```

Conditional formatting rules.

### `add`

```python
add(range_string, cfRule)
```

Add a rule such as ColorScaleRule, FormulaRule or CellIsRule

The priority will be added automatically.

### `max_priority`

```python
max_priority = 0
```
