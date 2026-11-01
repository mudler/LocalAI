<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.errors`

Typed safe-refusal exceptions for paper-pptx APIs.

## `AmbiguousTargetError`

Bases: `PaperRefusal`

The addressing given matches more than one target; refusing to pick one.

## `BoundaryViolationError`

Bases: `PaperRefusal`

An operation ran outside the scope where it is safe.

Today that means one thing: saving while a `batch()` block is open on the package. Those edits
are not validated yet and may still roll back, so save after the block closes.

## `PackageLimitError`

Bases: `PaperRefusal`

The package archive has no single unambiguous reading, or is unsafe to expand.

Raised reading a package (ambiguous ZIP end records, duplicate or overlapping members,
encryption, malformed `[Content_Types].xml`) and writing one (parts that would collide on a
single ZIP member name).

## `PaperRefusal`

Bases: `Exception`

Base class for all safe refusals raised by paper-pptx APIs.

## `RelationshipPolicyError`

Bases: `PaperRefusal`

The relationship graph cannot be carried across as asked.

Either the source carries relationship types this operation's ledger does not support, or the
graph itself is unusable: a malformed relationship collection, an invalid target mode, or an
internal relationship pointing outside its own package.

## `StaleAnchorError`

Bases: `TargetNotFoundError`

The block at an anchor's position no longer matches the anchor's content hash.

The document changed since the anchor was produced. Refusing beats guessing: use
`pptx.edit.refind()` to recover a fresh anchor explicitly. (Subclass of
`TargetNotFoundError` so existing handlers keep working.)

## `TargetNotFoundError`

Bases: `PaperRefusal`

The target could not be resolved in this document.

Either the addressing given (name, index, id, section) matches nothing, or a proxy handed in has
gone stale because the shape, part, or content it wrapped is no longer reachable. See
`StaleAnchorError` for the content-hash case.

## `UnsupportedStructureError`

Bases: `PaperRefusal`

This API cannot operate safely on the document as it stands.

Covers input it will not touch (unsupported structure, an unreadable package, a signed deck a
rewrite would invalidate) and, at commit or `batch()` exit, edits whose result would not reopen
as a presentation. In that second case the edits roll back.

## `materialize_slides`

```python
materialize_slides(prs, operation: str)
```

Return `list(prs.slides)`, refusing typed when the relationship graph is broken.

paper-pptx internal helper. Paper organs traverse the whole deck up front;
corrupt input (a `p:sldId` referencing a missing relationship) must speak from those
APIs as a typed, specific refusal - never a raw traceback. Upstream loader and
traversal behavior on such files is unchanged (the additive contract): only the
paper entry points route through this guard.
