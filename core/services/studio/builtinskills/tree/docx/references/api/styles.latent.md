<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.styles.latent`

Latent style-related objects.

## `LatentStyles`

Bases: `ElementProxy`

Provides access to the default behaviors for latent styles in this document and
to the collection of `_LatentStyle` objects that define overrides of those defaults
for a particular named latent style.

### `add_latent_style`

```python
add_latent_style(name)
```

Return a newly added `_LatentStyle` object to override the inherited defaults
defined in this latent styles object for the built-in style having `name`.

### `default_priority`

```python
default_priority
```

Integer between 0 and 99 inclusive specifying the default sort order for
latent styles in style lists and the style gallery.

`None` if no value is assigned, which causes Word to use the default value 99.

### `default_to_hidden`

```python
default_to_hidden
```

Boolean specifying whether the default behavior for latent styles is to be
hidden.

A hidden style does not appear in the recommended list or in the style gallery.

### `default_to_locked`

```python
default_to_locked
```

Boolean specifying whether the default behavior for latent styles is to be
locked.

A locked style does not appear in the styles panel or the style gallery and
cannot be applied to document content. This behavior is only active when
formatting protection is turned on for the document (via the Developer menu).

### `default_to_quick_style`

```python
default_to_quick_style
```

Boolean specifying whether the default behavior for latent styles is to
appear in the style gallery when not hidden.

### `default_to_unhide_when_used`

```python
default_to_unhide_when_used
```

Boolean specifying whether the default behavior for latent styles is to be
unhidden when first applied to content.

### `load_count`

```python
load_count
```

Integer specifying the number of built-in styles to initialize to the
defaults specified in this `LatentStyles` object.

`None` if there is no setting in the XML (very uncommon). The default Word 2011
template sets this value to 276, accounting for the built-in styles in Word
2010.
