<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.utils.datetime`

Manage Excel date weirdness.

## `CALENDAR_MAC_1904`

```python
CALENDAR_MAC_1904 = MAC_EPOCH
```

## `CALENDAR_WINDOWS_1900`

```python
CALENDAR_WINDOWS_1900 = WINDOWS_EPOCH
```

## `ISO_DURATION`

```python
ISO_DURATION = re.compile('PT((?P<hours>\\d+)H)?((?P<minutes>\\d+)M)?((?P<seconds>\\d+(\\.\\d{1,3})?)S)?')
```

## `ISO_FORMAT`

```python
ISO_FORMAT = '%Y-%m-%dT%H:%M:%SZ'
```

## `ISO_REGEX`

```python
ISO_REGEX = re.compile('\n(?P<date>(?P<year>\\d{4})-(?P<month>\\d{2})-(?P<day>\\d{2}))?T?\n(?P<time>(?P<hour>\\d{2}):(?P<minute>\\d{2})(:(?P<second>\\d{2})(?P<microsecond>\\.\\d{1,3})?)?)?Z?', re.VERBOSE)
```

## `MAC_EPOCH`

```python
MAC_EPOCH = datetime.datetime(1904, 1, 1)
```

## `SECS_PER_DAY`

```python
SECS_PER_DAY = 86400
```

## `WINDOWS_EPOCH`

```python
WINDOWS_EPOCH = datetime.datetime(1899, 12, 30)
```

## `days_to_time`

```python
days_to_time(value)
```

## `from_ISO8601`

```python
from_ISO8601(formatted_string)
```

Convert from a timestamp string to a datetime object. According to
18.17.4 in the specification the following ISO 8601 formats are
supported.

Dates B.1.1 and B.2.1
Times B.1.2 and B.2.2
Datetimes B.1.3 and B.2.3

There is no concept of timedeltas in the specification, but Excel
writes them (in strict OOXML mode), so these are also understood.

## `from_excel`

```python
from_excel(value, epoch = WINDOWS_EPOCH, timedelta = False)
```

Convert Excel serial to Python datetime

## `time_to_days`

```python
time_to_days(value)
```

Convert a time value to fractions of day

## `timedelta_to_days`

```python
timedelta_to_days(value)
```

Convert a timedelta value to fractions of a day

## `to_ISO8601`

```python
to_ISO8601(dt)
```

Convert from a datetime to a timestamp string.

## `to_excel`

```python
to_excel(dt, epoch = WINDOWS_EPOCH)
```

Convert Python datetime to Excel serial
