<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.chart.legend`

Legend of a chart.

## `Legend`

```python
Legend(legend_elm)
```

Bases: `object`

Represents the legend in a chart. A chart can have at most one legend.

### `font`

```python
font()
```

The `Font` object that provides access to the text properties for
this legend, such as bold, italic, etc.

### `horz_offset`

```python
horz_offset
```

Adjustment of the x position of the legend from its default.
Expressed as a float between -1.0 and 1.0 representing a fraction of
the chart width. Negative values move the legend left, positive
values move it to the right. `None` if no setting is specified.

### `include_in_layout`

```python
include_in_layout
```

`True` if legend should be located inside plot area.

Read/write boolean specifying whether legend should be placed inside
the plot area. In many cases this will cause it to be superimposed on
the chart itself. Assigning `None` to this property causes any
`c:overlay` element to be removed, which is interpreted the same as
`True`. This use case should rarely be required and assigning
a boolean value is recommended.

### `position`

```python
position
```

Read/write `XlLegendPosition` enumeration value specifying the
general region of the chart in which to place the legend.
