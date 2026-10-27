<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.text.tabstops`

Tabstop-related proxy types.

## `TabStop`

```python
TabStop(element)
```

Bases: `ElementProxy`

An individual tab stop applying to a paragraph or style.

Accessed using list semantics on its containing `TabStops` object.

### `alignment`

```python
alignment
```

A member of `WdTabAlignment` specifying the alignment setting for this
tab stop.

Read/write.

### `leader`

```python
leader
```

A member of `WdTabLeader` specifying a repeating character used as a
"leader", filling in the space spanned by this tab.

Assigning `None` produces the same result as assigning `WD_TAB_LEADER.SPACES`.
Read/write.

### `position`

```python
position
```

A `Length` object representing the distance of this tab stop from the inside
edge of the paragraph.

May be positive or negative. Read/write.

## `TabStops`

```python
TabStops(element)
```

Bases: `ElementProxy`

A sequence of `TabStop` objects providing access to the tab stops of a paragraph
or paragraph style.

Supports iteration, indexed access, del, and len(). It is accesed using the
`tab_stops` property of ParagraphFormat; it is not intended
to be constructed directly.

### `add_tab_stop`

```python
add_tab_stop(position, alignment = WD_TAB_ALIGNMENT.LEFT, leader = WD_TAB_LEADER.SPACES)
```

Add a new tab stop at `position`, a `Length` object specifying the location
of the tab stop relative to the paragraph edge.

A negative `position` value is valid and appears in hanging indentation. Tab
alignment defaults to left, but may be specified by passing a member of the
`WdTabAlignment` enumeration as `alignment`. An optional leader character
can be specified by passing a member of the `WdTabLeader` enumeration as
`leader`.

### `clear_all`

```python
clear_all()
```

Remove all custom tab stops.
