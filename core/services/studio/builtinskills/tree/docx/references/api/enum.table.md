<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.enum.table`

Enumerations related to tables in WordprocessingML files.

## `WD_ALIGN_VERTICAL`

```python
WD_ALIGN_VERTICAL = WD_CELL_VERTICAL_ALIGNMENT
```

## `WD_CELL_VERTICAL_ALIGNMENT`

Bases: `BaseXmlEnum`

Alias: **WD_ALIGN_VERTICAL**

Specifies the vertical alignment of text in one or more cells of a table.

Example::

    from docx.enum.table import WD_ALIGN_VERTICAL

    table = document.add_table(3, 3)
    table.cell(0, 0).vertical_alignment = WD_ALIGN_VERTICAL.BOTTOM

MS API name: `WdCellVerticalAlignment`

https://msdn.microsoft.com/en-us/library/office/ff193345.aspx

### `BOTH`

```python
BOTH = (101, 'both', "This is an option in the OpenXml spec, but not in Word itself. It's not clear what Word behavior this setting produces. If you find out please let us know and we'll update this documentation. Otherwise, probably best to avoid this option.")
```

This is an option in the OpenXml spec, but not in Word itself.

It's not clear what Word behavior this setting produces. If you find out please let
us know and we'll update this documentation. Otherwise, probably best to avoid this
option.

### `BOTTOM`

```python
BOTTOM = (3, 'bottom', 'Text is aligned to the bottom border of the cell.')
```

Text is aligned to the bottom border of the cell.

### `CENTER`

```python
CENTER = (1, 'center', 'Text is aligned to the center of the cell.')
```

Text is aligned to the center of the cell.

### `TOP`

```python
TOP = (0, 'top', 'Text is aligned to the top border of the cell.')
```

Text is aligned to the top border of the cell.

## `WD_ROW_HEIGHT`

```python
WD_ROW_HEIGHT = WD_ROW_HEIGHT_RULE
```

## `WD_ROW_HEIGHT_RULE`

Bases: `BaseXmlEnum`

Alias: **WD_ROW_HEIGHT**

Specifies the rule for determining the height of a table row

Example::

    from docx.enum.table import WD_ROW_HEIGHT_RULE

    table = document.add_table(3, 3)
    table.rows[0].height_rule = WD_ROW_HEIGHT_RULE.EXACTLY

MS API name: `WdRowHeightRule`

https://msdn.microsoft.com/en-us/library/office/ff193620.aspx

### `AT_LEAST`

```python
AT_LEAST = (1, 'atLeast', 'The row height is at least a minimum specified value.')
```

The row height is at least a minimum specified value.

### `AUTO`

```python
AUTO = (0, 'auto', 'The row height is adjusted to accommodate the tallest value in the row.')
```

The row height is adjusted to accommodate the tallest value in the row.

### `EXACTLY`

```python
EXACTLY = (2, 'exact', 'The row height is an exact value.')
```

The row height is an exact value.

## `WD_TABLE_ALIGNMENT`

Bases: `BaseXmlEnum`

Specifies table justification type.

Example::

    from docx.enum.table import WD_TABLE_ALIGNMENT

    table = document.add_table(3, 3)
    table.alignment = WD_TABLE_ALIGNMENT.CENTER

MS API name: `WdRowAlignment`

http://office.microsoft.com/en-us/word-help/HV080607259.aspx

### `CENTER`

```python
CENTER = (1, 'center', 'Center-aligned.')
```

Center-aligned.

### `LEFT`

```python
LEFT = (0, 'left', 'Left-aligned')
```

Left-aligned

### `RIGHT`

```python
RIGHT = (2, 'right', 'Right-aligned.')
```

Right-aligned.

## `WD_TABLE_DIRECTION`

Bases: `BaseEnum`

Specifies the direction in which an application orders cells in the specified
table or row.

Example::

    from docx.enum.table import WD_TABLE_DIRECTION

    table = document.add_table(3, 3)
    table.direction = WD_TABLE_DIRECTION.RTL

MS API name: `WdTableDirection`

http://msdn.microsoft.com/en-us/library/ff835141.aspx

### `LTR`

```python
LTR = (0, 'The table or row is arranged with the first column in the leftmost position.')
```

The table or row is arranged with the first column in the leftmost position.

### `RTL`

```python
RTL = (1, 'The table or row is arranged with the first column in the rightmost position.')
```

The table or row is arranged with the first column in the rightmost position.
