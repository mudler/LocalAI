<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.packaging.relationship`

## `Relationship`

```python
Relationship(Id = None, Type = None, type = None, Target = None, TargetMode = None)
```

Bases: `Serialisable`

Represents many kinds of relationships.

`type` can be used as a shorthand with the default relationships namespace
otherwise the `Type` must be a fully qualified URL

### `Id`

```python
Id = Id
```

### `Target`

```python
Target = Target
```

### `TargetMode`

```python
TargetMode = TargetMode
```

### `Type`

```python
Type = Type
```

### `id`

```python
id = Alias('Id')
```

### `tagname`

```python
tagname = 'Relationship'
```

### `target`

```python
target = Alias('Target')
```

## `RelationshipList`

Bases: `ElementList`

### `append`

```python
append(value)
```

### `expected_type`

```python
expected_type = Relationship
```

### `find`

```python
find(content_type)
```

Find relationships by content-type
NB. these content-types namespaced objects and different to the MIME-types
in the package manifest :-(

### `get`

```python
get(key)
```

### `tagname`

```python
tagname = 'Relationships'
```

### `to_dict`

```python
to_dict()
```

Return a dictionary of relations keyed by id

### `to_tree`

```python
to_tree()
```

## `get_dependents`

```python
get_dependents(archive, filename)
```

Normalise dependency file paths to absolute ones

Relative paths are relative to parent object

## `get_rel`

```python
get_rel(archive, deps, id = None, cls = None)
```

Get related object based on id or rel_type

## `get_rels_path`

```python
get_rels_path(path)
```

Convert relative path to absolutes that can be loaded from a zip
archive.
The path to be passed in is that of containing object (workbook,
worksheet, etc.)
