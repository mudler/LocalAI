<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.action`

Objects related to mouse click and hover actions on a shape or text.

## `ActionSetting`

```python
ActionSetting(xPr: CT_NonVisualDrawingProps | CT_TextCharacterProperties, parent: BaseShape, hover: bool = False)
```

Bases: `Subshape`

Properties specifying how a shape or run reacts to mouse actions.

### `action`

```python
action
```

Member of `PpActionType` enumeration, such as `PP_ACTION.HYPERLINK`.

The returned member indicates the type of action that will result when the
specified shape or text is clicked or the mouse pointer is positioned over the
shape during a slide show.

If there is no click-action or the click-action value is not recognized (is not
one of the official `MsoPpAction` values) then `PP_ACTION.NONE` is returned.

### `hyperlink`

```python
hyperlink() -> Hyperlink
```

A `Hyperlink` object representing the hyperlink action defined on
this click or hover mouse event. A `Hyperlink` object is always
returned, even if no hyperlink or other click action is defined.

### `target_slide`

```python
target_slide: Slide | None
```

A reference to the slide in this presentation that is the target of
the slide jump action in this shape. Slide jump actions include
`PP_ACTION.FIRST_SLIDE`, `LAST_SLIDE`, `NEXT_SLIDE`,
`PREVIOUS_SLIDE`, and `NAMED_SLIDE`. Returns `None` for all other
actions. In particular, the `LAST_SLIDE_VIEWED` action and the `PLAY`
(start other presentation) actions are not supported.

A slide object may be assigned to this property, which makes the
shape an "internal hyperlink" to the assigened slide::

    slide, target_slide = prs.slides[0], prs.slides[1]
    shape = slide.shapes[0]
    shape.target_slide = target_slide

Assigning `None` removes any slide jump action. Note that this is
accomplished by removing any action present (such as a hyperlink),
without first checking that it is a slide jump action.

## `Hyperlink`

```python
Hyperlink(xPr: CT_NonVisualDrawingProps | CT_TextCharacterProperties, parent: BaseShape, hover: bool = False)
```

Bases: `Subshape`

Represents a hyperlink action on a shape or text run.

### `address`

```python
address: str | None
```

Read/write. The URL of the hyperlink.

URL can be on http, https, mailto, or file scheme; others may work. Returns `None` if no
hyperlink is defined, including when another action such as `RUN_MACRO` is defined on the
object. Assigning `None` removes any action defined on the object, whether it is a hyperlink
action or not.
