// The words of a failed add-on upgrade, by the key the upgrade store
// (utils/addonUpgrades) reports. Pure, so the node unit tests drive it.
import { HOST_FAILURES } from './updateState.js'
export function upgradeFailureWords(key, t) {
  if (key === 'canceled') return t('updates.class.canceled')
  if (HOST_FAILURES.has(key)) return t(`hostAccess.errors.${key}`)
  return t(`updates.errors.${key || 'unavailable'}`)
}
