<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.chart.marker`

Marker-related objects.

## `Marker`

Bases: `ElementProxy`

Represents a data point marker, such as a diamond or circle, on
a line-type chart.

### `format`

```python
format()
```

The `ChartFormat` instance for this marker, providing access to shape
properties such as fill and line.

### `size`

```python
size
```

An integer between 2 and 72 inclusive indicating the size of this
marker in points. A value of `None` indicates no explicit value is
set and the size is inherited from a higher-level setting or the
PowerPoint default (which may be 9). Assigning `None` removes any
explicitly assigned size, causing this value to be inherited.

### `style`

```python
style
```

A member of the `XlMarkerStyle` enumeration indicating the shape
of this marker. Returns `None` if no explicit style has been set,
which corresponds to the "Automatic" option in the PowerPoint UI.
