<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.descriptors.excel`

Excel specific descriptors

## `Base64Binary`

Bases: `MatchPattern`

### `pattern`

```python
pattern = '^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=|[A-Za-z0-9+/]{4})$'
```

## `CellRange`

Bases: `MatchPattern`

### `allow_none`

```python
allow_none = True
```

### `pattern`

```python
pattern = '^[$]?([A-Za-z]{1,3})[$]?(\\d+)(:[$]?([A-Za-z]{1,3})[$]?(\\d+)?)?$|^[A-Za-z]{1,3}:[A-Za-z]{1,3}$'
```

## `Coordinate`

```python
Coordinate = Integer
```

## `Extension`

```python
Extension(uri = None)
```

Bases: `Serialisable`

### `uri`

```python
uri = uri
```

## `ExtensionList`

```python
ExtensionList(ext = ())
```

Bases: `Serialisable`

### `ext`

```python
ext = ext
```

## `Guid`

Bases: `MatchPattern`

### `pattern`

```python
pattern = '{[0-9A-F]{8}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{12}\\}'
```

## `HexBinary`

Bases: `MatchPattern`

### `pattern`

```python
pattern = '[0-9a-fA-F]+$'
```

## `Percentage`

Bases: `MinMax`

### `max`

```python
max = 1000000
```

### `min`

```python
min = -1000000
```

### `pattern`

```python
pattern = '((100)|([0-9][0-9]?))(\\.[0-9][0-9]?)?%'
```

## `Relation`

Bases: `String`

### `allow_none`

```python
allow_none = True
```

### `namespace`

```python
namespace = REL_NS
```

## `TextPoint`

Bases: `MinMax`

Size in hundredths of points.
In theory other units of measurement can be used but these are unbounded

### `expected_type`

```python
expected_type = int
```

### `max`

```python
max = 400000
```

### `min`

```python
min = -400000
```

## `UniversalMeasure`

Bases: `MatchPattern`

### `pattern`

```python
pattern = '[0-9]+(\\.[0-9]+)?(mm`cm`in`pt`pc|pi)'
```
