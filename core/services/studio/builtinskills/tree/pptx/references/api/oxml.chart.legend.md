<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.chart.legend`

lxml custom element classes for legend-related XML elements.

## `CT_Legend`

Bases: `BaseOxmlElement`

``<c:legend>`` custom element class

### `defRPr`

```python
defRPr
```

`./c:txPr/a:p/a:pPr/a:defRPr` great-great-grandchild element, added
with its ancestors if not present.

### `horz_offset`

```python
horz_offset
```

The float value in ./c:layout/c:manualLayout/c:x when
./c:layout/c:manualLayout/c:xMode@val == "factor". 0.0 if that
XPath expression has no match.

### `layout`

```python
layout = ZeroOrOne('c:layout', successors=_tag_seq[3:])
```

### `legendPos`

```python
legendPos = ZeroOrOne('c:legendPos', successors=_tag_seq[1:])
```

### `overlay`

```python
overlay = ZeroOrOne('c:overlay', successors=_tag_seq[4:])
```

### `txPr`

```python
txPr = ZeroOrOne('c:txPr', successors=_tag_seq[6:])
```

## `CT_LegendPos`

Bases: `BaseOxmlElement`

``<c:legendPos>`` element specifying position of legend with respect to
chart as a member of ST_LegendPos.

### `val`

```python
val = OptionalAttribute('val', XL_LEGEND_POSITION, default=XL_LEGEND_POSITION.RIGHT)
```
