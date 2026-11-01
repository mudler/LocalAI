<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.packaging.workbook`

## `ChildSheet`

```python
ChildSheet(name = None, sheetId = None, state = 'visible', id = None)
```

Bases: `Serialisable`

Represents a reference to a worksheet or chartsheet in workbook.xml

It contains the title, order and state but only an indirect reference to
the objects themselves.

### `id`

```python
id = id
```

### `name`

```python
name = name
```

### `sheetId`

```python
sheetId = sheetId
```

### `state`

```python
state = state
```

### `tagname`

```python
tagname = 'sheet'
```

## `FileRecoveryProperties`

```python
FileRecoveryProperties(autoRecover = None, crashSave = None, dataExtractLoad = None, repairLoad = None)
```

Bases: `Serialisable`

### `autoRecover`

```python
autoRecover = autoRecover
```

### `crashSave`

```python
crashSave = crashSave
```

### `dataExtractLoad`

```python
dataExtractLoad = dataExtractLoad
```

### `repairLoad`

```python
repairLoad = repairLoad
```

### `tagname`

```python
tagname = 'fileRecoveryPr'
```

## `PivotCache`

```python
PivotCache(cacheId = None, id = None)
```

Bases: `Serialisable`

### `cacheId`

```python
cacheId = cacheId
```

### `id`

```python
id = id
```

### `tagname`

```python
tagname = 'pivotCache'
```

## `WorkbookPackage`

```python
WorkbookPackage(conformance = None, fileVersion = None, fileSharing = None, workbookPr = None, workbookProtection = None, bookViews = (), sheets = (), functionGroups = None, externalReferences = (), definedNames = None, calcPr = None, oleSize = None, customWorkbookViews = (), pivotCaches = (), smartTagPr = None, smartTagTypes = None, webPublishing = None, fileRecoveryPr = None, webPublishObjects = None, extLst = None, Ignorable = None)
```

Bases: `Serialisable`

Represent the workbook file in the archive

### `Ignorable`

```python
Ignorable = NestedString(namespace='http://schemas.openxmlformats.org/markup-compatibility/2006', allow_none=True)
```

### `active`

```python
active
```

### `bookViews`

```python
bookViews = bookViews
```

### `calcPr`

```python
calcPr = calcPr
```

### `conformance`

```python
conformance = conformance
```

### `customWorkbookViews`

```python
customWorkbookViews = customWorkbookViews
```

### `definedNames`

```python
definedNames = definedNames
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `externalReferences`

```python
externalReferences = externalReferences
```

### `fileRecoveryPr`

```python
fileRecoveryPr = fileRecoveryPr
```

### `fileSharing`

```python
fileSharing = fileSharing
```

### `fileVersion`

```python
fileVersion = fileVersion
```

### `functionGroups`

```python
functionGroups = functionGroups
```

### `oleSize`

```python
oleSize = oleSize
```

### `pivotCaches`

```python
pivotCaches = pivotCaches
```

### `properties`

```python
properties = Alias('workbookPr')
```

### `sheets`

```python
sheets = sheets
```

### `smartTagPr`

```python
smartTagPr = smartTagPr
```

### `smartTagTypes`

```python
smartTagTypes = smartTagTypes
```

### `tagname`

```python
tagname = 'workbook'
```

### `to_tree`

```python
to_tree()
```

### `webPublishObjects`

```python
webPublishObjects = webPublishObjects
```

### `webPublishing`

```python
webPublishing = webPublishing
```

### `workbookPr`

```python
workbookPr = workbookPr
```

### `workbookProtection`

```python
workbookProtection = workbookProtection
```
