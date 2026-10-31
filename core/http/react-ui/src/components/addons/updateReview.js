// The words of a failed add-on upgrade, by the key the upgrade store
// (utils/addonUpgrades) reports. Pure, so the node unit tests drive it.
export function upgradeFailureWords(key, t) {
  if (key === 'canceled') return t('updates.class.canceled')
  return t(`updates.errors.${key || 'unavailable'}`)
}
