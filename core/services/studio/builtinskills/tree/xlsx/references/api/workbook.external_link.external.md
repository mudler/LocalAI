<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.workbook.external_link.external`

## `ExternalBook`

```python
ExternalBook(sheetNames = None, definedNames = (), sheetDataSet = None, id = None)
```

Bases: `Serialisable`

### `definedNames`

```python
definedNames = definedNames
```

### `id`

```python
id = id
```

### `sheetDataSet`

```python
sheetDataSet = sheetDataSet
```

### `sheetNames`

```python
sheetNames = sheetNames
```

### `tagname`

```python
tagname = 'externalBook'
```

## `ExternalCell`

```python
ExternalCell(r = None, t = None, vm = None, v = None)
```

Bases: `Serialisable`

### `r`

```python
r = r
```

### `t`

```python
t = t
```

### `v`

```python
v = v
```

### `vm`

```python
vm = vm
```

## `ExternalDefinedName`

```python
ExternalDefinedName(name = None, refersTo = None, sheetId = None)
```

Bases: `Serialisable`

### `name`

```python
name = name
```

### `refersTo`

```python
refersTo = refersTo
```

### `sheetId`

```python
sheetId = sheetId
```

### `tagname`

```python
tagname = 'definedName'
```

## `ExternalLink`

```python
ExternalLink(externalBook = None, ddeLink = None, oleLink = None, extLst = None)
```

Bases: `Serialisable`

### `externalBook`

```python
externalBook = externalBook
```

### `file_link`

```python
file_link = Typed(expected_type=Relationship, allow_none=True)
```

### `mime_type`

```python
mime_type = 'application/vnd.openxmlformats-officedocument.spreadsheetml.externalLink+xml'
```

### `path`

```python
path
```

### `tagname`

```python
tagname = 'externalLink'
```

### `to_tree`

```python
to_tree()
```

## `ExternalRow`

```python
ExternalRow(r = (), cell = None)
```

Bases: `Serialisable`

### `cell`

```python
cell = cell
```

### `r`

```python
r = r
```

## `ExternalSheetData`

```python
ExternalSheetData(sheetId = None, refreshError = None, row = ())
```

Bases: `Serialisable`

### `refreshError`

```python
refreshError = refreshError
```

### `row`

```python
row = row
```

### `sheetId`

```python
sheetId = sheetId
```

## `ExternalSheetDataSet`

```python
ExternalSheetDataSet(sheetData = None)
```

Bases: `Serialisable`

### `sheetData`

```python
sheetData = sheetData
```

## `ExternalSheetNames`

```python
ExternalSheetNames(sheetName = ())
```

Bases: `Serialisable`

### `sheetName`

```python
sheetName = sheetName
```

## `read_external_link`

```python
read_external_link(archive, book_path)
```
