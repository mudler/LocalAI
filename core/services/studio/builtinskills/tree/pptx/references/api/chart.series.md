<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.chart.series`

Series-related objects.

## `AreaSeries`

Bases: `_BaseCategorySeries`

A data point series belonging to an area plot.

## `BarSeries`

Bases: `_BaseCategorySeries`

A data point series belonging to a bar plot.

### `invert_if_negative`

```python
invert_if_negative
```

`True` if a point having a value less than zero should appear with a
fill different than those with a positive value. `False` if the fill
should be the same regardless of the bar's value. When `True`, a bar
with a solid fill appears with white fill; in a bar with gradient
fill, the direction of the gradient is reversed, e.g. dark -> light
instead of light -> dark. The term "invert" here should be understood
to mean "invert the *direction* of the *fill gradient*".

## `BubbleSeries`

Bases: `XySeries`

A data point series belonging to a bubble plot.

### `points`

```python
points()
```

The `BubblePoints` object providing access to individual data point
objects used to discover and adjust the formatting and data labels of
a data point.

## `LineSeries`

Bases: `_BaseCategorySeries`, `_MarkerMixin`

A data point series belonging to a line plot.

### `smooth`

```python
smooth
```

Read/write boolean specifying whether to use curve smoothing to
form the line connecting the data points in this series into
a continuous curve. If `False`, a series of straight line segments
are used to connect the points.

## `PieSeries`

Bases: `_BaseCategorySeries`

A data point series belonging to a pie plot.

## `RadarSeries`

Bases: `_BaseCategorySeries`, `_MarkerMixin`

A data point series belonging to a radar plot.

## `SeriesCollection`

```python
SeriesCollection(parent_elm)
```

Bases: `Sequence`

A sequence of `Series` objects.

## `XySeries`

Bases: `_BaseSeries`, `_MarkerMixin`

A data point series belonging to an XY (scatter) plot.

### `iter_values`

```python
iter_values()
```

Generate each float Y value in this series, in the order they appear
on the chart. A value of `None` represents a missing Y value
(corresponding to a blank Excel cell).

### `points`

```python
points()
```

The `XyPoints` object providing access to individual data points in
this series.

### `values`

```python
values
```

Read-only. A sequence containing the float values for this series, in
the order they appear on the chart.
