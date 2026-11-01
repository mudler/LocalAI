<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.opc.packuri`

Provides the PackURI value type.

## `CONTENT_TYPES_URI`

```python
CONTENT_TYPES_URI = PackURI('/[Content_Types].xml')
```

## `PACKAGE_URI`

```python
PACKAGE_URI = PackURI('/')
```

## `PackURI`

Bases: `str`

Provides access to pack URI components such as the baseURI and the filename slice.

Behaves as `str` otherwise.

### `baseURI`

```python
baseURI: str
```

The base URI of this pack URI, the directory portion, roughly speaking.

E.g. ``'/ppt/slides'`` for ``'/ppt/slides/slide1.xml'``. For the package pseudo-
partname '/', baseURI is '/'.

### `ext`

```python
ext: str
```

The extension portion of this pack URI, e.g. ``'xml'`` for ``'/word/document.xml'``.

Note the period is not included.

### `filename`

```python
filename
```

The "filename" portion of this pack URI, e.g. ``'slide1.xml'`` for
``'/ppt/slides/slide1.xml'``.

For the package pseudo-partname '/', filename is ''.

### `from_rel_ref`

```python
from_rel_ref(baseURI: str, relative_ref: str) -> PackURI
```

The absolute PackURI formed by translating `relative_ref` onto `baseURI`.

### `idx`

```python
idx
```

Return partname index as integer for tuple partname or None for singleton
partname, e.g. ``21`` for ``'/ppt/slides/slide21.xml'`` and `None` for
``'/ppt/presentation.xml'``.

### `membername`

```python
membername
```

The pack URI with the leading slash stripped off, the form used as the Zip
file membername for the package item.

Returns '' for the package pseudo-partname '/'.

### `relative_ref`

```python
relative_ref(baseURI: str)
```

Return string containing relative reference to package item from `baseURI`.

E.g. PackURI('/ppt/slideLayouts/slideLayout1.xml') would return
'../slideLayouts/slideLayout1.xml' for baseURI '/ppt/slides'.

### `rels_uri`

```python
rels_uri
```

The pack URI of the .rels part corresponding to the current pack URI.

Only produces sensible output if the pack URI is a partname or the package
pseudo-partname '/'.
