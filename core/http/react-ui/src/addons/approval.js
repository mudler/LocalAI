// Pure rules of the host approval frame. The frame belongs to the host: the
// add-on's approval-card component draws details only. It gets data, no
// callback and no digest, and Confirm and Cancel are the host's buttons that
// send exactly the stored proposal's digest to the same routes the rest of the
// host uses. The server still accepts a confirmation only from the proposer's
// own browser session (see the proposals routes).

import { validProposalId } from './slots.js'

export function approvalState(proposal, now = Date.now()) {
  if (!proposal || typeof proposal !== 'object' || !validProposalId(proposal.id)) {
    return { canDecide: false, state: '', expired: false, count: 0, tool: '', expiresAt: null }
  }
  const expiresAt = Date.parse(proposal.expiresAt)
  const expired = Number.isFinite(expiresAt) && expiresAt <= now
  const count = Number(proposal.effects && proposal.effects.count)
  const tool = proposal.arguments && typeof proposal.arguments.tool === 'string' ? proposal.arguments.tool : ''
  return {
    canDecide: proposal.state === 'pending' && !expired && proposal.blocked !== true && typeof proposal.digest === 'string' && proposal.digest !== '',
    state: String(proposal.state || ''),
    expired,
    count: Number.isFinite(count) ? count : 0,
    tool,
    expiresAt: Number.isFinite(expiresAt) ? expiresAt : null,
  }
}

// The confirmation the human's click sends. The header names the proposal the
// person is looking at and the body binds the digest that was displayed.
export function confirmCall(proposal) {
  if (!proposal || !validProposalId(proposal.id) || typeof proposal.digest !== 'string' || proposal.digest === '') return null
  return {
    path: `/api/addons/proposals/${encodeURIComponent(proposal.id)}/confirm`,
    method: 'POST',
    headers: { 'X-LocalAI-Confirm': proposal.id },
    body: { digest: proposal.digest },
  }
}

export function cancelCall(proposal) {
  if (!proposal || !validProposalId(proposal.id)) return null
  return { path: `/api/addons/proposals/${encodeURIComponent(proposal.id)}/cancel`, method: 'POST', headers: {}, body: undefined }
}

const detach = (value) => {
  try { return JSON.parse(JSON.stringify(value === undefined ? null : value)) } catch { return null }
}

// What the declared approval-card component receives: plain data copies of
// the stored proposal's details.
export function rendererProps(proposal) {
  const s = approvalState(proposal)
  return Object.freeze({
    proposalId: String(proposal && proposal.id),
    tool: s.tool,
    effects: Object.freeze({ count: s.count, sample: detach(proposal && proposal.effects && proposal.effects.sample) }),
    arguments: detach(proposal && proposal.arguments),
  })
}

// The facts the proposal card draws, all taken from the stored proposal the
// host returned. Nothing here comes from model text.
export function proposalFacts(proposal, now = Date.now()) {
  const s = approvalState(proposal, now)
  const args = proposal && proposal.arguments && typeof proposal.arguments === 'object' ? proposal.arguments : {}
  const sample = proposal && proposal.effects ? proposal.effects.sample : undefined
  return {
    addonId: String((proposal && proposal.addonId) || ''),
    // A create proposal names a reservation; the card shows the requested name.
    database: String((proposal && proposal.resourceId) || ''),
    requestedName: s.tool === 'databases-create' && typeof args.name === 'string' ? args.name : '',
    operation: s.tool,
    action: String((proposal && proposal.action) || ''),
    count: s.count,
    sample,
    expiresAt: s.expiresAt,
    state: s.state,
    expired: s.expired,
    blocked: !!(proposal && proposal.blocked === true),
    canDecide: s.canDecide,
    settled: ['applied', 'cancelled', 'expired', 'rejected', 'abandoned-unknown'].includes(s.state) || (s.state === 'pending' && s.expired),
    unknownOutcome: s.state === 'outcome-unknown' || s.state === 'abandoned-unknown',
  }
}
