#!/usr/bin/env bash
# Cases of the relay gate: who may reserve, connect and administer.
# shellcheck shell=bash

# Relay flags that every gated case uses: SRV and SRV2 are the relay admins.
gated() { relay_start --admin "$SRV" --admin "$SRV2" "$@"; }

register unpaired_refused "an unpaired peer is refused at the connection and cannot reach a paired server"
case_unpaired_refused() {
  gated
  serve b SRV "$CLI"
  local out
  out=$(admin a ROGUE check | tee "$CASE_LOGS/rogue-check.log")
  expect_match "the relay shows the unpaired peer as denied" '^STATE (denied|unreachable)' "$out"
  out=$(dial a ROGUE "$SRV" --quick --timeout 15s | grep RESULT | tee "$CASE_LOGS/rogue-dial.log")
  expect_match "the unpaired peer cannot reach the server through the relay" 'RESULT connect=fail' "$out"
  expect_match "the relay logs the refusal" 'deny connection.*not paired' "$(relay_log)"
}

register paired_relayed_50mib "paired peers move 50 MiB through the relay with raised circuit limits"
case_paired_relayed_50mib() {
  gated --circuit-max-bytes 1073741824 --circuit-max-duration 30m
  serve b SRV "$CLI"
  local out
  out=$(dial a CLI "$SRV" --mb 50 --timeout 60s | tee "$CASE_LOGS/dial.log")
  expect_match "the connection runs over the relay" '^PATH relayed\(limited\)' "$out"
  expect_match "all 50 MiB arrive" '^TEST transfer 52428800/52428800 bytes .* err=<nil>' "$out"
  expect_match "streams, SSE and websocket work over the relay" '^TEST sse events=5/5' "$out"
  expect_match "a half-closed stream still gets its reply" '^TEST halfclose reply="got 12 bytes"' "$out"
  out=$(admin a CLI check | tee "$CASE_LOGS/member-check.log")
  expect_match "the relay knows the peer as a member, not an admin" '^STATE not_admin' "$out"
}

register revoke_live_circuit "revoking a device closes its live circuit and blocks the reconnect"
case_revoke_live_circuit() {
  # The transfer must outlast the revoke on a fast runner: 4000 MiB takes
  # about 45 s at 90 MB/s, the revoke comes at 12 s. The circuit byte limit
  # sits above it so that only the revoke can cut the stream.
  gated --circuit-max-bytes 8589934592 --circuit-max-duration 30m
  serve b SRV "$CLI" --revoke-after 12s --revoke-relay-only
  local out total=4194304000 got
  out=$(dial a CLI "$SRV" --mb 4000 --timeout 90s | tee "$CASE_LOGS/transfer.log")
  got=$(transfer_bytes "$out")
  expect_true "the transfer was cut before the end (${got:-0} of $total bytes)" test "${got:-0}" -lt "$total"
  expect_true "some data flowed before the revoke" test "${got:-0}" -gt 0
  expect_match "the connection is gone" '^PATH_FINAL not-connected' "$out"
  sleep 2
  out=$(dial a CLI "$SRV" --quick --timeout 15s | grep RESULT)
  expect_match "the revoked peer cannot reconnect" 'RESULT connect=fail' "$out"
  expect_match "the relay closed the live connection" 'relay: revoked" .*connections=[1-9]' "$(relay_log)"
}

register non_admin_refused "a paired non-admin is refused by the admin protocol, a stranger cannot open it"
case_non_admin_refused() {
  gated
  serve b SRV "$CLI"
  local out
  out=$(admin b SRV check --expect "$CLI" | tee "$CASE_LOGS/admin-check.log")
  expect_match "the admin's state is in sync with the relay" '^STATE in_sync' "$out"
  out=$(admin a CLI list)
  expect_match "a paired non-admin cannot list" 'ok=false err="not an admin"' "$out"
  out=$(admin a CLI add "$ROGUE")
  expect_match "a paired non-admin cannot add" 'ok=false err="not an admin"' "$out"
  out=$(admin a ROGUE list)
  expect_nomatch "a stranger gets no answer from the admin protocol" 'ok=true' "$out"
}

register two_admins "two admins on one relay see only their own members"
case_two_admins() {
  gated
  serve b SRV "$CLI"
  serve a SRV2 "$CLI2"
  local out
  out=$(dial b CLI2 "$SRV2" --quick --timeout 15s | grep RESULT)
  expect_match "a member reaches the server of its own admin" 'RESULT connect=ok' "$out"
  out=$(dial a CLI "$SRV2" --quick --timeout 15s | grep RESULT)
  expect_match "a member of admin 1 cannot reach the server of admin 2" 'RESULT connect=fail' "$out"
  out=$(dial b CLI2 "$SRV" --quick --timeout 15s | grep RESULT)
  expect_match "a member of admin 2 cannot reach the server of admin 1" 'RESULT connect=fail' "$out"
  expect_match "the relay logs the missing common admin" 'deny circuit.*no common admin' "$(relay_log)"
  out=$(admin b SRV list | tee "$CASE_LOGS/list-srv.log")
  expect_match "admin 1 lists its own member" "$CLI" "$out"
  expect_nomatch "admin 1 does not list the member of admin 2" "$CLI2" "$out"
  out=$(admin a SRV2 list | tee "$CASE_LOGS/list-srv2.log")
  expect_match "admin 2 lists its own member" "$CLI2" "$out"
  expect_nomatch "admin 2 does not list the member of admin 1" "$CLI" "$out"
  admin a SRV2 remove "$CLI" >/dev/null
  out=$(dial a CLI "$SRV" --quick --timeout 15s | grep RESULT)
  expect_match "removing a peer that is not in your set changes nothing" 'RESULT connect=ok' "$out"
}
