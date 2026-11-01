<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.inventory`

Scan a source archive for content the stock save path cannot preserve.

## `LossInventory`

```python
LossInventory()
```

What a stock save would rebuild lossily or drop, as
``{"kind", "location", "detail"}`` entries.

### `add`

```python
add(kind, location, detail)
```

### `kinds`

```python
kinds()
```

### `losses`

```python
losses = []
```

### `render`

```python
render()
```

## `scan_archive`

```python
scan_archive(archive, valid_files, keep_vba = False, rich_text = False)
```

Build a `LossInventory` from an open source ZipFile.

``valid_files`` is the archive namelist (already computed by the reader).
Read failures on individual entries are recorded loudly, never swallowed
into silence.
