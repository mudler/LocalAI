<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.opc.part`

Open Packaging Convention (OPC) objects related to package parts.

## `Part`

```python
Part(partname: PackURI, content_type: str, blob: bytes | None = None, package: Package | None = None)
```

Base class for package parts.

Provides common properties and methods, but intended to be subclassed in client code
to implement specific part behaviors.

### `after_unmarshal`

```python
after_unmarshal()
```

Entry point for post-unmarshaling processing, for example to parse the part
XML.

May be overridden by subclasses without forwarding call to super.

### `before_marshal`

```python
before_marshal()
```

Entry point for pre-serialization processing, for example to finalize part
naming if necessary.

May be overridden by subclasses without forwarding call to super.

### `blob`

```python
blob: bytes
```

Contents of this package part as a sequence of bytes.

May be text or binary. Intended to be overridden by subclasses. Default behavior
is to return load blob.

### `content_type`

```python
content_type
```

Content type of this part.

### `drop_rel`

```python
drop_rel(rId: str)
```

Remove the relationship identified by `rId` if its reference count is less
than 2.

Relationships with a reference count of 0 are implicit relationships.

### `load`

```python
load(partname: PackURI, content_type: str, blob: bytes, package: Package)
```

### `load_rel`

```python
load_rel(reltype: str, target: Part | str, rId: str, is_external: bool = False)
```

Return newly added `_Relationship` instance of `reltype`.

The new relationship relates the `target` part to this part with key `rId`.

Target mode is set to ``RTM.EXTERNAL`` if `is_external` is `True`. Intended for
use during load from a serialized package, where the rId is well-known. Other
methods exist for adding a new relationship to a part when manipulating a part.

### `package`

```python
package
```

`OpcPackage` instance this part belongs to.

### `part_related_by`

```python
part_related_by(reltype: str) -> Part
```

Return part to which this part has a relationship of `reltype`.

Raises `KeyError` if no such relationship is found and `ValueError` if more than
one such relationship is found. Provides ability to resolve implicitly related
part, such as Slide -> SlideLayout.

### `partname`

```python
partname
```

`PackURI` instance holding partname of this part, e.g.
'/ppt/slides/slide1.xml'.

### `relate_to`

```python
relate_to(target: Part | str, reltype: str, is_external: bool = False) -> str
```

Return rId key of relationship of `reltype` to `target`.

The returned `rId` is from an existing relationship if there is one, otherwise a
new relationship is created.

### `related_parts`

```python
related_parts
```

Dictionary mapping related parts by rId, so child objects can resolve
explicit relationships present in the part XML, e.g. sldIdLst to a specific
`Slide` instance.

### `rels`

```python
rels()
```

`Relationships` instance holding the relationships for this part.

### `target_ref`

```python
target_ref(rId: str) -> str
```

Return URL contained in target ref of relationship identified by `rId`.

## `PartFactory`

Provides a way for client code to specify a subclass of `Part` to be constructed
by `Unmarshaller` based on its content type and/or a custom callable.

Setting ``PartFactory.part_class_selector`` to a callable object will cause that
object to be called with the parameters ``content_type, reltype``, once for each
part in the package. If the callable returns an object, it is used as the class for
that part. If it returns `None`, part class selection falls back to the content type
map defined in ``PartFactory.part_type_for``. If no class is returned from either of
these, the class contained in ``PartFactory.default_part_type`` is used to construct
the part, which is by default ``opc.package.Part``.

### `default_part_type`

```python
default_part_type = Part
```

### `part_class_selector`

```python
part_class_selector: Callable[[str, str], Type[Part] | None] | None
```

### `part_type_for`

```python
part_type_for: dict[str, Type[Part]] = {}
```

## `XmlPart`

```python
XmlPart(partname: PackURI, content_type: str, element: BaseOxmlElement, package: Package)
```

Bases: `Part`

Base class for package parts containing an XML payload, which is most of them.

Provides additional methods to the `Part` base class that take care of parsing and
reserializing the XML payload and managing relationships to other parts.

### `blob`

```python
blob
```

### `element`

```python
element
```

The root XML element of this XML part.

### `load`

```python
load(partname: PackURI, content_type: str, blob: bytes, package: Package)
```

### `part`

```python
part
```

Part of the parent protocol, "children" of the document will not know the
part that contains them so must ask their parent object.

That chain of delegation ends here for child objects.
