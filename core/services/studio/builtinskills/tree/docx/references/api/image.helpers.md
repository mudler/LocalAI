<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.image.helpers`

## `BIG_ENDIAN`

```python
BIG_ENDIAN = '>'
```

## `LITTLE_ENDIAN`

```python
LITTLE_ENDIAN = '<'
```

## `StreamReader`

```python
StreamReader(stream, byte_order, base_offset = 0)
```

Wraps a file-like object to provide access to structured data from a binary file.

Byte-order is configurable. `base_offset` is added to any base value provided to
calculate actual location for reads.

### `read`

```python
read(count)
```

Allow pass-through read() call.

### `read_byte`

```python
read_byte(base, offset = 0)
```

Return the int value of the byte at the file position defined by
self._base_offset + `base` + `offset`.

If `base` is None, the byte is read from the current position in the stream.

### `read_long`

```python
read_long(base, offset = 0)
```

Return the int value of the four bytes at the file position defined by
self._base_offset + `base` + `offset`.

If `base` is None, the long is read from the current position in the stream. The
endian setting of this instance is used to interpret the byte layout of the
long.

### `read_short`

```python
read_short(base, offset = 0)
```

Return the int value of the two bytes at the file position determined by
`base` and `offset`, similarly to ``read_long()`` above.

### `read_str`

```python
read_str(char_count, base, offset = 0)
```

Return a string containing the `char_count` bytes at the file position
determined by self._base_offset + `base` + `offset`.

### `seek`

```python
seek(base, offset = 0)
```

### `tell`

```python
tell()
```

Allow pass-through tell() call.
