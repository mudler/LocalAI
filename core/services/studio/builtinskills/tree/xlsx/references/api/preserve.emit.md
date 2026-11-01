<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.emit`

Serialize single cells and rows from the model as bare fragments ready to

## `carry_attributes`

```python
carry_attributes(new_cell_bytes, original_cell_bytes, drop_metadata = False)
```

Attribute-carry rule: re-attach every original cell attribute
the replacement does not intentionally rewrite (everything except r, s,
t). cm/vm rich-value metadata drops ONLY when the cell's VALUE was
overwritten (the cell stops being a rich value) — style-only edits,
move re-emissions and dissolution re-emits must carry it.

## `carry_start_tag_attributes`

```python
carry_start_tag_attributes(new_fragment, original_fragment, skip = ())
```

## `emit_cell`

```python
emit_cell(ws, cell, style_index)
```

Bytes of one ``<c>`` element, or ``None`` when the model holds
nothing worth serializing (mirrors the stock writer's skip rule).

``style_index`` is the FILE xf index (StyleTranslator.resolve), or None.

## `emit_new_row`

```python
emit_new_row(ws, index, cells_bytes, attrs)
```

Bytes of a whole new ``<row>`` element (sorted cells already emitted).

## `patch_cell_style`

```python
patch_cell_style(original_cell_bytes, style_index)
```

## `patch_start_tag_attribute`

```python
patch_start_tag_attribute(fragment, name, value)
```

## `row_start_tag`

```python
row_start_tag(index, attrs, self_closing = False)
```

Bytes of a ``<row>`` start tag with the given display attributes.
