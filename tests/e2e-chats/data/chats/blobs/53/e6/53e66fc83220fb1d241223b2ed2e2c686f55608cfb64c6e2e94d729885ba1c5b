<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.chart.datalabel`

Chart data-label related oxml objects.

## `CT_DLbl`

Bases: `BaseOxmlElement`

``<c:dLbl>`` element specifying the properties of the data label for an
individual data point.

### `dLblPos`

```python
dLblPos = ZeroOrOne('c:dLblPos', successors=_tag_seq[7:])
```

### `get_or_add_rich`

```python
get_or_add_rich()
```

Return the `c:rich` descendant representing the text frame of the
data label, newly created if not present. Any existing `c:strRef`
element is removed along with its contents.

### `get_or_add_tx_rich`

```python
get_or_add_tx_rich()
```

Return the `c:tx[c:rich]` subtree, newly created if not present.

### `idx`

```python
idx = OneAndOnlyOne('c:idx')
```

### `idx_val`

```python
idx_val
```

The integer value of the `val` attribute on the required `c:idx`
child.

### `new_dLbl`

```python
new_dLbl()
```

Return a newly created "loose" `c:dLbl` element.

The `c:dLbl` element contains the same (fairly extensive) default
subtree added by PowerPoint when an individual data label is
customized in the UI. Note that the idx value must be set by the
client. Failure to set the idx value will likely result in any
changes not being visible and may result in a repair error on open.

### `remove_tx_rich`

```python
remove_tx_rich()
```

Remove any `c:tx[c:rich]` child, or do nothing if not present.

### `spPr`

```python
spPr = ZeroOrOne('c:spPr', successors=_tag_seq[5:])
```

### `tx`

```python
tx = ZeroOrOne('c:tx', successors=_tag_seq[3:])
```

### `txPr`

```python
txPr = ZeroOrOne('c:txPr', successors=_tag_seq[6:])
```

## `CT_DLblPos`

Bases: `BaseOxmlElement`

``<c:dLblPos>`` element specifying the positioning of a data label with
respect to its data point.

### `val`

```python
val = RequiredAttribute('val', XL_DATA_LABEL_POSITION)
```

## `CT_DLbls`

Bases: `BaseOxmlElement`

`c:dLbls` element specifying properties for a set of data labels.

### `dLbl`

```python
dLbl = ZeroOrMore('c:dLbl', successors=_tag_seq[1:])
```

### `dLblPos`

```python
dLblPos = ZeroOrOne('c:dLblPos', successors=_tag_seq[5:])
```

### `defRPr`

```python
defRPr
```

``<a:defRPr>`` great-great-grandchild element, added with its
ancestors if not present.

### `get_dLbl_for_point`

```python
get_dLbl_for_point(idx)
```

Return the `c:dLbl` child representing the label for the data point
at index *idx*.

### `get_or_add_dLbl_for_point`

```python
get_or_add_dLbl_for_point(idx)
```

Return the `c:dLbl` element representing the label of the point at
index *idx*.

### `new_dLbls`

```python
new_dLbls()
```

Return a newly created "loose" `c:dLbls` element.

### `numFmt`

```python
numFmt = ZeroOrOne('c:numFmt', successors=_tag_seq[2:])
```

### `showCatName`

```python
showCatName = ZeroOrOne('c:showCatName', successors=_tag_seq[8:])
```

### `showLegendKey`

```python
showLegendKey = ZeroOrOne('c:showLegendKey', successors=_tag_seq[6:])
```

### `showPercent`

```python
showPercent = ZeroOrOne('c:showPercent', successors=_tag_seq[10:])
```

### `showSerName`

```python
showSerName = ZeroOrOne('c:showSerName', successors=_tag_seq[9:])
```

### `showVal`

```python
showVal = ZeroOrOne('c:showVal', successors=_tag_seq[7:])
```

### `txPr`

```python
txPr = ZeroOrOne('c:txPr', successors=_tag_seq[4:])
```
