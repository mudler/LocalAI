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
  'download', 'verify', 'swap', 'start', 'not_installed', 'no_update', 'stale', 'held', 'unknown', 'unconfirmed', 'edited_keys',
  // an item that a restart of the server stopped
  'interrupted',
  // why a review item cannot start (the reason of a preview item)
  'in_run', 'installs_disabled',
  // refusals of the run routes
  'run_active', 'source_unchecked', 'run_not_finished', 'nothing_to_retry', 'not_cancellable', 'run_finished', 'invalid', 'not_found',
  'forbidden',
])

// Keys that mean something else in an update run than on the add-on pages:
// the run's own words win there.
const RUN_WORDS = { not_human_surface: 'run_not_human_surface', unavailable: 'run_unavailable' }

//
// `context` 'run' words an item or a refusal of an update run.
export function upgradeFailureWords(key, t, message = '', context = '') {
  if (context === 'run' && RUN_WORDS[key]) return t(`updates.errors.${RUN_WORDS[key]}`)
  if (key === 'canceled' || key === 'cancelled') return t('updates.class.canceled')
  if (HOST_FAILURES.has(key)) return t(`hostAccess.errors.${key}`)
  if (KNOWN.has(key)) return t(`updates.errors.${key}`)
  if (message) return message
  return t(`updates.errors.${key ? 'unknown' : 'unavailable'}`)
}
