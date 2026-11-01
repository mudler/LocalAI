<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.shapes.connector`

lxml custom element classes for XML elements related to the Connector shape.

## `CT_Connection`

Bases: `BaseShapeElement`

A `a:stCxn` or `a:endCxn` element.

Specifies a connection between an end-point of a connector and a shape connection point.

### `id`

```python
id = RequiredAttribute('id', ST_DrawingElementId)
```

### `idx`

```python
idx = RequiredAttribute('idx', XsdUnsignedInt)
```

## `CT_Connector`

Bases: `BaseShapeElement`

A line/connector shape `p:cxnSp` element

### `new_cxnSp`

```python
new_cxnSp(id_: int, name: str, prst: str, x: int, y: int, cx: int, cy: int, flipH: bool, flipV: bool) -> CT_Connector
```

Return a new `p:cxnSp` element tree configured as a base connector.

### `nvCxnSpPr`

```python
nvCxnSpPr = OneAndOnlyOne('p:nvCxnSpPr')
```

### `spPr`

```python
spPr: CT_ShapeProperties = OneAndOnlyOne('p:spPr')
```

## `CT_ConnectorNonVisual`

Bases: `BaseOxmlElement`

`p:nvCxnSpPr` element, container for the non-visual properties of
a connector, such as name, id, etc.

### `cNvCxnSpPr`

```python
cNvCxnSpPr = OneAndOnlyOne('p:cNvCxnSpPr')
```

### `cNvPr`

```python
cNvPr = OneAndOnlyOne('p:cNvPr')
```

### `nvPr`

```python
nvPr = OneAndOnlyOne('p:nvPr')
```

## `CT_NonVisualConnectorProperties`

Bases: `BaseOxmlElement`

`p:cNvCxnSpPr` element, container for the non-visual properties specific
to a connector shape, such as connections and connector locking.

### `endCxn`

```python
endCxn = ZeroOrOne('a:endCxn', successors=_tag_seq[3:])
```

### `stCxn`

```python
stCxn = ZeroOrOne('a:stCxn', successors=_tag_seq[2:])
```
