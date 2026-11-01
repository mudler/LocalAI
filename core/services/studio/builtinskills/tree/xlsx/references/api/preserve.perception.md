<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.perception`

Coarse formula-dependency analysis used by model maps and shift guards.

## `DependencySketch`

```python
DependencySketch()
```

Coarse formula-dependency map: which cells feed which.

``references`` maps each formula cell (sheet-qualified A1) to the list
of references its formula makes, as (sheet_title, bounds, raw) tuples —
bounds may contain None for open-ended (whole-row/column) ranges.
Structured references that cannot be resolved to cells are listed in
``unresolved`` (treated as always-intersecting).

### `cells_referencing`

```python
cells_referencing(sheet_title, bounds)
```

Formula cells whose references intersect ``bounds`` on the given
sheet — plus every cell with an unresolved (structured/table)
reference, reported conservatively.

### `contextual`

```python
contextual = set()
```

### `references`

```python
references = {}
```

### `to_dict`

```python
to_dict()
```

### `unresolved`

```python
unresolved = {}
```

### `volatile`

```python
volatile = set()
```

## `VOLATILE_NONDETERMINISTIC`

```python
VOLATILE_NONDETERMINISTIC = ('NOW', 'TODAY', 'RAND', 'RANDBETWEEN', 'RANDARRAY', 'CELL', 'INFO')
```

## `dependency_sketch`

```python
dependency_sketch(wb)
```

Build a `DependencySketch` from every formula in the model
(tokenizer-based; — coarse is fine).
