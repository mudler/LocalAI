<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.styles.numbers`

## `BUILTIN_FORMATS`

```python
BUILTIN_FORMATS = {0: 'General', 1: '0', 2: '0.00', 3: '#,##0', 4: '#,##0.00', 5: '"$"#,##0_);("$"#,##0)', 6: '"$"#,##0_);[Red]("$"#,##0)', 7: '"$"#,##0.00_);("$"#,##0.00)', 8: '"$"#,##0.00_);[Red]("$"#,##0.00)', 9: '0%', 10: '0.00%', 11: '0.00E+00', 12: '# ?/?', 13: '# ??/??', 14: 'mm-dd-yy', 15: 'd-mmm-yy', 16: 'd-mmm', 17: 'mmm-yy', 18: 'h:mm AM/PM', 19: 'h:mm:ss AM/PM', 20: 'h:mm', 21: 'h:mm:ss', 22: 'm/d/yy h:mm', 37: '#,##0_);(#,##0)', 38: '#,##0_);Red', 39: '#,##0.00_);(#,##0.00)', 40: '#,##0.00_);Red', 41: '_(* #,##0_);_(* \\(#,##0\\);_(* "-"_);_(@_)', 42: '_("$"* #,##0_);_("$"* \\(#,##0\\);_("$"* "-"_);_(@_)', 43: '_(* #,##0.00_);_(* \\(#,##0.00\\);_(* "-"??_);_(@_)', 44: '_("$"* #,##0.00_)_("$"* \\(#,##0.00\\)_("$"* "-"??_)_(@_)', 45: 'mm:ss', 46: '[h]:mm:ss', 47: 'mmss.0', 48: '##0.0E+0', 49: '@'}
```

## `BUILTIN_FORMATS_MAX_SIZE`

```python
BUILTIN_FORMATS_MAX_SIZE = 164
```

## `BUILTIN_FORMATS_REVERSE`

```python
BUILTIN_FORMATS_REVERSE = dict([(value, key) for key, value in BUILTIN_FORMATS.items()])
```

## `COLORS`

```python
COLORS = '\\[(BLACK`BLUE`CYAN`GREEN`MAGENTA`RED`WHITE|YELLOW)\\]'
```

## `FORMAT_CURRENCY_EUR_SIMPLE`

```python
FORMAT_CURRENCY_EUR_SIMPLE = '[$EUR ]#,##0.00_-'
```

## `FORMAT_CURRENCY_USD`

```python
FORMAT_CURRENCY_USD = '$#,##0_-'
```

## `FORMAT_CURRENCY_USD_SIMPLE`

```python
FORMAT_CURRENCY_USD_SIMPLE = '"$"#,##0.00_-'
```

## `FORMAT_DATE_DATETIME`

```python
FORMAT_DATE_DATETIME = 'yyyy-mm-dd h:mm:ss'
```

## `FORMAT_DATE_DDMMYY`

```python
FORMAT_DATE_DDMMYY = 'dd/mm/yy'
```

## `FORMAT_DATE_DMMINUS`

```python
FORMAT_DATE_DMMINUS = 'd-m'
```

## `FORMAT_DATE_DMYMINUS`

```python
FORMAT_DATE_DMYMINUS = 'd-m-y'
```

## `FORMAT_DATE_DMYSLASH`

```python
FORMAT_DATE_DMYSLASH = 'd/m/y'
```

## `FORMAT_DATE_MYMINUS`

```python
FORMAT_DATE_MYMINUS = 'm-y'
```

## `FORMAT_DATE_TIME1`

```python
FORMAT_DATE_TIME1 = BUILTIN_FORMATS[18]
```

## `FORMAT_DATE_TIME2`

```python
FORMAT_DATE_TIME2 = BUILTIN_FORMATS[19]
```

## `FORMAT_DATE_TIME3`

```python
FORMAT_DATE_TIME3 = BUILTIN_FORMATS[20]
```

## `FORMAT_DATE_TIME4`

```python
FORMAT_DATE_TIME4 = BUILTIN_FORMATS[21]
```

## `FORMAT_DATE_TIME5`

```python
FORMAT_DATE_TIME5 = BUILTIN_FORMATS[45]
```

## `FORMAT_DATE_TIME6`

```python
FORMAT_DATE_TIME6 = BUILTIN_FORMATS[21]
```

## `FORMAT_DATE_TIME7`

```python
FORMAT_DATE_TIME7 = 'i:s.S'
```

## `FORMAT_DATE_TIME8`

```python
FORMAT_DATE_TIME8 = 'h:mm:ss@'
```

## `FORMAT_DATE_TIMEDELTA`

```python
FORMAT_DATE_TIMEDELTA = '[hh]:mm:ss'
```

## `FORMAT_DATE_XLSX14`

```python
FORMAT_DATE_XLSX14 = BUILTIN_FORMATS[14]
```

## `FORMAT_DATE_XLSX15`

```python
FORMAT_DATE_XLSX15 = BUILTIN_FORMATS[15]
```

## `FORMAT_DATE_XLSX16`

```python
FORMAT_DATE_XLSX16 = BUILTIN_FORMATS[16]
```

## `FORMAT_DATE_XLSX17`

```python
FORMAT_DATE_XLSX17 = BUILTIN_FORMATS[17]
```

## `FORMAT_DATE_XLSX22`

```python
FORMAT_DATE_XLSX22 = BUILTIN_FORMATS[22]
```

## `FORMAT_DATE_YYMMDD`

```python
FORMAT_DATE_YYMMDD = 'yy-mm-dd'
```

## `FORMAT_DATE_YYMMDDSLASH`

```python
FORMAT_DATE_YYMMDDSLASH = 'yy/mm/dd@'
```

## `FORMAT_DATE_YYYYMMDD2`

```python
FORMAT_DATE_YYYYMMDD2 = 'yyyy-mm-dd'
```

## `FORMAT_GENERAL`

```python
FORMAT_GENERAL = BUILTIN_FORMATS[0]
```

## `FORMAT_NUMBER`

```python
FORMAT_NUMBER = BUILTIN_FORMATS[1]
```

## `FORMAT_NUMBER_00`

```python
FORMAT_NUMBER_00 = BUILTIN_FORMATS[2]
```

## `FORMAT_NUMBER_COMMA_SEPARATED1`

```python
FORMAT_NUMBER_COMMA_SEPARATED1 = BUILTIN_FORMATS[4]
```

## `FORMAT_NUMBER_COMMA_SEPARATED2`

```python
FORMAT_NUMBER_COMMA_SEPARATED2 = '#,##0.00_-'
```

## `FORMAT_PERCENTAGE`

```python
FORMAT_PERCENTAGE = BUILTIN_FORMATS[9]
```

## `FORMAT_PERCENTAGE_00`

```python
FORMAT_PERCENTAGE_00 = BUILTIN_FORMATS[10]
```

## `FORMAT_TEXT`

```python
FORMAT_TEXT = BUILTIN_FORMATS[49]
```

## `LITERAL_GROUP`

```python
LITERAL_GROUP = '".*?"'
```

## `LOCALE_GROUP`

```python
LOCALE_GROUP = '\\[(?!hh?\\]|mm?\\]|ss?\\])[^\\]]*\\]'
```

## `NumberFormat`

```python
NumberFormat(numFmtId = None, formatCode = None)
```

Bases: `Serialisable`

### `formatCode`

```python
formatCode = formatCode
```

### `numFmtId`

```python
numFmtId = numFmtId
```

## `NumberFormatDescriptor`

Bases: `String`

## `NumberFormatList`

```python
NumberFormatList(count = None, numFmt = ())
```

Bases: `Serialisable`

### `count`

```python
count
```

### `numFmt`

```python
numFmt = numFmt
```

## `STRIP_RE`

```python
STRIP_RE = re.compile(f'{LITERAL_GROUP}|{LOCALE_GROUP}')
```

## `TIMEDELTA_RE`

```python
TIMEDELTA_RE = re.compile('\\[hh?\\](:mm(:ss(\\.0*)?)?)?|\\[mm?\\](:ss(\\.0*)?)?|\\[ss?\\](\\.0*)?', re.I)
```

## `builtin_format_code`

```python
builtin_format_code(index)
```

Return one of the standard format codes by index.

## `builtin_format_id`

```python
builtin_format_id(fmt)
```

Return the id of a standard style.

## `is_builtin`

```python
is_builtin(fmt)
```

## `is_date_format`

```python
is_date_format(fmt)
```

## `is_datetime`

```python
is_datetime(fmt)
```

Return date, time or datetime

## `is_timedelta_format`

```python
is_timedelta_format(fmt)
```
