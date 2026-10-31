<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.opc.phys_pkg`

Provides a general interface to a `physical` OPC package, such as a zip file.

## `GuardedZipReader`

```python
GuardedZipReader(zip_file: ZipFile)
```

Validate and stream every member of an already-open ``ZipFile``.

Construction fully validates the archive and caches member bytes.
This makes ordinary ``Document()`` opens and package-kernel reads share the
same validation, including for members not reachable from OPC relationships.

### `order`

```python
order: Tuple[str, ...]
```

Member names in central-directory order.

### `read`

```python
read(name: str) -> bytes
```

Return validated bytes for member `name`.

### `read_all`

```python
read_all() -> Tuple[Dict[str, bytes], List[str]]
```

Return a copy of validated parts and their archive order.

## `PhysPkgReader`

Factory for physical package reader objects.

## `PhysPkgWriter`

Factory for physical package writer objects.

## `preflight_zip`

```python
preflight_zip(source: object) -> None
```

Validate central-directory metadata before ``ZipFile`` opens.

This preflight reads only fixed-size records and skips variable fields
without retaining them.
