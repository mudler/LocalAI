import { useSyncExternalStore } from 'react'
import { addonUpgrades } from '../utils/addonUpgrades'

// The add-on upgrades this browser started: { live, records }. Live entries
// have the shape of the rows /api/operations returns, plus isAddon.
export function useAddonUpgrades() {
  return useSyncExternalStore(addonUpgrades.subscribe, addonUpgrades.getSnapshot)
}
