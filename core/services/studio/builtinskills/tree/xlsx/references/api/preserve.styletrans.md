<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.styletrans`

Translate the model's style indices into the ORIGINAL file's xf indices.

## `StyleTranslator`

```python
StyleTranslator(wb, original_styles_bytes)
```

### `file_xf_count`

```python
file_xf_count = len(file_arrays)
```

### `model_to_file_table`

```python
model_to_file_table()
```

`{model index: file index}` for every current model array — used to
rewrite the s attributes of freshly generated (added-sheet) parts.

### `render_new_numfmts`

```python
render_new_numfmts()
```

Serialized <numFmt> elements allocated during xf rendering.

### `render_new_xfs`

```python
render_new_xfs()
```

Serialized <xf> elements for the appended entries (mirrors
styles/stylesheet.py write_stylesheet), numFmtIds in FILE numbering.
Call after every resolve() — i.e. after all sheets are planned.

### `resolve`

```python
resolve(style_array)
```

The FILE xf index for a model StyleArray (allocating an appended
xf when the file has no equivalent).

### `resolver`

```python
resolver()
```

A per-cell callable for the splice writer.
