<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.chart.marker`

Series-related oxml objects.

## `CT_Marker`

Bases: `BaseOxmlElement`

`c:marker` custom element class, containing visual properties for a data
point marker on line-type charts.

### `size`

```python
size = ZeroOrOne('c:size', successors=_tag_seq[2:])
```

### `size_val`

```python
size_val
```

Return the value of `./c:size/@val`, specifying the size of this
marker in points. Returns `None` if no `c:size` element is present or
its val attribute is not present.

### `spPr`

```python
spPr = ZeroOrOne('c:spPr', successors=_tag_seq[3:])
```

### `symbol`

```python
symbol = ZeroOrOne('c:symbol', successors=_tag_seq[1:])
```

### `symbol_val`

```python
symbol_val
```

Return the value of `./c:symbol/@val`, specifying the shape of this
marker. Returns `None` if no `c:symbol` element is present.

## `CT_MarkerSize`

Bases: `BaseOxmlElement`

`c:size` custom element class, specifying the size (in points) of a data
point marker for a line, XY, or radar chart.

### `val`

```python
val = RequiredAttribute('val', ST_MarkerSize)
```

## `CT_MarkerStyle`

Bases: `BaseOxmlElement`

`c:symbol` custom element class, specifying the shape of a data point
marker for a line, XY, or radar chart.

### `val`

```python
val = RequiredAttribute('val', XL_MARKER_STYLE)
```
