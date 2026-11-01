<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.shared`

Objects shared by docx modules.

## `Cm`

Bases: `Length`

Convenience constructor for length in centimeters, e.g. ``height = Cm(12)``.

## `ElementProxy`

```python
ElementProxy(element: BaseOxmlElement, parent: t.ProvidesXmlPart | None = None)
```

Base class for lxml element proxy classes.

An element proxy class is one whose primary responsibilities are fulfilled by
manipulating the attributes and child elements of an XML element. They are the most
common type of class in python-docx other than custom element (oxml) classes.

### `element`

```python
element
```

The lxml element proxied by this object.

### `part`

```python
part: XmlPart
```

The package part containing this object.

## `Emu`

Bases: `Length`

Convenience constructor for length in English Metric Units, e.g. ``width =
Emu(457200)``.

## `Inches`

Bases: `Length`

Convenience constructor for length in inches, e.g. ``width = Inches(0.5)``.

## `Length`

Bases: `int`

Base class for length constructor classes Inches, Cm, Mm, Px, and Emu.

Behaves as an int count of English Metric Units, 914,400 to the inch, 36,000 to the
mm. Provides convenience unit conversion methods in the form of read-only
properties. Immutable.

### `cm`

```python
cm
```

The equivalent length expressed in centimeters (float).

### `emu`

```python
emu
```

The equivalent length expressed in English Metric Units (int).

### `inches`

```python
inches
```

The equivalent length expressed in inches (float).

### `mm`

```python
mm
```

The equivalent length expressed in millimeters (float).

### `pt`

```python
pt
```

Floating point length in points.

### `twips`

```python
twips
```

The equivalent length expressed in twips (int).

## `Mm`

Bases: `Length`

Convenience constructor for length in millimeters, e.g. ``width = Mm(240.5)``.

## `Parented`

```python
Parented(parent: t.ProvidesXmlPart)
```

Provides common services for document elements that occur below a part but may
occasionally require an ancestor object to provide a service, such as add or drop a
relationship.

Provides ``self._parent`` attribute to subclasses.

### `part`

```python
part: XmlPart
```

The package part containing this object.

## `Pt`

Bases: `Length`

Convenience value class for specifying a length in points.

## `RGBColor`

Bases: `Tuple[int, int, int]`

Immutable value object defining a particular RGB color.

### `from_string`

```python
from_string(rgb_hex_str: str) -> RGBColor
```

Return a new instance from an RGB color hex string like ``'3C2F80'``.

## `StoryChild`

```python
StoryChild(parent: t.ProvidesStoryPart)
```

A document element within a story part.

Story parts include DocumentPart and Header/FooterPart and can contain block items
(paragraphs and tables). Items from the block-item subtree occasionally require an
ancestor object to provide access to part-level or package-level items like styles
or images or to add or drop a relationship.

Provides `self._parent` attribute to subclasses.

### `part`

```python
part: StoryPart
```

The package part containing this object.

## `T`

```python
T = TypeVar('T')
```

## `TextAccumulator`

```python
TextAccumulator(separator: str = '')
```

Accepts `str` fragments and joins them together, in order, on `.pop().

Handy when text in a stream is broken up arbitrarily and you want to join it back
together within certain bounds. The optional `separator` argument determines how
the text fragments are punctuated, defaulting to the empty string.

### `pop`

```python
pop() -> Iterator[str]
```

Generate sero-or-one str from those accumulated.

Using `yield from accum.pop()` in a generator setting avoids producing an empty
string when no text is in the accumulator.

### `push`

```python
push(text: str) -> None
```

Add a text fragment to the accumulator.

## `Twips`

Bases: `Length`

Convenience constructor for length in twips, e.g. ``width = Twips(42)``.

A twip is a twentieth of a point, 635 EMU.

## `lazyproperty`

```python
lazyproperty(fget: Callable[..., T]) -> None
```

Bases: `Generic[T]`

Decorator like @property, but evaluated only on first access.

Like @property, this can only be used to decorate methods having only a `self`
parameter, and is accessed like an attribute on an instance, i.e. trailing
parentheses are not used. Unlike @property, the decorated method is only evaluated
on first access; the resulting value is cached and that same value returned on
second and later access without re-evaluation of the method.

Like @property, this class produces a *data descriptor* object, which is stored in
the __dict__ of the *class* under the name of the decorated method ('fget'
nominally). The cached value is stored in the __dict__ of the *instance* under that
same name.

Because it is a data descriptor (as opposed to a *non-data descriptor*), its
`__get__()` method is executed on each access of the decorated attribute; the
__dict__ item of the same name is "shadowed" by the descriptor.

While this may represent a performance improvement over a property, its greater
benefit may be its other characteristics. One common use is to construct
collaborator objects, removing that "real work" from the constructor, while still
only executing once. It also de-couples client code from any sequencing
considerations; if it's accessed from more than one location, it's assured it will
be ready whenever needed.

Loosely based on: https://stackoverflow.com/a/6849299/1902513.

A lazyproperty is read-only. There is no counterpart to the optional "setter" (or
deleter) behavior of an @property. This is critically important to maintaining its
immutability and idempotence guarantees. Attempting to assign to a lazyproperty
raises AttributeError unconditionally.

The parameter names in the methods below correspond to this usage example::

    class Obj(object)

        @lazyproperty
        def fget(self):
            return 'some result'

    obj = Obj()

Not suitable for wrapping a function (as opposed to a method) because it is not
callable.

*fget* is the decorated method (a "getter" function).

A lazyproperty is read-only, so there is only an *fget* function (a regular
@property can also have an fset and fdel function). This name was chosen for
consistency with Python's `property` class which uses this name for the
corresponding parameter.

## `write_only_property`

```python
write_only_property(f: Callable[[Any, Any], None])
```

@write_only_property decorator.

Creates a property (descriptor attribute) that accepts assignment, but not getattr
(use in an expression).
