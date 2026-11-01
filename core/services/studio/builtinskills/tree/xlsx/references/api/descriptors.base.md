<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.descriptors.base`

Based on Python Cookbook 3rd Edition, 8.13

## `ASCII`

Bases: `Typed`

### `expected_type`

```python
expected_type = bytes
```

## `Alias`

```python
Alias(alias)
```

Bases: `Descriptor`

Aliases can be used when either the desired attribute name is not allowed
or confusing in Python (eg. "type") or a more descriptive name is desired
(eg. "underline" for "u")

### `alias`

```python
alias = alias
```

## `Bool`

Bases: `Convertible`

### `expected_type`

```python
expected_type = bool
```

## `Convertible`

Bases: `Typed`

Values must be convertible to a particular type

## `DEBUG`

```python
DEBUG = False
```

## `DateTime`

Bases: `Typed`

### `expected_type`

```python
expected_type = datetime.datetime
```

## `Default`

```python
Default(name = None, **kw)
```

Bases: `Typed`

When called returns an instance of the expected type.
Additional default values can be passed in to the descriptor

## `Descriptor`

```python
Descriptor(name = None, **kw)
```

### `name`

```python
name = name
```

## `Float`

Bases: `Convertible`

### `expected_type`

```python
expected_type = float
```

## `Integer`

Bases: `Convertible`

### `expected_type`

```python
expected_type = int
```

## `Length`

```python
Length(name = None, **kw)
```

Bases: `Descriptor`

## `MatchPattern`

```python
MatchPattern(name = None, **kw)
```

Bases: `Descriptor`

Values must match a regex pattern

### `allow_none`

```python
allow_none = False
```

### `test_pattern`

```python
test_pattern = re.compile(self.pattern, re.VERBOSE)
```

## `Max`

```python
Max(**kw)
```

Bases: `Convertible`

Values must be less than a `max` value

### `allow_none`

```python
allow_none = False
```

### `expected_type`

```python
expected_type = float
```

## `Min`

```python
Min(**kw)
```

Bases: `Convertible`

Values must be greater than a `min` value

### `allow_none`

```python
allow_none = False
```

### `expected_type`

```python
expected_type = float
```

## `MinMax`

Bases: `Min`, `Max`

Values must be greater than `min` value and less than a `max` one

## `NoneSet`

```python
NoneSet(name = None, **kw)
```

Bases: `Set`

'none' will be treated as None

## `Set`

```python
Set(name = None, **kw)
```

Bases: `Descriptor`

Value can only be from a set of know values

## `String`

Bases: `Typed`

### `expected_type`

```python
expected_type = str
```

## `Text`

Bases: `String`, `Convertible`

## `Tuple`

Bases: `Typed`

### `expected_type`

```python
expected_type = tuple
```

## `Typed`

```python
Typed(*args, **kw)
```

Bases: `Descriptor`

Values must of a particular type

### `allow_none`

```python
allow_none = False
```

### `expected_type`

```python
expected_type = type(None)
```

### `nested`

```python
nested = False
```
