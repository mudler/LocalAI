<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.utils.inference`

Type inference functions

## `NUMBER_REGEX`

```python
NUMBER_REGEX = re.compile('^-?([\\d]|[\\d]+\\.[\\d]*|\\.[\\d]+|[1-9][\\d]+\\.?[\\d]*)((E|e)[-+]?[\\d]+)?$')
```

## `PERCENT_REGEX`

```python
PERCENT_REGEX = re.compile('^(?P<number>\\-?[0-9]*\\.?[0-9]*\\s?)\\%$')
```

## `TIME_REGEX`

```python
TIME_REGEX = re.compile('\n^(?: # HH:MM and HH:MM:SS\n(?P<hour>[0-1]{0,1}[0-9]{2}):\n(?P<minute>[0-5][0-9]):?\n(?P<second>[0-5][0-9])?$)\n|\n^(?: # MM:SS.\n([0-5][0-9]):\n([0-5][0-9])?\\.\n(?P<microsecond>\\d{1,6}))\n', re.VERBOSE)
```

## `cast_numeric`

```python
cast_numeric(value)
```

Explicitly convert a string to a numeric value

## `cast_percentage`

```python
cast_percentage(value)
```

Explicitly convert a string to numeric value and format as a
percentage

## `cast_time`

```python
cast_time(value)
```

Explicitly convert a string to a number and format as datetime or
time
