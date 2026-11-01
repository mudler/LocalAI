<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.text.parfmt`

Paragraph-related proxy types.

## `ParagraphFormat`

Bases: `ElementProxy`

Provides access to paragraph formatting such as justification, indentation, line
spacing, space before and after, and widow/orphan control.

### `alignment`

```python
alignment
```

A member of the `WdParagraphAlignment` enumeration specifying the
justification setting for this paragraph.

A value of `None` indicates paragraph alignment is inherited from the style
hierarchy.

### `first_line_indent`

```python
first_line_indent
```

`Length` value specifying the relative difference in indentation for the
first line of the paragraph.

A positive value causes the first line to be indented. A negative value produces
a hanging indent. `None` indicates first line indentation is inherited from the
style hierarchy.

### `keep_together`

```python
keep_together
```

`True` if the paragraph should be kept "in one piece" and not broken across a
page boundary when the document is rendered.

`None` indicates its effective value is inherited from the style hierarchy.

### `keep_with_next`

```python
keep_with_next
```

`True` if the paragraph should be kept on the same page as the subsequent
paragraph when the document is rendered.

For example, this property could be used to keep a section heading on the same
page as its first paragraph. `None` indicates its effective value is inherited
from the style hierarchy.

### `left_indent`

```python
left_indent
```

`Length` value specifying the space between the left margin and the left side
of the paragraph.

`None` indicates the left indent value is inherited from the style hierarchy.
Use an `Inches` value object as a convenient way to apply indentation in units
of inches.

### `line_spacing`

```python
line_spacing
```

`float` or `Length` value specifying the space between baselines in
successive lines of the paragraph.

A value of `None` indicates line spacing is inherited from the style hierarchy.
A float value, e.g. ``2.0`` or ``1.75``, indicates spacing is applied in
multiples of line heights. A `Length` value such as ``Pt(12)`` indicates spacing
is a fixed height. The `Pt` value class is a convenient way to apply line
spacing in units of points. Assigning `None` resets line spacing to inherit from
the style hierarchy.

### `line_spacing_rule`

```python
line_spacing_rule
```

A member of the `WdLineSpacing` enumeration indicating how the value of
`line_spacing` should be interpreted.

Assigning any of the `WdLineSpacing` members `SINGLE`,
`DOUBLE`, or `ONE_POINT_FIVE` will cause the value of
`line_spacing` to be updated to produce the corresponding line spacing.

### `page_break_before`

```python
page_break_before
```

`True` if the paragraph should appear at the top of the page following the
prior paragraph.

`None` indicates its effective value is inherited from the style hierarchy.

### `right_indent`

```python
right_indent
```

`Length` value specifying the space between the right margin and the right
side of the paragraph.

`None` indicates the right indent value is inherited from the style hierarchy.
Use a `Cm` value object as a convenient way to apply indentation in units of
centimeters.

### `space_after`

```python
space_after
```

`Length` value specifying the spacing to appear between this paragraph and
the subsequent paragraph.

`None` indicates this value is inherited from the style hierarchy. `Length`
objects provide convenience properties, such as `pt` and
`inches`, that allow easy conversion to various length units.

### `space_before`

```python
space_before
```

`Length` value specifying the spacing to appear between this paragraph and
the prior paragraph.

`None` indicates this value is inherited from the style hierarchy. `Length`
objects provide convenience properties, such as `pt` and
`cm`, that allow easy conversion to various length units.

### `tab_stops`

```python
tab_stops()
```

`TabStops` object providing access to the tab stops defined for this
paragraph format.

### `widow_control`

```python
widow_control
```

`True` if the first and last lines in the paragraph remain on the same page
as the rest of the paragraph when Word repaginates the document.

`None` indicates its effective value is inherited from the style hierarchy.
