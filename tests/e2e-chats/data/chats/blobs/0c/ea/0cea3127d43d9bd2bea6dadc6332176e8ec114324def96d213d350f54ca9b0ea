<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.chart.datalabel`

Data label-related objects.

## `DataLabel`

```python
DataLabel(ser, idx)
```

Bases: `object`

The data label associated with an individual data point.

### `font`

```python
font()
```

The `Font` object providing text formatting for this data label.

This font object is used to customize the appearance of automatically
inserted text, such as the data point value. The font applies to the
entire data label. More granular control of the appearance of custom
data label text is controlled by a font object on runs in the text
frame.

### `has_text_frame`

```python
has_text_frame
```

Return `True` if this data label has a text frame (implying it has
custom data label text), and `False` otherwise. Assigning `True`
causes a text frame to be added if not already present. Assigning
`False` causes any existing text frame to be removed along with any
text contained in the text frame.

### `position`

```python
position
```

Read/write `XlDataLabelPosition` member specifying the position
of this data label with respect to its data point, or `None` if no
position is specified. Assigning `None` causes PowerPoint to choose
the default position, which varies by chart type.

### `text_frame`

```python
text_frame
```

`TextFrame` instance for this data label, containing the text of the
data label and providing access to its text formatting properties.

## `DataLabels`

```python
DataLabels(dLbls)
```

Bases: `object`

Provides access to properties of data labels for a plot or a series.

This is not a collection and does not provide access to individual data
labels. Access to individual labels is via the `Point` object. The
properties this object provides control formatting of *all* the data
labels in its scope.

### `font`

```python
font()
```

The `Font` object that provides access to the text properties for
these data labels, such as bold, italic, etc.

### `number_format`

```python
number_format
```

Read/write string specifying the format for the numbers on this set
of data labels. Returns 'General' if no number format has been set.
Note that this format string has no effect on rendered data labels
when `number_format_is_linked` is `True`. Assigning a format
string to this property automatically sets
`number_format_is_linked` to `False`.

### `number_format_is_linked`

```python
number_format_is_linked
```

Read/write boolean specifying whether number formatting should be
taken from the source spreadsheet rather than the value of
`number_format`.

### `position`

```python
position
```

Read/write `XlDataLabelPosition` enumeration value specifying
the position of the data labels with respect to their data point, or
`None` if no position is specified. Assigning `None` causes
PowerPoint to choose the default position, which varies by chart
type.

### `show_category_name`

```python
show_category_name
```

Read/write. True when name of category should appear in label.

### `show_legend_key`

```python
show_legend_key
```

Read/write. True when data label displays legend-color swatch.

### `show_percentage`

```python
show_percentage
```

Read/write. True when data label displays percentage.

This option is not operative on all chart types. Percentage appears
on polar charts such as pie and donut.

### `show_series_name`

```python
show_series_name
```

Read/write. True when data label displays series name.

### `show_value`

```python
show_value
```

Read/write. True when label displays numeric value of datapoint.
