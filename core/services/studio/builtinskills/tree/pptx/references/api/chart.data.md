<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.chart.data`

ChartData and related objects.

## `BubbleChartData`

Bases: `XyChartData`

A specialized ChartData object suitable for use with a bubble chart.
A bubble chart is essentially an XY chart where the markers are scaled to
provide a third quantitative dimension to the exhibit.

### `add_series`

```python
add_series(name, number_format = None)
```

Return a `BubbleSeriesData` object newly created and added at the end
of this sequence, and having series named *name* and values formatted
with *number_format*.

### `bubble_sizes_ref`

```python
bubble_sizes_ref(series)
```

The Excel worksheet reference for the range containing the bubble
sizes for *series*.

## `BubbleDataPoint`

```python
BubbleDataPoint(series_data, x, y, size, number_format)
```

Bases: `XyDataPoint`

A data point in a bubble chart series. Provides access to the x, y, and
size values of the datapoint.

### `bubble_size`

```python
bubble_size
```

The value representing the size of the bubble for this data point.

## `BubbleSeriesData`

Bases: `XySeriesData`

The data specific to a particular Bubble chart series. It provides access
to the series label, the series data points, and an optional number
format to be applied to each data point not having a specified number
format.

The sequence of data points in a bubble chart series is maintained
throughout the chart building process because a data point has no unique
identifier and can only be retrieved by index.

### `add_data_point`

```python
add_data_point(x, y, size, number_format = None)
```

Append a new BubbleDataPoint object having the values *x*, *y*, and
*size*. The optional *number_format* is used to format the Y value.
If not provided, the number format is inherited from the series data.

### `bubble_sizes`

```python
bubble_sizes
```

A sequence containing the bubble size for each datapoint in this
series, in data point order.

### `bubble_sizes_ref`

```python
bubble_sizes_ref
```

The Excel worksheet reference for the range containing the bubble
sizes for this series.

## `Categories`

```python
Categories()
```

Bases: `Sequence`

A sequence of `data.Category` objects, also having certain hierarchical
graph behaviors for support of multi-level (nested) categories.

### `add_category`

```python
add_category(label)
```

Return a newly created `data.Category` object having *label* and
appended to the end of this category sequence. *label* can be
a string, a number, a datetime.date, or datetime.datetime object. All
category labels in a chart must be the same type. All category labels
in a chart having multi-level categories must be strings.

Creating a chart from chart data having date categories will cause
the chart to have a `DateAxis` for its category axis.

### `are_dates`

```python
are_dates
```

Return `True` if the first category in this collection has a date
label (as opposed to str or numeric). A date label is one of type
datetime.date or datetime.datetime. Returns `False` otherwise,
including when this category collection is empty. It also returns
False when this category collection is hierarchical, because
hierarchical categories can only be written as string labels.

### `are_numeric`

```python
are_numeric
```

Return `True` if the first category in this collection has a numeric
label (as opposed to a string label), including if that value is
a datetime.date or datetime.datetime object (as those are converted
to integers for storage in Excel). Returns `False` otherwise,
including when this category collection is empty. It also returns
False when this category collection is hierarchical, because
hierarchical categories can only be written as string labels.

### `depth`

```python
depth
```

The number of hierarchy levels in this category graph. Returns 0 if
it contains no categories.

### `index`

```python
index(category)
```

The offset of *category* in the overall sequence of leaf categories.
A non-leaf category gets the index of its first sub-category.

### `leaf_count`

```python
leaf_count
```

The number of leaf-level categories in this hierarchy. The return
value is the same as that of `len()` only when the hierarchy is
single level.

### `levels`

```python
levels
```

A generator of (idx, label) sequences representing the category
hierarchy from the bottom up. The first level contains all leaf
categories, and each subsequent is the next level up.

### `number_format`

```python
number_format
```

Read/write. Return a string representing the number format used in
Excel to format these category values, e.g. '0.0' or 'mm/dd/yyyy'.
This string is only relevant when the categories are numeric or date
type, although it returns 'General' without error when the categories
are string labels. Assigning `None` causes the default number format
to be used, based on the type of the category labels.

## `Category`

```python
Category(label, parent)
```

Bases: `object`

A chart category, primarily having a label to be displayed on the
category axis, but also able to be configured in a hierarchy for support
of multi-level category charts.

### `add_sub_category`

```python
add_sub_category(label)
```

Return a newly created `data.Category` object having *label* and
appended to the end of the sub-category sequence for this category.

### `depth`

```python
depth
```

The number of hierarchy levels rooted at this category node. Returns
1 if this category has no sub-categories.

### `idx`

```python
idx
```

The offset of this category in the overall sequence of leaf
categories. A non-leaf category gets the index of its first
sub-category.

### `index`

```python
index(sub_category)
```

The offset of *sub_category* in the overall sequence of leaf
categories.

### `label`

```python
label
```

The value that appears on the axis for this category. The label can
be a string, a number, or a datetime.date or datetime.datetime
object.

### `leaf_count`

```python
leaf_count
```

The number of leaf category nodes under this category. Returns
1 if this category has no sub-categories.

### `numeric_str_val`

```python
numeric_str_val(date_1904 = False)
```

The string representation of the numeric (or date) label of this
category, suitable for use in the XML `c:pt` element for this
category. The optional *date_1904* parameter specifies the epoch used
for calculating Excel date numbers.

### `sub_categories`

```python
sub_categories
```

The sequence of child categories for this category.

## `CategoryChartData`

Bases: `_BaseChartData`

Accumulates data specifying the categories and series values for a chart
and acts as a proxy for the chart data table that will be written to an
Excel worksheet. Used as a parameter in `shapes.add_chart` and
`Chart.replace_data`.

This object is suitable for use with category charts, i.e. all those
having a discrete set of label values (categories) as the range of their
independent variable (X-axis) values. Unlike the ChartData types for
charts supporting a continuous range of independent variable values (such
as XyChartData), CategoryChartData has a single collection of category
(X) values and each data point in its series specifies only the Y value.
The corresponding X value is inferred by its position in the sequence.

### `add_category`

```python
add_category(label)
```

Return a newly created `data.Category` object having *label* and
appended to the end of the category collection for this chart.
*label* can be a string, a number, a datetime.date, or
datetime.datetime object. All category labels in a chart must be the
same type. All category labels in a chart having multi-level
categories must be strings.

### `add_series`

```python
add_series(name, values = (), number_format = None)
```

Add a series to this data set entitled *name* and having the data
points specified by *values*, an iterable of numeric values.
*number_format* specifies how the series values will be displayed,
and may be a string, e.g. '#,##0' corresponding to an Excel number
format.

### `categories`

```python
categories
```

`data.Categories` object providing access to category-object hierarchy.

Assigning an iterable of category labels (strings, numbers, or dates) replaces
the `data.Categories` object with a new one containing a category for each label
in the sequence.

Creating a chart from chart data having date categories will cause the chart to
have a `DateAxis` for its category axis.

### `categories_ref`

```python
categories_ref
```

The Excel worksheet reference to the categories for this chart (not
including the column heading).

### `values_ref`

```python
values_ref(series)
```

The Excel worksheet reference to the values for *series* (not
including the column heading).

## `CategoryDataPoint`

```python
CategoryDataPoint(series_data, value, number_format)
```

Bases: `_BaseDataPoint`

A data point in a category chart series. Provides access to the value of
the datapoint and the number format with which it should appear in the
Excel file.

### `value`

```python
value
```

The (Y) value for this category data point.

## `CategorySeriesData`

Bases: `_BaseSeriesData`

The data specific to a particular category chart series. It provides
access to the series label, the series data points, and an optional
number format to be applied to each data point not having a specified
number format.

### `add_data_point`

```python
add_data_point(value, number_format = None)
```

Return a CategoryDataPoint object newly created with value *value*,
an optional *number_format*, and appended to this sequence.

### `categories`

```python
categories
```

The `data.Categories` object that provides access to the category
objects for this series.

### `categories_ref`

```python
categories_ref
```

The Excel worksheet reference to the categories for this chart (not
including the column heading).

### `values`

```python
values
```

A sequence containing the (Y) value of each datapoint in this series,
in data point order.

### `values_ref`

```python
values_ref
```

The Excel worksheet reference to the (Y) values for this series (not
including the column heading).

## `ChartData`

Bases: `CategoryChartData`

`ChartData` is simply an alias for `CategoryChartData` and may be removed
in a future release. All new development should use `CategoryChartData`
for creating or replacing the data in chart types other than XY and
Bubble.

## `XyChartData`

Bases: `_BaseChartData`

A specialized ChartData object suitable for use with an XY (aka. scatter)
chart. Unlike ChartData, it has no category sequence. Rather, each data
point of each series specifies both an X and a Y value.

### `add_series`

```python
add_series(name, number_format = None)
```

Return an `XySeriesData` object newly created and added at the end of
this sequence, identified by *name* and values formatted with
*number_format*.

## `XyDataPoint`

```python
XyDataPoint(series_data, x, y, number_format)
```

Bases: `_BaseDataPoint`

A data point in an XY chart series. Provides access to the x and y values
of the datapoint.

### `x`

```python
x
```

The X value for this XY data point.

### `y`

```python
y
```

The Y value for this XY data point.

## `XySeriesData`

Bases: `_BaseSeriesData`

The data specific to a particular XY chart series. It provides access to
the series label, the series data points, and an optional number format
to be applied to each data point not having a specified number format.

The sequence of data points in an XY series is significant; lines are
plotted following the sequence of points, even if that causes a line
segment to "travel backward" (implying a multi-valued function). The data
points are not automatically sorted into increasing order by X value.

### `add_data_point`

```python
add_data_point(x, y, number_format = None)
```

Return an XyDataPoint object newly created with values *x* and *y*,
and appended to this sequence.
