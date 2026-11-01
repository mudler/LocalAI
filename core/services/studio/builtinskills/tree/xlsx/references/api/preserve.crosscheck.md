<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.crosscheck`

Cross-check the splice output against the ledger's claims.

## `LedgerCrossCheckError`

Bases: `RuntimeError`

The splice changed cells the ledger never recorded.

## `verify_splice`

```python
verify_splice(source_bytes, output_bytes, dirty_by_part, baselines = None, region_claims = None, row_claims = None)
```

Assert that in every spliced part, the set of semantically changed
cells is a subset of the ledger's dirty claims — and
that no region the saver didn't claim differs, and no row's display
attributes/multiplicity change outside the claimed rows.

``baselines`` maps parts to their post-shift bytes: those
parts are checked against the renumbered baseline (the renumber pass is
covered by its own tests and the oracle property tests).
``region_claims`` maps parts to the region tags the saver knowingly
rewrote; an unclaimed region that differs is corruption inside the
safety tooling, exactly like an unclaimed cell.
``row_claims`` maps parts to row indices whose display attributes the
saver knowingly rewrote; rows holding dirty cells are implicitly
allowed (the splice recomputes their spans/attrs when rebuilding).
