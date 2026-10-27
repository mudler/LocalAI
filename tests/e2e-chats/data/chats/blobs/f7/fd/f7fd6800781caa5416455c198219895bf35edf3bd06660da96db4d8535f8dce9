<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.parts.image`

The proxy class for an image part, and related objects.

## `ImagePart`

```python
ImagePart(partname: PackURI, content_type: str, blob: bytes, image: Image | None = None)
```

Bases: `Part`

An image part.

Corresponds to the target part of a relationship with type RELATIONSHIP_TYPE.IMAGE.

### `default_cx`

```python
default_cx
```

Native width of this image, calculated from its width in pixels and
horizontal dots per inch (dpi).

### `default_cy`

```python
default_cy
```

Native height of this image, calculated from its height in pixels and
vertical dots per inch (dpi).

### `filename`

```python
filename
```

Filename from which this image part was originally created.

A generic name, e.g. 'image.png', is substituted if no name is available, for
example when the image was loaded from an unnamed stream. In that case a default
extension is applied based on the detected MIME type of the image.

### `from_image`

```python
from_image(image: Image, partname: PackURI)
```

Return an `ImagePart` instance newly created from `image` and assigned
`partname`.

### `image`

```python
image: Image
```

### `load`

```python
load(partname: PackURI, content_type: str, blob: bytes, package: OpcPackage)
```

Called by ``docx.opc.package.PartFactory`` to load an image part from a
package being opened by ``Document(...)`` call.

### `sha1`

```python
sha1
```

SHA1 hash digest of the blob of this image part.
