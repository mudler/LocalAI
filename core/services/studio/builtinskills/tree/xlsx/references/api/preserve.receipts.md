<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.receipts`

One artifact answering "what did that save actually do?": cells-diff

## `EditReceipt`

```python
EditReceipt(cells_changed, parts_changed, parts_added, parts_removed, confession, recalc, derived_effects = ())
```

### `SCHEMA`

```python
SCHEMA = 'edit_receipt'
```

### `VERSION`

```python
VERSION = 2
```

### `cells_changed`

```python
cells_changed = cells_changed
```

### `confession`

```python
confession = confession
```

### `derived_effects`

```python
derived_effects = list(derived_effects)
```

### `parts_added`

```python
parts_added = parts_added
```

### `parts_changed`

```python
parts_changed = parts_changed
```

### `parts_removed`

```python
parts_removed = parts_removed
```

### `recalc`

```python
recalc = recalc
```

### `to_dict`

```python
to_dict()
```

## `receipt`

```python
receipt(before, after, *, recalc = None, _ledger = None, _workbook = None)
```

Build an `EditReceipt` from two package states (paths,
bytes, or binary file-likes). ``recalc``: an oracle result
(RecalcResult/CertificationResult/Evaluation) whose
``to_dict()`` rides along. The result must carry ``artifact_sha256``
matching ``after``; unbound or cross-workbook verification refuses.
