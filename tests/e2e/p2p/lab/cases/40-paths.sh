#!/usr/bin/env bash
# Cases of the paths between two peers: hole punching, revoke by the server,
# live limit changes and an invite that carries a relay pass.
# shellcheck shell=bash

register hole_punch "two peers behind cone NATs connect through the relay, then punch a direct path"
case_hole_punch() {
  set_nats cone cone
  observers
  relay_start --admin "$SRV"
  serve b SRV "$CLI"
  local out
  # A host can punch only once it knows its public address, so the dialer waits
  # before its first dial.
  out=$(dial a CLI "$SRV" --warmup 20s --wait-direct 30s --mb 50 --timeout 90s | tee "$CASE_LOGS/dial.log")
  expect_match "the connection is direct, not relayed" '^PATH (hole-punched|direct)' "$out"
  expect_match "all 50 MiB arrive" '^TEST transfer 52428800/52428800 bytes .* err=<nil>' "$out"
  expect_match "the path stays direct to the end" '^PATH_FINAL hole-punched' "$out"
}

register no_punch_symmetric "behind symmetric NATs the hole punch fails and the relay carries the traffic"
case_no_punch_symmetric() {
  observers
  gated --circuit-max-bytes 1073741824 --circuit-max-duration 30m
  serve b SRV "$CLI"
  local out
  out=$(dial a CLI "$SRV" --warmup 20s --wait-direct 10s --mb 20 --timeout 90s | tee "$CASE_LOGS/dial.log")
  expect_match "the path stays on the relay" '^PATH relayed\(limited\)' "$out"
  expect_match "the relay reported its limits to the dialer" 'limit_bytes=1073741824 limit_seconds=1800' "$out"
  expect_match "the data arrives over the relay" '^TEST transfer 20971520/20971520 bytes .* err=<nil>' "$out"
}

register revoke_server "a revoke by the server closes the live stream, the relay drops the peer and it cannot come back"
case_revoke_server() {
  # Same sizing as revoke_live_circuit: the revoke at 12 s must land inside
  # the transfer, and the circuit byte limit must not end it first.
  gated --circuit-max-bytes 8589934592 --circuit-max-duration 30m
  serve b SRV "$CLI" --revoke-after 12s
  local out total=4194304000 got
  out=$(dial a CLI "$SRV" --mb 4000 --timeout 90s | tee "$CASE_LOGS/transfer.log")
  got=$(transfer_bytes "$out")
  expect_true "the transfer was cut before the end (${got:-0} of $total bytes)" test "${got:-0}" -lt "$total"
  expect_true "some data flowed before the revoke" test "${got:-0}" -gt 0
  expect_match "the server logs the revoke" '^REVOKED' "$(get b SRV.out)"
  # The relay learns of the revoke from the server's manager.
  waitfor relay "$RELAY_LOG.out" 'relay: revoked' 20
  expect_match "the relay removed the peer" 'relay: revoked' "$(relay_log)"
  out=$(dial a CLI "$SRV" --quick --timeout 15s | grep RESULT)
  expect_match "the revoked peer cannot reconnect" 'RESULT connect=fail' "$out"
  out=$(admin b SRV list)
  expect_nomatch "the relay no longer lists the peer" "$CLI" "$out"
}

register limits_live "the server changes the circuit limits of its relay and the next circuit reports them"
case_limits_live() {
  gated --circuit-max-bytes 1073741824 --circuit-max-duration 30m
  serve b SRV "$CLI" --limits-after 8s --limit-bytes 536870912 --limit-seconds 900
  local out
  out=$(dial a CLI "$SRV" --quick --timeout 20s | tee "$CASE_LOGS/before.log")
  expect_match "the first circuit reports the flag limits" 'limit_bytes=1073741824 limit_seconds=1800' "$out"
  waitfor b SRV.out '^LIMITS_SET' 30
  expect_match "the relay applied the change and restarted its circuit service" 'LIMITS_SET ok bytes=536870912 seconds=900 restarted=true' "$(get b SRV.out)"
  out=$(admin a CLI status | tee "$CASE_LOGS/member-read.log")
  expect_match "a member reads the new limits" 'limit_bytes=536870912 limit_seconds=900' "$out"
  out=$(admin a CLI limits --bytes 1)
  expect_match "a member cannot change the limits" 'ok=false' "$out"
  # The server has its reservation again at once, so the next circuit works.
  out=$(dial a CLI "$SRV" --quick --timeout 20s | tee "$CASE_LOGS/after.log")
  expect_match "a new circuit reports the new limits" 'limit_bytes=536870912 limit_seconds=900' "$out"
  out=$(dial a CLI "$SRV" --mb 600 --timeout 60s | tee "$CASE_LOGS/over-limit.log")
  expect_true "a transfer above the new byte limit is cut" test "$(transfer_bytes "$out")" -lt 629145600
}

register invite_redeem "a device with only an invite link redeems it through the relay pass, once"
case_invite_redeem() {
  gated
  serve b SRV "" --invite
  waitfor b SRV.out '^INVITE ' 20 || fail "the server minted no invite"
  local link
  link=$(get b SRV.out | awk '/^INVITE /{print $2; exit}')
  expect_match "the link carries a relay pass" '[?&]x=' "$link"
  local out
  keygen a STRANGER1 >/dev/null
  out=$(ex a "$X redeem --link '$link' --key /root/STRANGER1.key $NODE --timeout 60s" 2>&1 | tee "$CASE_LOGS/redeem.log")
  expect_match "the redeem works through the relay" '^REDEEM ok' "$out"
  expect_match "the device connects after the redeem" 'RESULT connect=ok' "$out"
  expect_match "the relay used the pass" 'relay: pass used' "$(relay_log)"
  keygen a STRANGER2 >/dev/null
  out=$(ex a "$X redeem --link '$link' --key /root/STRANGER2.key $NODE --timeout 30s" 2>&1 | tee "$CASE_LOGS/redeem2.log")
  expect_match "the same link cannot be used twice" 'REDEEM fail' "$out"
}

register invite_without_pass "an invite link without a relay pass does not get a stranger through a gated relay"
case_invite_without_pass() {
  gated
  serve b SRV "" --invite --invite-pass=false
  waitfor b SRV.out '^INVITE ' 20 || fail "the server minted no invite"
  local link out
  link=$(get b SRV.out | awk '/^INVITE /{print $2; exit}')
  expect_nomatch "the link has no relay pass" '[?&]x=' "$link"
  keygen a STRANGER1 >/dev/null
  out=$(ex a "$X redeem --link '$link' --key /root/STRANGER1.key $NODE --timeout 30s" 2>&1 | tee "$CASE_LOGS/redeem.log")
  expect_match "the redeem fails" 'REDEEM fail' "$out"
}
