<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.shared`

Objects shared by pptx modules.

## `ElementProxy`

```python
ElementProxy(element: BaseOxmlElement)
```

Bases: `object`

Base class for lxml element proxy classes.

An element proxy class is one whose primary responsibilities are fulfilled by manipulating the
attributes and child elements of an XML element. They are the most common type of class in
python-pptx other than custom element (oxml) classes.

### `element`

```python
element
```

The lxml element proxied by this object.

## `ParentedElementProxy`

```python
ParentedElementProxy(element: BaseOxmlElement, parent: ProvidesPart)
```

Bases: `ElementProxy`

Provides access to ancestor objects and part.

An ancestor may occasionally be required to provide a service, such as add or drop a
relationship. Provides the `_parent` attribute to subclasses and the public
`parent` read-only property.

### `parent`

```python
parent
```

The ancestor proxy object to this one.

For example, the parent of a shape is generally the `SlideShapes` object that contains it.

### `part`

```python
part: XmlPart
```

The package part containing this object.

## `PartElementProxy`

```python
PartElementProxy(element: BaseOxmlElement, part: XmlPart)
```

Bases: `ElementProxy`

Provides common members for proxy-objects that wrap a part's root element, e.g. `p:sld`.

### `part`

```python
part: XmlPart
```

The package part containing this object.
