<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.package`

Package-level perception: semantic XML comparison and part-by-part

## `CellsDiff`

```python
CellsDiff(changes, sheets_added, sheets_removed)
```

### `SCHEMA`

```python
SCHEMA = 'cells_diff'
```

### `VERSION`

```python
VERSION = 1
```

### `changes`

```python
changes = changes
```

### `clean`

```python
clean
```

### `sheets_added`

```python
sheets_added = sheets_added
```

### `sheets_removed`

```python
sheets_removed = sheets_removed
```

### `to_dict`

```python
to_dict()
```

## `PackageDiff`

```python
PackageDiff(added, removed, changed, identical, equivalent)
```

Part-by-part diff of two OOXML packages.

XML parts are compared semantically; binary parts by size and SHA-256.
``identical`` counts parts whose payloads are byte-identical (a stricter
condition than semantic equivalence; byte-identical XML parts are never
parsed at all).

### `SCHEMA`

```python
SCHEMA = 'package_diff'
```

### `VERSION`

```python
VERSION = 1
```

### `added`

```python
added = sorted(added)
```

### `changed`

```python
changed = sorted(changed, key=lambda c: c.part)
```

### `clean`

```python
clean
```

No parts added, removed, or semantically changed.

### `equivalent`

```python
equivalent = sorted(equivalent)
```

### `identical`

```python
identical = sorted(identical)
```

### `removed`

```python
removed = sorted(removed)
```

### `to_dict`

```python
to_dict()
```

## `PartChange`

```python
PartChange(part, kind, detail)
```

One changed part in a package diff.

### `detail`

```python
detail = detail
```

### `kind`

```python
kind = kind
```

### `part`

```python
part = part
```

### `to_dict`

```python
to_dict()
```

## `diff_cells`

```python
diff_cells(a, b)
```

Cell-level semantic diff of two packages (paths, bytes, or binary
file-likes). Deterministic order: sheet, then row, then column.

## `diff_package`

```python
diff_package(a, b, max_detail = 25)
```

Diff two packages (paths, bytes, or binary file-likes) part by part.

## `xml_equivalent`

```python
xml_equivalent(a, b)
```

True when two XML payloads are semantically equivalent.

Never normalizes cell text content.

## `xml_semantic_diff`

```python
xml_semantic_diff(a, b, max_diffs = 25)
```

Semantic differences between two XML payloads (paths/bytes/file-likes).

Compared: element structure, Clark-qualified tags (namespace *prefixes*
are insignificant), attributes (order-insensitive), and text content —
which is never normalized. Returns a list of human-readable differences,
empty when equivalent.
