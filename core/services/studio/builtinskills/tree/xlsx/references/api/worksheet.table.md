<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.worksheet.table`

## `PIVOTSTYLES`

```python
PIVOTSTYLES = tuple(['PivotStyleMedium{0}'.format(i) for i in range(1, 29)] + ['PivotStyleLight{0}'.format(i) for i in range(1, 29)] + ['PivotStyleDark{0}'.format(i) for i in range(1, 29)])
```

## `TABLESTYLES`

```python
TABLESTYLES = tuple(['TableStyleMedium{0}'.format(i) for i in range(1, 29)] + ['TableStyleLight{0}'.format(i) for i in range(1, 22)] + ['TableStyleDark{0}'.format(i) for i in range(1, 12)])
```

## `Table`

```python
Table(id = 1, displayName = None, ref = None, name = None, comment = None, tableType = None, headerRowCount = 1, insertRow = None, insertRowShift = None, totalsRowCount = None, totalsRowShown = None, published = None, headerRowDxfId = None, dataDxfId = None, totalsRowDxfId = None, headerRowBorderDxfId = None, tableBorderDxfId = None, totalsRowBorderDxfId = None, headerRowCellStyle = None, dataCellStyle = None, totalsRowCellStyle = None, connectionId = None, autoFilter = None, sortState = None, tableColumns = (), tableStyleInfo = None, extLst = None)
```

Bases: `Serialisable`

### `autoFilter`

```python
autoFilter = autoFilter
```

### `column_names`

```python
column_names
```

### `comment`

```python
comment = comment
```

### `connectionId`

```python
connectionId = connectionId
```

### `dataCellStyle`

```python
dataCellStyle = dataCellStyle
```

### `dataDxfId`

```python
dataDxfId = dataDxfId
```

### `displayName`

```python
displayName = displayName
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `headerRowBorderDxfId`

```python
headerRowBorderDxfId = headerRowBorderDxfId
```

### `headerRowCellStyle`

```python
headerRowCellStyle = headerRowCellStyle
```

### `headerRowCount`

```python
headerRowCount = headerRowCount
```

### `headerRowDxfId`

```python
headerRowDxfId = headerRowDxfId
```

### `id`

```python
id = id
```

### `insertRow`

```python
insertRow = insertRow
```

### `insertRowShift`

```python
insertRowShift = insertRowShift
```

### `mime_type`

```python
mime_type = 'application/vnd.openxmlformats-officedocument.spreadsheetml.table+xml'
```

### `name`

```python
name = name
```

### `path`

```python
path
```

Return path within the archive

### `published`

```python
published = published
```

### `ref`

```python
ref = ref
```

### `sortState`

```python
sortState = sortState
```

### `tableBorderDxfId`

```python
tableBorderDxfId = tableBorderDxfId
```

### `tableColumns`

```python
tableColumns = tableColumns
```

### `tableStyleInfo`

```python
tableStyleInfo = tableStyleInfo
```

### `tableType`

```python
tableType = tableType
```

### `tagname`

```python
tagname = 'table'
```

### `to_tree`

```python
to_tree()
```

### `totalsRowBorderDxfId`

```python
totalsRowBorderDxfId = totalsRowBorderDxfId
```

### `totalsRowCellStyle`

```python
totalsRowCellStyle = totalsRowCellStyle
```

### `totalsRowCount`

```python
totalsRowCount = totalsRowCount
```

### `totalsRowDxfId`

```python
totalsRowDxfId = totalsRowDxfId
```

### `totalsRowShown`

```python
totalsRowShown = totalsRowShown
```

## `TableColumn`

```python
TableColumn(id = None, uniqueName = None, name = None, totalsRowFunction = None, totalsRowLabel = None, queryTableFieldId = None, headerRowDxfId = None, dataDxfId = None, totalsRowDxfId = None, headerRowCellStyle = None, dataCellStyle = None, totalsRowCellStyle = None, calculatedColumnFormula = None, totalsRowFormula = None, xmlColumnPr = None, extLst = None)
```

Bases: `Serialisable`

### `calculatedColumnFormula`

```python
calculatedColumnFormula = calculatedColumnFormula
```

### `dataCellStyle`

```python
dataCellStyle = dataCellStyle
```

### `dataDxfId`

```python
dataDxfId = dataDxfId
```

### `extLst`

```python
extLst = extLst
```

### `from_tree`

```python
from_tree(node)
```

### `headerRowCellStyle`

```python
headerRowCellStyle = headerRowCellStyle
```

### `headerRowDxfId`

```python
headerRowDxfId = headerRowDxfId
```

### `id`

```python
id = id
```

### `name`

```python
name = name
```

### `queryTableFieldId`

```python
queryTableFieldId = queryTableFieldId
```

### `tagname`

```python
tagname = 'tableColumn'
```

### `totalsRowCellStyle`

```python
totalsRowCellStyle = totalsRowCellStyle
```

### `totalsRowDxfId`

```python
totalsRowDxfId = totalsRowDxfId
```

### `totalsRowFormula`

```python
totalsRowFormula = totalsRowFormula
```

### `totalsRowFunction`

```python
totalsRowFunction = totalsRowFunction
```

### `totalsRowLabel`

```python
totalsRowLabel = totalsRowLabel
```

### `uniqueName`

```python
uniqueName = uniqueName
```

### `xmlColumnPr`

```python
xmlColumnPr = xmlColumnPr
```

## `TableFormula`

```python
TableFormula(array = None, attr_text = None)
```

Bases: `Serialisable`

### `array`

```python
array = array
```

### `attr_text`

```python
attr_text = attr_text
```

### `tagname`

```python
tagname = 'tableFormula'
```

### `text`

```python
text = Alias('attr_text')
```

## `TableList`

Bases: `dict`

### `add`

```python
add(table)
```

### `get`

```python
get(name = None, table_range = None)
```

### `items`

```python
items()
```

## `TableNameDescriptor`

Bases: `String`

Table names cannot have spaces in them

## `TablePartList`

```python
TablePartList(count = None, tablePart = ())
```

Bases: `Serialisable`

### `append`

```python
append(part)
```

### `count`

```python
count
```

### `tablePart`

```python
tablePart = tablePart
```

### `tagname`

```python
tagname = 'tableParts'
```

## `TableStyleInfo`

```python
TableStyleInfo(name = None, showFirstColumn = None, showLastColumn = None, showRowStripes = None, showColumnStripes = None)
```

Bases: `Serialisable`

### `name`

```python
name = name
```

### `showColumnStripes`

```python
showColumnStripes = showColumnStripes
```

### `showFirstColumn`

```python
showFirstColumn = showFirstColumn
```

### `showLastColumn`

```python
showLastColumn = showLastColumn
```

### `showRowStripes`

```python
showRowStripes = showRowStripes
```

### `tagname`

```python
tagname = 'tableStyleInfo'
```

## `XMLColumnProps`

```python
XMLColumnProps(mapId = None, xpath = None, denormalized = None, xmlDataType = None, extLst = None)
```

Bases: `Serialisable`

### `denormalized`

```python
denormalized = denormalized
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `mapId`

```python
mapId = mapId
```

### `tagname`

```python
tagname = 'xmlColumnPr'
```

### `xmlDataType`

```python
xmlDataType = xmlDataType
```

### `xpath`

```python
xpath = xpath
```
