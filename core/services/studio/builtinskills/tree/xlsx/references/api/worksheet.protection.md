<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.worksheet.protection`

## `SheetProtection`

```python
SheetProtection(sheet = False, objects = False, scenarios = False, formatCells = True, formatRows = True, formatColumns = True, insertColumns = True, insertRows = True, insertHyperlinks = True, deleteColumns = True, deleteRows = True, selectLockedCells = False, selectUnlockedCells = False, sort = True, autoFilter = True, pivotTables = True, password = None, algorithmName = None, saltValue = None, spinCount = None, hashValue = None)
```

Bases: `Serialisable`, `_Protected`

Information about protection of various aspects of a sheet. True values
mean that protection for the object or action is active This is the
**default** when protection is active, ie. users cannot do something

### `algorithmName`

```python
algorithmName = algorithmName
```

### `autoFilter`

```python
autoFilter = autoFilter
```

### `deleteColumns`

```python
deleteColumns = deleteColumns
```

### `deleteRows`

```python
deleteRows = deleteRows
```

### `disable`

```python
disable()
```

### `enable`

```python
enable()
```

### `enabled`

```python
enabled = Alias('sheet')
```

### `formatCells`

```python
formatCells = formatCells
```

### `formatColumns`

```python
formatColumns = formatColumns
```

### `formatRows`

```python
formatRows = formatRows
```

### `hashValue`

```python
hashValue = hashValue
```

### `insertColumns`

```python
insertColumns = insertColumns
```

### `insertHyperlinks`

```python
insertHyperlinks = insertHyperlinks
```

### `insertRows`

```python
insertRows = insertRows
```

### `objects`

```python
objects = objects
```

### `password`

```python
password = password
```

### `pivotTables`

```python
pivotTables = pivotTables
```

### `saltValue`

```python
saltValue = saltValue
```

### `scenarios`

```python
scenarios = scenarios
```

### `selectLockedCells`

```python
selectLockedCells = selectLockedCells
```

### `selectUnlockedCells`

```python
selectUnlockedCells = selectUnlockedCells
```

### `set_password`

```python
set_password(value = '', already_hashed = False)
```

### `sheet`

```python
sheet = sheet
```

### `sort`

```python
sort = sort
```

### `spinCount`

```python
spinCount = spinCount
```

### `tagname`

```python
tagname = 'sheetProtection'
```
