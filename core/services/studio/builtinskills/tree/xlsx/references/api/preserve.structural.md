<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.structural`

Analyze what a row/column shift would strand.

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

## `EXCEL_MAX_COL`

```python
EXCEL_MAX_COL = 16384
```

## `EXCEL_MAX_ROW`

```python
EXCEL_MAX_ROW = 1048576
```

## `MAX_COL`

```python
MAX_COL = 1 << 20
```

## `MAX_ROW`

```python
MAX_ROW = 1 << 22
```

## `STOCK_WARNING`

```python
STOCK_WARNING = '{0}() moves cells but updates NOTHING that points at them: formulas keep their old ranges, defined names and chart series keep their old cells — the numbers that come out will look plausible and be wrong. Open the file with preserve=True to get a safety analysis instead.'
```

## `StructuralEditTransaction`

```python
StructuralEditTransaction(ws, operation, warn_protection = False, reference_rewrites = ())
```

In-memory rollback boundary for a preserve-mode row/column shift.

### `commit`

```python
commit()
```

### `operation`

```python
operation = operation
```

### `reference_rewrites`

```python
reference_rewrites = tuple(reference_rewrites)
```

### `rollback`

```python
rollback()
```

### `warn_protection`

```python
warn_protection = warn_protection
```

### `ws`

```python
ws = ws
```

## `analyze_shift`

```python
analyze_shift(ws, kind, index)
```

Everything a shift would strand, as human-readable impact lines.

## `apply_model_shift`

```python
apply_model_shift(ws, operation, index, amount, reference_rewrites = None)
```

All the reference updates stock openpyxl skips, applied to the MODEL
after the cells moved (Excel insert/delete semantics via rewrite.py).
Also rebases the positional arm snapshots so pure moves are not
mis-detected as user changes.

## `apply_shift_to_bytes`

```python
apply_shift_to_bytes(original, operation, index, amount)
```

The byte-level renumber pre-transform: deleted rows are cut; shifted
rows get their r attributes (row and cells) rewritten; every other byte
— cell contents, unmodeled attributes, spans — is copied verbatim. The
result becomes the baseline the standard splice runs against.

## `shift_blockers`

```python
shift_blockers(ws, operation, index, amount = 1)
```

Content that makes a shift unsafe to REWRITE in v0 — anything whose
references live outside the fully-modeled set. A non-empty result means
refusal (with analyze_shift providing the victim list).

## `shift_bounds`

```python
shift_bounds(kind, index)
```

The cell region a shift at ``index`` moves or destroys: everything
at or after the index (deletes also destroy the range itself).

## `validate_model_shift`

```python
validate_model_shift(ws, operation, index, amount)
```

Dry-run every modeled formula reference before cells are moved.
