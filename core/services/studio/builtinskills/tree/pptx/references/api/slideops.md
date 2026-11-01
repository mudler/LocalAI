<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.slideops`

Internal machinery for slide clone/delete/reorder (paper-pptx). Not public API.

## `clone_slide_part`

```python
clone_slide_part(source_part: 'SlidePart', policy: 'SlidePart') -> 'SlidePart'
```

Return a new `SlidePart` that is a policy-governed deep copy of `source_part`.

The new part is fully related (layout, media, charts+workbooks, notes per `policy`) but
NOT yet added to the presentation's slide list — the caller owns `p:sldIdLst`.

## `enroll_clone_in_section`

```python
enroll_clone_in_section(presentation_elm, source_slide_id: int, clone_slide_id: int) -> None
```

Add `clone_slide_id` to the section holding `source_slide_id`, directly after it.

No-op when the deck has no sections or the source slide is not enrolled in one. Custom
shows are deliberately NOT extended: a copy is not part of a curated show.

## `remove_slide_from_id_lists`

```python
remove_slide_from_id_lists(presentation_elm, slide_id: int, rId: str) -> None
```

Purge `slide_id`/`rId` bookkeeping for a deleted slide from auxiliary ID lists.

Sections (`p14:sectionLst`, in the presentation's extension list) reference slides by
slide id; custom shows (`p:custShowLst`) reference them by relationship id. Neither is
reachable through the relationship graph, so without this step a delete leaves dangling
entries behind — the corruption class this step closes. Empty sections and
empty custom-show slide lists are schema-valid and left in place (matching PowerPoint,
which keeps an emptied section).
