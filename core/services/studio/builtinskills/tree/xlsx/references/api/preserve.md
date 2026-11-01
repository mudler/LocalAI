<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve`

Preserve mode: the original package is the source of truth; the object

## `AddressRemap`

```python
AddressRemap(sheet_title, operation, index, amount)
```

How one structural edit moved addresses:
every pre-edit address must be remapped through this, never reused.

``map('Model!B12') -> 'Model!B13'``; addresses whose cells the edit
deleted map to ``None``; addresses on other sheets (or untouched by
the shift) come back unchanged. Accepts bare cells, ranges, and
sheet-qualified forms; ``$`` markers are kept positionally, matching
the rewriter's Excel semantics.

### `amount`

```python
amount = amount
```

### `index`

```python
index = index
```

### `map`

```python
map(address)
```

### `operation`

```python
operation = operation
```

### `sheet_title`

```python
sheet_title = sheet_title
```

## `copy_format`

```python
copy_format(ws, src_cell, dst_range)
```

Atomically copy one cell's complete cell style onto a finite range.

The copied style includes font, fill, border, alignment, number format,
and protection. Values, formulas, comments, hyperlinks, validation, row
heights, and column widths are not copied.

## `diff_workbooks`

```python
diff_workbooks(a, b, remaps = ())
```

A cell-level report of how package ``b`` differs from ``a``
(paths, bytes, or file-likes). ``remaps``: AddressRemap chain from
the structural edits performed between the two states — differences
explained by a remap classify as "shifted", the rest as "changed".

## `receipt`

```python
receipt(before, after, *, recalc = None, _ledger = None, _workbook = None)
```

Build an `EditReceipt` from two package states (paths,
bytes, or binary file-likes). ``recalc``: an oracle result
(RecalcResult/CertificationResult/Evaluation) whose
``to_dict()`` rides along. The result must carry ``artifact_sha256``
matching ``after``; unbound or cross-workbook verification refuses.

## `scan_errors`

```python
scan_errors(wb)
```

Return cached values and actual formula error operands.
