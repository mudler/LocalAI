// The words of a failed update, by its key. One map for every kind: the add-on
// upgrade store (utils/addonUpgrades) reports its keys, and an update run
// reports the item's error_key (the backend steps download, verify, swap and
// start, and the refusals of a run). Pure, so the node unit tests drive it.
//
// `t` is the translator of the addons namespace. `message` is the server's own
// text: a key this build does not know is said with it rather than with a
// wrong guess.
import { HOST_FAILURES } from './updateState.js'

const KNOWN = new Set([
  // add-on upgrade store
  'stale_installed', 'candidate_changed', 'upgrade_conflict', 'incompatible', 'integrity_error',
  'upgrade_failed', 'operation_conflict', 'check_failed', 'unknownOutcome', 'auth', 'denied',
  'operationNotFound', 'network', 'unavailable',
  // update runs
  'download', 'verify', 'swap', 'start', 'not_installed', 'no_update', 'stale', 'held', 'unknown', 'unconfirmed',
])

export function upgradeFailureWords(key, t, message = '') {
  if (key === 'canceled' || key === 'cancelled') return t('updates.class.canceled')
  if (HOST_FAILURES.has(key)) return t(`hostAccess.errors.${key}`)
  if (KNOWN.has(key)) return t(`updates.errors.${key}`)
  if (message) return message
  return t(`updates.errors.${key ? 'unknown' : 'unavailable'}`)
}
