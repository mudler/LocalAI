<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.workbook.protection`

## `DocumentSecurity`

```python
DocumentSecurity = WorkbookProtection
```

## `FileSharing`

```python
FileSharing(readOnlyRecommended = None, userName = None, reservationPassword = None, algorithmName = None, hashValue = None, saltValue = None, spinCount = None)
```

Bases: `Serialisable`

### `algorithmName`

```python
algorithmName = algorithmName
```

### `hashValue`

```python
hashValue = hashValue
```

### `readOnlyRecommended`

```python
readOnlyRecommended = readOnlyRecommended
```

### `reservationPassword`

```python
reservationPassword = reservationPassword
```

### `saltValue`

```python
saltValue = saltValue
```

### `spinCount`

```python
spinCount = spinCount
```

### `tagname`

```python
tagname = 'fileSharing'
```

### `userName`

```python
userName = userName
```

## `WorkbookProtection`

```python
WorkbookProtection(workbookPassword = None, workbookPasswordCharacterSet = None, revisionsPassword = None, revisionsPasswordCharacterSet = None, lockStructure = None, lockWindows = None, lockRevision = None, revisionsAlgorithmName = None, revisionsHashValue = None, revisionsSaltValue = None, revisionsSpinCount = None, workbookAlgorithmName = None, workbookHashValue = None, workbookSaltValue = None, workbookSpinCount = None)
```

Bases: `Serialisable`

### `from_tree`

```python
from_tree(node)
```

Don't hash passwords when deserialising from XML

### `lockRevision`

```python
lockRevision = lockRevision
```

### `lockStructure`

```python
lockStructure = lockStructure
```

### `lockWindows`

```python
lockWindows = lockWindows
```

### `lock_revision`

```python
lock_revision = Alias('lockRevision')
```

### `lock_structure`

```python
lock_structure = Alias('lockStructure')
```

### `lock_windows`

```python
lock_windows = Alias('lockWindows')
```

### `revision_password`

```python
revision_password = Alias('revisionsPassword')
```

### `revisionsAlgorithmName`

```python
revisionsAlgorithmName = revisionsAlgorithmName
```

### `revisionsHashValue`

```python
revisionsHashValue = revisionsHashValue
```

### `revisionsPassword`

```python
revisionsPassword
```

Return the revisions password value, regardless of hash.

### `revisionsPasswordCharacterSet`

```python
revisionsPasswordCharacterSet = revisionsPasswordCharacterSet
```

### `revisionsSaltValue`

```python
revisionsSaltValue = revisionsSaltValue
```

### `revisionsSpinCount`

```python
revisionsSpinCount = revisionsSpinCount
```

### `set_revisions_password`

```python
set_revisions_password(value = '', already_hashed = False)
```

Set a revision password on this workbook.

### `set_workbook_password`

```python
set_workbook_password(value = '', already_hashed = False)
```

Set a password on this workbook.

### `tagname`

```python
tagname = 'workbookPr'
```

### `workbookAlgorithmName`

```python
workbookAlgorithmName = workbookAlgorithmName
```

### `workbookHashValue`

```python
workbookHashValue = workbookHashValue
```

### `workbookPassword`

```python
workbookPassword
```

Return the workbook password value, regardless of hash.

### `workbookPasswordCharacterSet`

```python
workbookPasswordCharacterSet = workbookPasswordCharacterSet
```

### `workbookSaltValue`

```python
workbookSaltValue = workbookSaltValue
```

### `workbookSpinCount`

```python
workbookSpinCount = workbookSpinCount
```

### `workbook_password`

```python
workbook_password = Alias('workbookPassword')
```
