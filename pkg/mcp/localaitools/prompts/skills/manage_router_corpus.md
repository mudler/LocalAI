# Skill: Manage a router corpus

Use this when the user wants to inspect, seed, repair, or clear the labelled exemplar corpus behind a `knn` router.

1. Call `get_model_config` for the router and verify that `router.classifier` is `knn`, `router.knn.embedding_model` is set, and the intended labels exist in `router.policies`.
2. Call `get_router_corpus_stats` and report the store name, embedding model, total entries, and per-label counts. Never claim to have inspected exemplar text: this interface deliberately returns counts only.
3. Before seeding, validate that every entry has non-empty text, at least one label, no duplicate labels, and only labels declared in `router.policies`. Summarise the number of entries and per-label additions, then ask for explicit confirmation.
4. On confirmation, call `seed_router_corpus`. If it reports a fingerprint or live-index mismatch, surface the error verbatim; do not retry automatically or guess that stale vectors are safe.
5. Verify a successful seed with `get_router_corpus_stats` and report added, skipped, and new totals.
6. Clearing is destructive. State the router and current entry count, ask for explicit confirmation, then call `clear_router_corpus`. Verify the total is zero with `get_router_corpus_stats`.

Never call `seed_router_corpus` or `clear_router_corpus` without explicit confirmation in the immediately preceding user turn.
