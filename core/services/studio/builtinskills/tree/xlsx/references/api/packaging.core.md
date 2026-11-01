<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.packaging.core`

## `DocumentProperties`

```python
DocumentProperties(category = None, contentStatus = None, keywords = None, lastModifiedBy = None, lastPrinted = None, revision = None, version = None, created = None, creator = 'openpyxl', description = None, identifier = None, language = None, modified = None, subject = None, title = None)
```

Bases: `Serialisable`

High-level properties of the document.
Defined in ECMA-376 Par2 Annex D

### `category`

```python
category = category
```

### `contentStatus`

```python
contentStatus = contentStatus
```

### `created`

```python
created = created or now
```

### `creator`

```python
creator = creator
```

### `description`

```python
description = description
```

### `identifier`

```python
identifier = identifier
```

### `keywords`

```python
keywords = keywords
```

### `language`

```python
language = language
```

### `lastModifiedBy`

```python
lastModifiedBy = lastModifiedBy
```

### `lastPrinted`

```python
lastPrinted = lastPrinted
```

### `last_modified_by`

```python
last_modified_by = Alias('lastModifiedBy')
```

### `modified`

```python
modified = modified or now
```

### `namespace`

```python
namespace = COREPROPS_NS
```

### `revision`

```python
revision = revision
```

### `subject`

```python
subject = subject
```

### `tagname`

```python
tagname = 'coreProperties'
```

### `title`

```python
title = title
```

### `version`

```python
version = version
```

## `NestedDateTime`

Bases: `DateTime`, `NestedText`

### `expected_type`

```python
expected_type = datetime.datetime
```

### `to_tree`

```python
to_tree(tagname = None, value = None, namespace = None)
```

## `QualifiedDateTime`

Bases: `NestedDateTime`

In certain situations Excel will complain if the additional type
attribute isn't set

### `to_tree`

```python
to_tree(tagname = None, value = None, namespace = None)
```
