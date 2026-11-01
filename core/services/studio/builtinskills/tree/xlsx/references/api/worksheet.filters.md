<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.worksheet.filters`

## `AutoFilter`

```python
AutoFilter(ref = None, filterColumn = (), sortState = None, extLst = None)
```

Bases: `Serialisable`

### `add_filter_column`

```python
add_filter_column(col_id, vals, blank = False)
```

Add row filter for specified column.

**Parameters:**

- **col_id** – Zero-origin column id. 0 means first column.
- **vals** – Value list to show.
- **blank** – Show rows that have blank cell if True (default=``False``)

### `add_sort_condition`

```python
add_sort_condition(ref, descending = False)
```

Add sort condition for cpecified range of cells.

**Parameters:**

- **ref** – range of the cells (e.g. 'A2:A150')
- **descending** – Descending sort order (default=``False``)

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `filterColumn`

```python
filterColumn = filterColumn
```

### `ref`

```python
ref = ref
```

### `sortState`

```python
sortState = sortState
```

### `tagname`

```python
tagname = 'autoFilter'
```

## `BlankFilter`

```python
BlankFilter(**kw)
```

Bases: `CustomFilter`

Exclude blanks

### `operator`

```python
operator
```

### `val`

```python
val
```

## `ColorFilter`

```python
ColorFilter(dxfId = None, cellColor = None)
```

Bases: `Serialisable`

### `cellColor`

```python
cellColor = cellColor
```

### `dxfId`

```python
dxfId = dxfId
```

### `tagname`

```python
tagname = 'colorFilter'
```

## `CustomFilter`

```python
CustomFilter(operator = 'equal', val = None)
```

Bases: `Serialisable`

### `convert`

```python
convert()
```

Convert to more specific filter

### `operator`

```python
operator = operator
```

### `tagname`

```python
tagname = 'customFilter'
```

### `val`

```python
val = val
```

## `CustomFilters`

```python
CustomFilters(_and = None, customFilter = ())
```

Bases: `Serialisable`

### `customFilter`

```python
customFilter = customFilter
```

### `tagname`

```python
tagname = 'customFilters'
```

## `DateGroupItem`

```python
DateGroupItem(year = None, month = None, day = None, hour = None, minute = None, second = None, dateTimeGrouping = None)
```

Bases: `Serialisable`

### `dateTimeGrouping`

```python
dateTimeGrouping = dateTimeGrouping
```

### `day`

```python
day = day
```

### `hour`

```python
hour = hour
```

### `minute`

```python
minute = minute
```

### `month`

```python
month = month
```

### `second`

```python
second = second
```

### `tagname`

```python
tagname = 'dateGroupItem'
```

### `year`

```python
year = year
```

## `DynamicFilter`

```python
DynamicFilter(type = None, val = None, valIso = None, maxVal = None, maxValIso = None)
```

Bases: `Serialisable`

### `maxVal`

```python
maxVal = maxVal
```

### `maxValIso`

```python
maxValIso = maxValIso
```

### `tagname`

```python
tagname = 'dynamicFilter'
```

### `type`

```python
type = type
```

### `val`

```python
val = val
```

### `valIso`

```python
valIso = valIso
```

## `FilterColumn`

```python
FilterColumn(colId = None, hiddenButton = False, showButton = True, filters = None, top10 = None, customFilters = None, dynamicFilter = None, colorFilter = None, iconFilter = None, extLst = None, blank = None, vals = None)
```

Bases: `Serialisable`

### `colId`

```python
colId = colId
```

### `col_id`

```python
col_id = Alias('colId')
```

### `colorFilter`

```python
colorFilter = colorFilter
```

### `customFilters`

```python
customFilters = customFilters
```

### `dynamicFilter`

```python
dynamicFilter = dynamicFilter
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `filters`

```python
filters = filters
```

### `hiddenButton`

```python
hiddenButton = hiddenButton
```

### `iconFilter`

```python
iconFilter = iconFilter
```

### `showButton`

```python
showButton = showButton
```

### `tagname`

```python
tagname = 'filterColumn'
```

### `top10`

```python
top10 = top10
```

## `Filters`

```python
Filters(blank = None, calendarType = None, filter = (), dateGroupItem = ())
```

Bases: `Serialisable`

### `blank`

```python
blank = blank
```

### `calendarType`

```python
calendarType = calendarType
```

### `dateGroupItem`

```python
dateGroupItem = dateGroupItem
```

### `filter`

```python
filter = filter
```

### `tagname`

```python
tagname = 'filters'
```

## `IconFilter`

```python
IconFilter(iconSet = None, iconId = None)
```

Bases: `Serialisable`

### `iconId`

```python
iconId = iconId
```

### `iconSet`

```python
iconSet = iconSet
```

### `tagname`

```python
tagname = 'iconFilter'
```

## `NumberFilter`

```python
NumberFilter(operator = 'equal', val = None)
```

Bases: `CustomFilter`

### `operator`

```python
operator = operator
```

### `val`

```python
val = val
```

## `SortCondition`

```python
SortCondition(ref = None, descending = None, sortBy = None, customList = None, dxfId = None, iconSet = None, iconId = None)
```

Bases: `Serialisable`

### `customList`

```python
customList = customList
```

### `descending`

```python
descending = descending
```

### `dxfId`

```python
dxfId = dxfId
```

### `iconId`

```python
iconId = iconId
```

### `iconSet`

```python
iconSet = iconSet
```

### `ref`

```python
ref = ref
```

### `sortBy`

```python
sortBy = sortBy
```

### `tagname`

```python
tagname = 'sortCondition'
```

## `SortState`

```python
SortState(columnSort = None, caseSensitive = None, sortMethod = None, ref = None, sortCondition = (), extLst = None)
```

Bases: `Serialisable`

### `caseSensitive`

```python
caseSensitive = caseSensitive
```

### `columnSort`

```python
columnSort = columnSort
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `ref`

```python
ref = ref
```

### `sortCondition`

```python
sortCondition = sortCondition
```

### `sortMethod`

```python
sortMethod = sortMethod
```

### `tagname`

```python
tagname = 'sortState'
```

## `StringFilter`

```python
StringFilter(operator = 'contains', val = None, exclude = False)
```

Bases: `CustomFilter`

### `exclude`

```python
exclude = exclude
```

### `operator`

```python
operator = operator
```

### `to_tree`

```python
to_tree(tagname = None, idx = None, namespace = None)
```

### `val`

```python
val = val
```

## `Top10`

```python
Top10(top = None, percent = None, val = None, filterVal = None)
```

Bases: `Serialisable`

### `filterVal`

```python
filterVal = filterVal
```

### `percent`

```python
percent = percent
```

### `tagname`

```python
tagname = 'top10'
```

### `top`

```python
top = top
```

### `val`

```python
val = val
```

## `string_format_mapping`

```python
string_format_mapping = {'contains': '*{}*', 'startswith': '{}*', 'endswith': '*{}', 'wildcard': '{}'}
```
