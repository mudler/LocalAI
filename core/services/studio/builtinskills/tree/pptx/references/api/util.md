<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.util`

Utility functions and classes.

## `Centipoints`

Bases: `Length`

Convenience constructor for length in hundredths of a point.

## `Cm`

Bases: `Length`

Convenience constructor for length in centimeters.

## `Emu`

Bases: `Length`

Convenience constructor for length in english metric units.

## `Inches`

Bases: `Length`

Convenience constructor for length in inches.

## `Length`

Bases: `int`

Base class for length classes Inches, Emu, Cm, Mm, and Pt.

Provides properties for converting length values to convenient units.

### `centipoints`

```python
centipoints: int
```

Integer length in hundredths of a point (1/7200 inch).

Used internally because PowerPoint stores font size in centipoints.

### `cm`

```python
cm: float
```

Floating point length in centimeters.

### `emu`

```python
emu: int
```

Integer length in English Metric Units.

### `inches`

```python
inches: float
```

Floating point length in inches.

### `mm`

```python
mm: float
```

Floating point length in millimeters.

### `pt`

```python
pt: float
```

Floating point length in points.

## `Mm`

Bases: `Length`

Convenience constructor for length in millimeters.

## `Pt`

Bases: `Length`

Convenience value class for specifying a length in points.

## `lazyproperty`

```python
lazyproperty(fget: Callable[..., _T]) -> None
```

Bases: `Generic[_T]`

Decorator like @property, but evaluated only on first access.

Like @property, this can only be used to decorate methods having only a `self` parameter, and
is accessed like an attribute on an instance, i.e. trailing parentheses are not used. Unlike
@property, the decorated method is only evaluated on first access; the resulting value is
cached and that same value returned on second and later access without re-evaluation of the
method.

Like @property, this class produces a *data descriptor* object, which is stored in the __dict__
of the *class* under the name of the decorated method ('fget' nominally). The cached value is
stored in the __dict__ of the *instance* under that same name.

Because it is a data descriptor (as opposed to a *non-data descriptor*), its `__get__()` method
is executed on each access of the decorated attribute; the __dict__ item of the same name is
"shadowed" by the descriptor.

While this may represent a performance improvement over a property, its greater benefit may be
its other characteristics. One common use is to construct collaborator objects, removing that
"real work" from the constructor, while still only executing once. It also de-couples client
code from any sequencing considerations; if it's accessed from more than one location, it's
assured it will be ready whenever needed.

Loosely based on: https://stackoverflow.com/a/6849299/1902513.

A lazyproperty is read-only. There is no counterpart to the optional "setter" (or deleter)
behavior of an @property. This is critically important to maintaining its immutability and
idempotence guarantees. Attempting to assign to a lazyproperty raises AttributeError
unconditionally.

The parameter names in the methods below correspond to this usage example::

    class Obj(object)

        @lazyproperty
        def fget(self):
            return 'some result'

    obj = Obj()

Not suitable for wrapping a function (as opposed to a method) because it is not callable.

*fget* is the decorated method (a "getter" function).

A lazyproperty is read-only, so there is only an *fget* function (a regular
@property can also have an fset and fdel function). This name was chosen for
consistency with Python's `property` class which uses this name for the
corresponding parameter.
