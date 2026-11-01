<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.styles.styles`

Styles object, container for all objects in the styles part.

## `Styles`

```python
Styles(styles: CT_Styles)
```

Bases: `ElementProxy`

Provides access to the styles defined in a document.

Accessed using the `.Document.styles` property. Supports ``len()``, iteration,
and dictionary-style access by style name.

### `add_style`

```python
add_style(name, style_type, builtin = False)
```

Return a newly added style object of `style_type` and identified by `name`.

A builtin style can be defined by passing True for the optional `builtin`
argument.

### `default`

```python
default(style_type: WD_STYLE_TYPE)
```

Return the default style for `style_type` or `None` if no default is defined
for that type (not common).

### `get_by_id`

```python
get_by_id(style_id: str | None, style_type: WD_STYLE_TYPE)
```

Return the style of `style_type` matching `style_id`.

Returns the default for `style_type` if `style_id` is not found or is `None`, or
if the style having `style_id` is not of `style_type`.

### `get_style_id`

```python
get_style_id(style_or_name, style_type)
```

Return the id of the style corresponding to `style_or_name`, or `None` if
`style_or_name` is `None`.

If `style_or_name` is not a style object, the style is looked up using
`style_or_name` as a style name, raising `ValueError` if no style with that name
is defined. Raises `ValueError` if the target style is not of `style_type`.

### `latent_styles`

```python
latent_styles
```

A `LatentStyles` object providing access to the default behaviors for latent
styles and the collection of `_LatentStyle` objects that define overrides of
those defaults for a particular named latent style.
