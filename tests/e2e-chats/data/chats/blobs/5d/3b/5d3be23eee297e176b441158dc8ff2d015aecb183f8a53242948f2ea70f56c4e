<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.zipio`

Zip-layer machinery for the preserve-mode save.

## `FIXED_DATE_TIME`

```python
FIXED_DATE_TIME = (1980, 1, 1, 0, 0, 0)
```

## `PathIdentity`

```python
PathIdentity(requested: str, resolved: str, entry: object, occupant: object, digest: object) -> None
```

Content-bound identity of one pathname and its resolved occupant.

### `digest`

```python
digest: object
```

### `entry`

```python
entry: object
```

### `exists`

```python
exists
```

### `occupant`

```python
occupant: object
```

### `requested`

```python
requested: str
```

### `resolved`

```python
resolved: str
```

## `RAW_COPY_AVAILABLE`

```python
RAW_COPY_AVAILABLE = _probe_private_zipfile_api()
```

## `build_and_deliver`

```python
build_and_deliver(build_fn, target, *, expected_identity = None, validator = None, precommit = None, postcommit = None)
```

Build the archive DIRECTLY into the delivery temp file for path
targets (~1x file-size peak memory instead of
a whole in-memory copy), atomically replaced and fsynced; exact
``io.BytesIO`` targets keep the in-memory build.

## `build_archive_bytes`

```python
build_archive_bytes(build)
```

Run ``build(zout)`` against an in-memory archive; return its bytes.

## `copy_entry`

```python
copy_entry(zin, info, zout)
```

Copy one entry from ``zin`` into ``zout``, raw when possible.

Payload bytes are identical either way; the raw path also preserves the
compressed stream. Entry metadata is normalized for determinism.

## `deliver`

```python
deliver(data, target, *, expected_identity = None, validator = None, precommit = None, postcommit = None)
```

Deliver bytes to a path or supported transactional in-memory handle.

Path targets: temp file in the same directory + ``os.replace`` — the
original survives any mid-write crash (never in-place truncation).
An exact ``io.BytesIO`` receives one built-in state replacement. A verified
path-backed ``io.BufferedRandom`` is rebound around the same temp-file
replacement used for paths. Arbitrary streams are refused because their
write and rollback behavior cannot be proven atomic.

## `path_identity`

```python
path_identity(path, *, allow_missing = False)
```

Capture a stable, content-bound identity for ``path``.

## `raw_copy_supported`

```python
raw_copy_supported(info)
```

D10 guards: data-descriptor entries (GP flag bit 3), zip64-sized
entries, and exotic compression methods take the recompression fallback.

## `read_path_handle_snapshot`

```python
read_path_handle_snapshot(handle, *, context = 'preserve-mode workbook')
```

Return a stable snapshot for a named regular-file handle, or None.

## `read_path_snapshot`

```python
read_path_snapshot(path, *, context = 'preserve-mode workbook')
```

Read one stable regular-file snapshot and its path identity.

## `validate_target`

```python
validate_target(target)
```

Refuse unsupported file-like destinations before archive planning.

## `write_entry`

```python
write_entry(zout, name, payload, compress_type = zipfile.ZIP_DEFLATED)
```

Write one entry with deterministic metadata.
