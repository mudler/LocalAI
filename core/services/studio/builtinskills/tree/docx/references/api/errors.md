<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.errors`

Typed refusals for paper-docx safe-editing APIs.

## `AmbiguousTargetError`

Bases: `PaperRefusal`

The target specification matches more than one location.

Supply a more specific exact target: for example a live span or block, a
narrower story, or an explicit `nth` when supported. To inspect contextual
ranking without hiding candidates, use `find_text(..., near=...)`.

## `BoundaryViolationError`

Bases: `PaperRefusal`

The operation would cross a structural boundary it must respect.

Examples: a character-level replacement spanning a paragraph boundary or
entering/leaving a content control; a block operation selecting
paragraphs that do not share one parent.

## `DocumentProtectedError`

Bases: `PaperRefusal`

The document enforces an editing restriction this operation ignores.

Word honors `w:documentProtection` (read-only, forms-only, comments-only,
tracked-changes-enforced); silently editing a locked template reports
false state. Protection is ADVISORY, not security — after reviewing why
the document is locked, call
`docx.protection.acknowledge_protection(document)` to proceed. paper-docx
never strips the protection setting itself.

## `MalformedPackageError`

Bases: `PaperRefusal`

The file is not a package this library can safely read.

Raised for a damaged or inconsistent ZIP envelope, an encrypted or
unsupported member, a member name that cannot denote one part, and a
`[Content_Types].xml` or relationship graph that does not describe a
readable package. Validation runs before any XML is parsed or any output is
replaced, so this is a safe refusal: nothing was read into the object model
and nothing on disk was touched.

It does not mean the file is too large. Size and member-count ceilings were
removed once Word was measured opening documents past all of them.

## `PaperRefusal`

Bases: `Exception`

Base for every safe refusal raised by paper-docx APIs.

A refused operation mutated nothing, in memory or on disk. The message
states what was found and why it was unsafe to proceed.

## `RelationshipPolicyError`

Bases: `PaperRefusal`

The operation would create or modify a package relationship unsafely.

## `TargetNotFoundError`

Bases: `PaperRefusal`

No location matches the target specification.

Also raised when a previously-valid anchor or span has gone stale — the
underlying content changed since it was captured.

## `UnsupportedStructureError`

Bases: `PaperRefusal`

The target involves structure this operation does not safely support.

Examples: text inside a tracked deletion or field instruction; a table
with merged or nested cells; numbering that would require authoring new
definitions.

## `check_install`

```python
check_install() -> None
```

Refuse when paper-docx and python-docx are both installed.
