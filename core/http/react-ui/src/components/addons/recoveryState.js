// Pure rules of the data recovery panel, so the node unit tests drive them
// without a browser. The server is the authority for every answer: this
// module only shapes requests and words.
import { classifyResponse } from '../catalog/catalogState.js'

// The typed refusals the owner gives. Anything else is classified as the
// other add-on routes are.
export const RECOVERY_TYPES = new Set([
  'needs_recovery', 'addon_running', 'busy', 'backup_not_found', 'not_installed', 'backup_corrupt',
  'incompatible_format', 'identity_changed', 'mapping_required', 'mapping_invalid', 'downgrade_unsupported',
  'sole_recovery_copy', 'confirmation_mismatch', 'operation_conflict', 'invalid_request', 'insufficient_storage',
])

export function classifyRecovery(status, body) {
  const err = body && typeof body === 'object' ? body.error : null
  const type = err && typeof err === 'object' && typeof err.type === 'string' ? err.type : ''
  if (RECOVERY_TYPES.has(type)) {
    return { kind: 'recovery', type, status, message: typeof err.message === 'string' ? err.message : '' }
  }
  return classifyResponse(status, body)
}

// What the banner says about the state of the data. `ready` has no banner.
export function bannerKind(status) {
  const s = status && status.state
  if (!s || s === 'ready') return ''
  if (s === 'needs-recovery' || s === 'upgrading' || s === 'restoring' || s === 'purging') return s
  // A state this page does not know is shown as a problem, never as ready.
  return 'needs-recovery'
}

// The order of the phases of an upgrade, for the progress line.
export const UPGRADE_PHASES = ['begin', 'backed-up', 'staged', 'migrating', 'healthy', 'publishing']

export function phaseName(phase) {
  const [kind, step] = String(phase || '').split(':')
  return { kind: kind || '', step: step || '' }
}

export function phaseProgress(phase) {
  const { kind, step } = phaseName(phase)
  if (kind !== 'upgrade') return null
  const i = UPGRADE_PHASES.indexOf(step)
  return { index: i < 0 ? 0 : i + 1, total: UPGRADE_PHASES.length, step }
}

// The verification state of one backup, as a key and a tone. The text is
// always the signal; the tone only helps.
export function verificationOf(backup) {
  switch (backup && backup.verification) {
    case 'verified': return { key: 'verified', tone: 'ok' }
    case 'failed': return { key: 'failed', tone: 'error' }
    default: return { key: 'unverified', tone: 'warn' }
  }
}

// The words for a refusal.
export function recoveryErrorKey(error) {
  if (!error) return 'unavailable'
  if (error.kind === 'recovery') return error.type
  if (error.kind === 'unknown_outcome') return 'unknownOutcome'
  if (error.kind === 'auth') return 'auth'
  if (error.kind === 'denied') return 'denied'
  if (error.kind === 'network') return 'network'
  return 'unavailable'
}

// Whether the restore button can be pressed. The server decides; this only
// keeps a request from being sent that cannot succeed.
export function restoreBlocked(addon, status) {
  if (!addon || !status) return 'loading'
  if (addon.state === 'running' || addon.state === 'starting') return 'running'
  return ''
}

// The body of a restore. The mapping is sent only when the backup was made in
// another ownership mode and the person named an account.
export function restoreBody({ operationId, backup, addonId, mapping }) {
  const body = { operationId, backupId: backup.id, confirm: addonId }
  const account = String(mapping || '').trim()
  if (backup.mode === 'local-owner' && account) body.mapping = { 'local-owner': account }
  return body
}

export function needsMapping(backup, status) {
  return Boolean(backup && status && backup.mode === 'local-owner' && status.mode === 'named-user')
}

export function purgeBody({ operationId, addonId, scope }) {
  return { operationId, confirm: addonId, scope: { data: Boolean(scope.data), backups: Boolean(scope.backups), recoveryCopies: Boolean(scope.recoveryCopies) } }
}

export function purgeBlocked(addon, scope) {
  if (!scope.data && !scope.backups && !scope.recoveryCopies) return 'scope'
  if (addon.enabled || addon.state === 'running' || addon.state === 'starting') return 'running'
  return ''
}

// A typed confirmation is the ID of the add-on, exactly.
export function confirmMatches(typed, addonId) {
  return typeof typed === 'string' && typed === addonId
}
