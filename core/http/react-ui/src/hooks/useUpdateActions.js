import { useCallback, useState } from 'react'
import { operationsApi } from '../utils/api'
import { addonsApi } from '../utils/addonsClient'
import { addonUpgrades } from '../utils/addonUpgrades'
import { useOperations } from './useOperations'
import { useAddonUpgrade } from '../components/addons/useAddonUpgrade'

// What the Updates page does to a row outside an update run: cancel an
// operation it did not start (another tab, an automatic pass), check an add-on
// for a build it can pin, and the review of a held add-on upgrade. Every
// update itself goes through an update run (hooks/useUpgradeRun), which the
// server owns.
export function useUpdateActions({ updates }) {
  const { cancelOperation } = useOperations()
  const [review, setReview] = useState(null)
  const { readAddons } = updates

  // The add-on store: the check of an add-on and the approval of a held
  // upgrade, the same as on the Apps page.
  const addonUpgrade = useAddonUpgrade(addonsApi, { onChanged: () => readAddons() })

  const cancel = useCallback((row) => {
    // An add-on upgrade the store follows is cancelled on the server by its
    // job; one only the server lists (another tab) by its own id.
    if (row.kind === 'addon') {
      if (!addonUpgrades.cancel(row.jobID, (id) => operationsApi.cancel(id)) && row.jobID && !String(row.jobID).startsWith('addon:')) cancelOperation(row.jobID)
    } else if (row.jobID) cancelOperation(row.jobID)
  }, [cancelOperation])

  const check = useCallback((row) => { if (row.record) addonUpgrade.check(row.record) }, [addonUpgrade])
  const checking = useCallback((row) => addonUpgrade.checks[row.addonId]?.phase === 'working', [addonUpgrade.checks])

  return { cancel, check, checking, review, setReview, addonUpgrade }
}
