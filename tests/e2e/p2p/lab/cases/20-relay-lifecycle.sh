#!/usr/bin/env bash
# Cases of the relay's own life: restart, first reservation, open mode.
# shellcheck shell=bash

register relay_restart "after a relay restart the allowlist is back and the server holds a reservation within 70 s"
case_relay_restart() {
  relay_start --admin "$SRV"
  serve b SRV "$CLI"
  local out
  out=$(dial a CLI "$SRV" --quick --timeout 15s | grep RESULT)
  expect_match "the pair connects before the restart" 'RESULT connect=ok' "$out"
  relay_stop
  relay_start --admin "$SRV"
  expect_match "the allowlist is loaded from disk before any admin contact" 'paired peers: 1' "$(relay_log)"
  # The server comes back when a dial through the relay works again.
  local secs=""
  for _ in $(seq 1 100); do
    if dial a CLI "$SRV" --quick --timeout 5s | grep -q 'RESULT connect=ok'; then
      secs=$(awk -v a="$(date +%s.%N)" -v b="$RELAY_STARTED_AT" 'BEGIN{printf "%.1f", a - b}')
      break
    fi
    sleep 1
  done
  echo "  the server was reachable again ${secs:-never} s after the restart" | tee "$CASE_LOGS/restart.log"
  expect_true "the server is reachable through the relay again within 70 s" num_lt "${secs:-99999}" 70
  out=$(dial a CLI "$SRV" --quick --timeout 20s | grep RESULT)
  expect_match "the pair connects again after the restart" 'RESULT connect=ok' "$out"
}

register first_reservation "a server with a static relay has a relay address at once"
case_first_reservation() {
  relay_start --admin "$SRV"
  serve b SRV "$CLI"
  local secs
  secs=$(first_relay_seconds b SRV)
  echo "  first relay address after $secs s" | tee "$CASE_LOGS/first-reservation.log"
  expect_true "the first relay address came in under 10 s" num_lt "$secs" 10
  expect_match "the relay holds the reservation" 'reservations=[1-9]' "$(admin b SRV status)"
}

register open_mode "an open relay serves a stranger, announces itself as open and warns"
case_open_mode() {
  relay_start_open
  local out
  out=$(relay_log)
  expect_match "the banner says OPEN" 'mode: OPEN' "$out"
  expect_match "the banner warns" '^WARNING' "$out"
  serve_plain a ROGUE
  out=$(dial b CLI2 "$ROGUE" --mb 50 --timeout 60s | tee "$CASE_LOGS/dial.log")
  expect_match "an unpaired client moves 50 MiB through the open relay" '^TEST transfer 52428800/52428800 bytes .* err=<nil>' "$out"
  out=$(admin b CLI2 check | tee "$CASE_LOGS/check.log")
  expect_match "the probe reports open mode" '^STATE open' "$out"
}
