<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.chart.chart`

Chart-related objects such as Chart and ChartTitle.

## `Chart`

```python
Chart(chartSpace, chart_part)
```

Bases: `PartElementProxy`

A chart object.

### `category_axis`

```python
category_axis
```

The category axis of this chart. In the case of an XY or Bubble
chart, this is the X axis. Raises `ValueError` if no category
axis is defined (as is the case for a pie chart, for example).

### `chart_style`

```python
chart_style
```

Read/write integer index of chart style used to format this chart.
Range is from 1 to 48. Value is `None` if no explicit style has been
assigned, in which case the default chart style is used. Assigning
`None` causes any explicit setting to be removed. The integer index
corresponds to the style's position in the chart style gallery in the
PowerPoint UI.

### `chart_title`

```python
chart_title
```

A `ChartTitle` object providing access to title properties.

Calling this property is destructive in the sense it adds a chart
title element (`c:title`) to the chart XML if one is not already
present. Use `has_title` to test for presence of a chart title
non-destructively.

### `chart_type`

```python
chart_type
```

Member of `XlChartType` enumeration specifying type of this chart.

If the chart has two plots, for example, a line plot overlayed on a bar plot,
the type reported is for the first (back-most) plot. Read-only.

### `font`

```python
font()
```

Font object controlling text format defaults for this chart.

### `has_legend`

```python
has_legend
```

Read/write boolean, `True` if the chart has a legend. Assigning
`True` causes a legend to be added to the chart if it doesn't already
have one. Assigning False removes any existing legend definition
along with any existing legend settings.

### `has_title`

```python
has_title
```

Read/write boolean, specifying whether this chart has a title.

Assigning `True` causes a title to be added if not already present.
Assigning `False` removes any existing title along with its text and
settings.

### `legend`

```python
legend
```

A `Legend` object providing access to the properties of the legend
for this chart.

### `plots`

```python
plots()
```

The sequence of plots in this chart. A plot, called a *chart group*
in the Microsoft API, is a distinct sequence of one or more series
depicted in a particular charting type. For example, a chart having
a series plotted as a line overlaid on three series plotted as
columns would have two plots; the first corresponding to the three
column series and the second to the line series. Plots are sequenced
in the order drawn, i.e. back-most to front-most. Supports *len()*,
membership (e.g. ``p in plots``), iteration, slicing, and indexed
access (e.g. ``plot = plots[i]``).

### `replace_data`

```python
replace_data(chart_data)
```

Use the categories and series values in the `ChartData` object
*chart_data* to replace those in the XML and Excel worksheet for this
chart.

### `replace_data_safe`

```python
replace_data_safe(categories, series, *, number_format = None)
```

Validate `categories`/`series` fully, then route to `replace_data`.

paper-pptx addition: the safety-and-addressing wrapper over the existing replacement
mechanism. `categories` is a sequence of str; `series` is a sequence of
`(name, values)` pairs where each `values` is a sequence of numbers (or None for a
missing point) exactly as long as `categories`.

Data-shape problems raise `ValueError` (programmer error). Structural refusals
(`UnsupportedStructureError`, document untouched): a chart type outside the
supported category-chart families (XY/bubble/stock/surface/radar and 3-D variants
are not supported) or a multi-plot (combo) chart. Charts without an embedded
workbook (e.g. LibreOffice/Google-authored) are supported: their chart
XML is rewritten and the (absent) workbook update is skipped.

### `series`

```python
series()
```

A `SeriesCollection` object containing all the series in this
chart. When the chart has multiple plots, all the series for the
first plot appear before all those for the second, and so on. Series
within a plot have an explicit ordering and appear in that sequence.

### `value_axis`

```python
value_axis
```

The `ValueAxis` object providing access to properties of the value
axis of this chart. Raises `ValueError` if the chart has no value
axis.

## `ChartTitle`

```python
ChartTitle(title)
```

Bases: `ElementProxy`

Provides properties for manipulating a chart title.

### `format`

```python
format()
```

`ChartFormat` object providing access to line and fill formatting.

Return the `ChartFormat` object providing shape formatting properties
for this chart title, such as its line color and fill.

### `has_text_frame`

```python
has_text_frame
```

Read/write Boolean specifying whether this title has a text frame.

Return `True` if this chart title has a text frame, and `False`
otherwise. Assigning `True` causes a text frame to be added if not
already present. Assigning `False` causes any existing text frame to
be removed along with its text and formatting.

### `text_frame`

```python
text_frame
```

`TextFrame` instance for this chart title.

Return a `TextFrame` instance allowing read/write access to the text
of this chart title and its text formatting properties. Accessing this
property is destructive in the sense it adds a text frame if one is
not present. Use `has_text_frame` to test for the presence of
a text frame non-destructively.
