<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.chart.category`

Category-related objects.

## `Categories`

```python
Categories(xChart)
```

Bases: `Sequence`

A sequence of `category.Category` objects, each representing a category
label on the chart. Provides properties for dealing with hierarchical
categories.

### `depth`

```python
depth
```

Return an integer representing the number of hierarchical levels in
this category collection. Returns 1 for non-hierarchical categories
and 0 if no categories are present (generally meaning no series are
present).

### `flattened_labels`

```python
flattened_labels
```

Return a sequence of tuples, each containing the flattened hierarchy
of category labels for a leaf category. Each tuple is in parent ->
child order, e.g. ``('US', 'CA', 'San Francisco')``, with the leaf
category appearing last. If this categories collection is
non-hierarchical, each tuple will contain only a leaf category label.
If the plot has no series (and therefore no categories), an empty
tuple is returned.

### `levels`

```python
levels
```

Return a sequence of `CategoryLevel` objects representing the
hierarchy of this category collection. The sequence is empty when the
category collection is not hierarchical, that is, contains only
leaf-level categories. The levels are ordered from the leaf level to
the root level; so the first level will contain the same categories
as this category collection.

## `Category`

```python
Category(pt, idx = None)
```

Bases: `str`

An extension of `str` that provides the category label as its string
value, and additional attributes representing other aspects of the
category.

*idx* is a required attribute of a c:pt element, but must be
specified when pt is None, as when a "placeholder" category is
created to represent a missing c:pt element.

### `idx`

```python
idx
```

Return an integer representing the index reference of this category.
For a leaf node, the index identifies the category. For a parent (or
other ancestor) category, the index specifies the first leaf category
that ancestor encloses.

### `label`

```python
label
```

Return the label of this category as a string.

## `CategoryLevel`

```python
CategoryLevel(lvl)
```

Bases: `Sequence`

A sequence of `category.Category` objects representing a single level in
a hierarchical category collection. This object is only used when the
categories are hierarchical, meaning they have more than one level and
higher level categories group those at lower levels.
