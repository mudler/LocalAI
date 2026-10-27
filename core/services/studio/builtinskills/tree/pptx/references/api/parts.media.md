<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.parts.media`

MediaPart and related objects.

## `MediaPart`

Bases: `Part`

A media part, containing an audio or video resource.

A media part generally has a partname matching the regex
`ppt/media/media[1-9][0-9]*.*`.

### `new`

```python
new(package, media)
```

Return new `MediaPart` instance containing `media`.

`media` must be a `Media` object.

### `sha1`

```python
sha1()
```

The SHA1 hash digest for the media binary of this media part.

Example: `'1be010ea47803b00e140b852765cdf84f491da47'`
