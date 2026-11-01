<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.rewrite`

Rewrite references for row/column inserts and deletes the way EXCEL

## `EXCEL_MAX_COL`

```python
EXCEL_MAX_COL = 16384
```

## `EXCEL_MAX_ROW`

```python
EXCEL_MAX_ROW = 1048576
```

## `REF_ERROR`

```python
REF_ERROR = '#REF!'
```

## `rename_sheet_in_formula`

```python
rename_sheet_in_formula(formula, old_title, new_title)
```

Rewrite sheet-prefixed references from ``old_title`` to
``new_title`` (case-insensitive, quote-aware, 3-D span endpoints
included). Returns (new_formula, changed).

## `rename_sheet_in_formula_fragment`

```python
rename_sheet_in_formula_fragment(value, old_title, new_title)
```

## `rename_sheets_in_formula`

```python
rename_sheets_in_formula(formula, mapping)
```

Simultaneous multi-title rewrite: every sheet component maps
through ``mapping`` (casefold keys resolved per component) exactly
once — a swap can never cascade.

## `rename_sheets_in_formula_fragment`

```python
rename_sheets_in_formula_fragment(value, mapping)
```

Rename sheet references in a CF/DV formula with optional ``=``.

## `row_mapping`

```python
row_mapping(operation, index, amount)
```

old_row -> new_row, or None when the row is deleted.

## `shift_cell_range`

```python
shift_cell_range(cell_range, axis, index, amount, is_delete)
```

Shift a CellRange in place. Returns 'changed', 'unchanged' or
'deleted' (range fully inside a deleted zone — the caller removes it).

## `shift_formula`

```python
shift_formula(formula, context_sheet, target_sheet, axis, index, amount, is_delete)
```

Rewrite one formula for a shift on ``target_sheet``.

``context_sheet`` is the sheet the formula lives on (unprefixed
references resolve to it). Returns (new_formula, changed).

## `shift_formula_fragment`

```python
shift_formula_fragment(value, context_sheet, target_sheet, axis, index, amount, is_delete)
```

Rewrite a CF/DV formula, which may legally omit the leading ``=``.

## `shift_name_value`

```python
shift_name_value(value, target_sheet, axis, index, amount, is_delete)
```

Defined-name / print-area values are formula fragments with explicit
sheet prefixes; rewrite them with the same machinery.

## `shift_ref`

```python
shift_ref(ref, axis, index, amount, is_delete)
```

Shift one bare A1 reference (no sheet prefix). Returns the new text,
``ref`` unchanged when unaffected, or ``#REF!``. ``$`` markers are kept
positionally — insert/delete moves absolutes too (Excel semantics).

## `title_in_string_literals`

```python
title_in_string_literals(formula, title)
```

True when a formula's STRING literals mention ``title`` — the
textual (INDIRECT-style) references a rename cannot rewrite.
