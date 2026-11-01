<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.chart.point`

Data point-related objects.

## `BubblePoints`

Bases: `_BasePoints`

Sequence providing access to the individual data points in
a `BubbleSeries` object.

## `CategoryPoints`

Bases: `_BasePoints`

Sequence providing access to individual `Point` objects, each
representing the visual properties of a data point in the specified
category series.

## `Point`

```python
Point(ser, idx)
```

Bases: `object`

Provides access to the properties of an individual data point in
a series, such as the visual properties of its marker and the text and
font of its data label.

### `data_label`

```python
data_label()
```

The `DataLabel` object representing the label on this data point.

### `format`

```python
format()
```

The `ChartFormat` object providing access to the shape formatting
properties of this data point, such as line and fill.

### `marker`

```python
marker()
```

The `Marker` instance for this point, providing access to the visual
properties of the data point marker, such as fill and line. Setting
these properties overrides any value set at the series level.

## `XyPoints`

Bases: `_BasePoints`

Sequence providing access to the individual data points in an `XySeries`
object.
