<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.formula.translate`

This module contains code to translate formulae across cells in a worksheet.

## `Translator`

```python
Translator(formula, origin)
```

Modifies a formula so that it can be translated from one cell to another.

`formula`: The str string to translate. Must include the leading '='
           character.
`origin`: The cell address (in A1 notation) where this formula was
          defined (excluding the worksheet name).

### `CELL_REF_RE`

```python
CELL_REF_RE = re.compile('(\\$?[A-Za-z]{1,3})(\\$?[1-9][0-9]{0,6})$')
```

### `COL_RANGE_RE`

```python
COL_RANGE_RE = re.compile('(\\$?[A-Za-z]{1,3}):(\\$?[A-Za-z]{1,3})$')
```

### `ROW_RANGE_RE`

```python
ROW_RANGE_RE = re.compile('(\\$?[1-9][0-9]{0,6}):(\\$?[1-9][0-9]{0,6})$')
```

### `get_tokens`

```python
get_tokens()
```

Returns a list with the tokens comprising the formula.

### `strip_ws_name`

```python
strip_ws_name(range_str)
```

Splits out the worksheet reference, if any, from a range reference.

### `tokenizer`

```python
tokenizer = Tokenizer(formula)
```

### `translate_col`

```python
translate_col(col_str, cdelta)
```

Translate a range col-snippet by the given number of columns

### `translate_formula`

```python
translate_formula(dest = None, row_delta = 0, col_delta = 0)
```

Convert the formula into A1 notation, or as row and column coordinates

The formula is converted into A1 assuming it is assigned to the cell
whose address is `dest` (no worksheet name).

### `translate_range`

```python
translate_range(range_str, rdelta, cdelta)
```

Translate an A1-style range reference to the destination cell.

`rdelta`: the row offset to add to the range
`cdelta`: the column offset to add to the range
`range_str`: an A1-style reference to a range. Potentially includes
             the worksheet reference. Could also be a named range.

### `translate_row`

```python
translate_row(row_str, rdelta)
```

Translate a range row-snippet by the given number of rows.

## `TranslatorError`

Bases: `Exception`

Raised when a formula can't be translated across cells.

This error arises when a formula's references would be translated outside
the worksheet's bounds on the top or left. Excel represents these
situations with a #REF! literal error. E.g., if the formula at B2 is
'=A1', attempting to translate the formula to B1 raises TranslatorError,
since there's no cell above A1. Similarly, translating the same formula
from B2 to A2 raises TranslatorError, since there's no cell to the left of
A1.
