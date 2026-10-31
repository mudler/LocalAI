<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.numbering`

Custom element classes related to the numbering part.

## `CT_Num`

Bases: `BaseOxmlElement`

``<w:num>`` element, which represents a concrete list definition instance, having
a required child <w:abstractNumId> that references an abstract numbering definition
that defines most of the formatting details.

### `abstractNumId`

```python
abstractNumId = OneAndOnlyOne('w:abstractNumId')
```

### `add_lvlOverride`

```python
add_lvlOverride(ilvl)
```

Return a newly added CT_NumLvl (<w:lvlOverride>) element having its ``ilvl``
attribute set to `ilvl`.

### `lvlOverride`

```python
lvlOverride = ZeroOrMore('w:lvlOverride')
```

### `new`

```python
new(num_id, abstractNum_id)
```

Return a new ``<w:num>`` element having numId of `num_id` and having a
``<w:abstractNumId>`` child with val attribute set to `abstractNum_id`.

### `numId`

```python
numId = RequiredAttribute('w:numId', ST_DecimalNumber)
```

## `CT_NumLvl`

Bases: `BaseOxmlElement`

``<w:lvlOverride>`` element, which identifies a level in a list definition to
override with settings it contains.

### `add_startOverride`

```python
add_startOverride(val)
```

Return a newly added CT_DecimalNumber element having tagname
``w:startOverride`` and ``val`` attribute set to `val`.

### `ilvl`

```python
ilvl = RequiredAttribute('w:ilvl', ST_DecimalNumber)
```

### `startOverride`

```python
startOverride = ZeroOrOne('w:startOverride', successors=('w:lvl',))
```

## `CT_NumPr`

Bases: `BaseOxmlElement`

A ``<w:numPr>`` element, a container for numbering properties applied to a
paragraph.

### `ilvl`

```python
ilvl = ZeroOrOne('w:ilvl', successors=('w:numId', 'w:numberingChange', 'w:ins'))
```

### `numId`

```python
numId = ZeroOrOne('w:numId', successors=('w:numberingChange', 'w:ins'))
```

## `CT_Numbering`

Bases: `BaseOxmlElement`

``<w:numbering>`` element, the root element of a numbering part, i.e.
numbering.xml.

### `add_num`

```python
add_num(abstractNum_id)
```

Return a newly added CT_Num (<w:num>) element referencing the abstract
numbering definition identified by `abstractNum_id`.

### `num`

```python
num = ZeroOrMore('w:num', successors=('w:numIdMacAtCleanup',))
```

### `num_having_numId`

```python
num_having_numId(numId)
```

Return the ``<w:num>`` child element having ``numId`` attribute matching
`numId`.
