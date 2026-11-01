<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.styles.style`

Style object hierarchy.

## `BaseStyle`

```python
BaseStyle(style_elm: CT_Style)
```

Bases: `ElementProxy`

Base class for the various types of style object, paragraph, character, table,
and numbering.

These properties and methods are inherited by all style objects.

### `builtin`

```python
builtin
```

Read-only.

`True` if this style is a built-in style. `False` indicates it is a custom
(user-defined) style. Note this value is based on the presence of a
`customStyle` attribute in the XML, not on specific knowledge of which styles
are built into Word.

### `delete`

```python
delete()
```

Remove this style definition from the document.

Note that calling this method does not remove or change the style applied to any
document content. Content items having the deleted style will be rendered using
the default style, as is any content with a style not defined in the document.

### `hidden`

```python
hidden
```

`True` if display of this style in the style gallery and list of recommended
styles is suppressed.

`False` otherwise. In order to be shown in the style gallery, this value must be
`False` and `.quick_style` must be `True`.

### `locked`

```python
locked
```

Read/write Boolean.

`True` if this style is locked. A locked style does not appear in the styles
panel or the style gallery and cannot be applied to document content. This
behavior is only active when formatting protection is turned on for the document
(via the Developer menu).

### `name`

```python
name
```

The UI name of this style.

### `priority`

```python
priority
```

The integer sort key governing display sequence of this style in the Word UI.

`None` indicates no setting is defined, causing Word to use the default value of
0. Style name is used as a secondary sort key to resolve ordering of styles
having the same priority value.

### `quick_style`

```python
quick_style
```

`True` if this style should be displayed in the style gallery when
`.hidden` is `False`.

Read/write Boolean.

### `style_id`

```python
style_id: str
```

The unique key name (string) for this style.

This value is subject to rewriting by Word and should generally not be changed
unless you are familiar with the internals involved.

### `type`

```python
type
```

Member of `WdStyleType` corresponding to the type of this style, e.g.
``WD_STYLE_TYPE.PARAGRAPH``.

### `unhide_when_used`

```python
unhide_when_used
```

`True` if an application should make this style visible the next time it is
applied to content.

False otherwise. Note that `docx` does not automatically unhide a style having
`True` for this attribute when it is applied to content.

## `CharacterStyle`

Bases: `BaseStyle`

A character style.

A character style is applied to a `Run` object and primarily provides character-
level formatting via the `Font` object in its `.font` property.

### `base_style`

```python
base_style
```

Style object this style inherits from or `None` if this style is not based on
another style.

### `font`

```python
font
```

The `Font` object providing access to the character formatting properties for
this style, such as font name and size.

## `ParagraphStyle`

Bases: `CharacterStyle`

A paragraph style.

A paragraph style provides both character formatting and paragraph formatting such
as indentation and line-spacing.

### `next_paragraph_style`

```python
next_paragraph_style
```

`_ParagraphStyle` object representing the style to be applied automatically
to a new paragraph inserted after a paragraph of this style.

Returns self if no next paragraph style is defined. Assigning `None` or `self`
removes the setting such that new paragraphs are created using this same style.

### `paragraph_format`

```python
paragraph_format
```

The `ParagraphFormat` object providing access to the paragraph formatting
properties for this style such as indentation.

## `StyleFactory`

```python
StyleFactory(style_elm: CT_Style) -> BaseStyle
```

Return `Style` object of appropriate `BaseStyle` subclass for `style_elm`.
