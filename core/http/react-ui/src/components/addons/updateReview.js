// The add-on side of the review sheet: which installed add-ons have a pending
// update, what each row says, and the words of a failed upgrade. Pure, so the
// node unit tests drive it. The owner decides whether an update exists and
// whether it fits; this never compares versions.
import { updateView } from './updateState.js'
import { shortDigest } from './addonState.js'

// The rows of the sheet: every installed add-on whose check found a candidate.
// A candidate the host cannot run is listed with its reasons and is not
// selectable. `t` is the add-ons translator.
export function reviewItems(installed, t) {
  const rows = []
  for (const addon of installed || []) {
    const view = updateView(addon.update)
    if (view.state === 'incompatible') {
      const reasons = view.reasons.map(r => t(`updates.reason.${r}`))
      rows.push(row(addon, view, reasons.length > 0 ? reasons : [t('updates.state.incompatible', { version: view.version })]))
    } else if (view.state === 'update-available' && view.canUpgrade && addon.digest) {
      rows.push(row(addon, view, []))
    }
  }
  return rows.sort((a, b) => a.name.localeCompare(b.name))
}

function row(addon, view, blocked) {
  const where = [addon.sourceId, addon.channel || addon.selection].filter(Boolean).join(' · ')
  const digests = view.digest && addon.digest ? `${shortDigest(addon.digest)} → ${shortDigest(view.digest)}` : ''
  return {
    key: addon.id,
    name: addon.title || addon.id,
    from: addon.version || '',
    to: view.version || '',
    blocked,
    detail: [where, digests].filter(Boolean).join(' · '),
  }
}

// The version a card offers, or '' when there is nothing to upgrade to here.
export function upgradeVersionOf(addon) {
  if (!addon) return ''
  const view = updateView(addon.update)
  return view.canUpgrade && addon.digest ? view.version : ''
}

// The words of a failed upgrade, by the key the upgrade store reports.
export function upgradeFailureWords(key, t) {
  if (key === 'canceled') return t('updates.class.canceled')
  return t(`updates.errors.${key || 'unavailable'}`)
}
