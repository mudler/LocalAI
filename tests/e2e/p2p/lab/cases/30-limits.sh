#!/usr/bin/env bash
# Cases of the circuit limits and the relay pass.
# shellcheck shell=bash

register circuit_limit "a circuit that reaches its byte limit ends the transfer"
case_circuit_limit() {
  relay_start_open --circuit-max-bytes 8388608 --circuit-max-duration 2m
  serve_plain a ROGUE
  local out got
  out=$(dial b CLI2 "$ROGUE" --mb 50 --timeout 60s | tee "$CASE_LOGS/dial.log")
  got=$(transfer_bytes "$out")
  expect_true "the transfer stopped early (${got:-0} of 52428800 bytes)" test "${got:-0}" -lt 52428800
  expect_true "the transfer stopped near the 8 MiB limit" test "${got:-0}" -gt 4194304
  expect_match "the application sees the circuit end" 'stream reset|EOF' "$out"
  expect_match "the connection is gone" '^TEST transfer .* path="not-connected"' "$out"
}

register bandwidth_cap "the relay holds a circuit to its bandwidth cap"
case_bandwidth_cap() {
  relay_start --open --rate-limit 1048576
  serve_plain a ROGUE
  local out rate
  out=$(dial b CLI2 "$ROGUE" --mb 8 --timeout 90s | tee "$CASE_LOGS/dial.log")
  rate=$(transfer_rate "$out")
  expect_match "all 8 MiB arrive" '^TEST transfer 8388608/8388608 bytes' "$out"
  echo "  rate ${rate:-?} MB/s with a cap of 1.05 MB/s" | tee "$CASE_LOGS/rate.log"
  expect_true "the rate stays near the cap" num_lt "${rate:-99999}" 1.8
}

register relay_pass "a one-time relay pass lets a stranger pair, and only once"
case_relay_pass() {
  relay_start --admin "$SRV"
  local out
  keygen a STRANGER1 >/dev/null
  keygen a STRANGER2 >/dev/null
  serve b SRV "$CLI" --pairing-open
  out=$(admin b SRV pass --code LABCODE123 --ttl 5m)
  expect_match "the admin registers a pass on the relay" 'ok=true' "$out"
  out=$(dial a STRANGER1 "$SRV" --quick --relay "$RELAY" --pass LABCODE123 --timeout 20s | grep RESULT)
  expect_match "a stranger with the pass reaches the server" 'RESULT connect=ok' "$out"
  out=$(dial a STRANGER2 "$SRV" --quick --relay "$RELAY" --pass LABCODE123 --timeout 20s | grep RESULT)
  expect_match "the same pass is refused a second time" 'RESULT connect=fail' "$out"
  out=$(dial a ROGUE "$SRV" --quick --timeout 15s | grep RESULT)
  expect_match "a stranger without a pass is refused" 'RESULT connect=fail' "$out"
}
