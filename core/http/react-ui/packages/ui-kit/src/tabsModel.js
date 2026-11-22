// The pure rules of <Tabs>, so the node unit tests drive them without a
// browser. A tab list is one tab stop: the selected tab holds it (the first
// enabled one when nothing is selected). The arrow keys of the list's axis,
// Home and End move the focus and the selection together, skip disabled tabs
// and wrap at both ends.
import { radioKeyTarget, radioTabStop } from './segmentedModel.js'

const AXIS_KEYS = {
  horizontal: new Set(['ArrowLeft', 'ArrowRight', 'Home', 'End']),
  vertical: new Set(['ArrowUp', 'ArrowDown', 'Home', 'End']),
}

// The index of the tab that is reachable with Tab, or -1 when none is enabled.
export const tabStop = (items, value) => radioTabStop(items, value)

// The index a key moves to, or -1 when the key does nothing on this axis.
// `from` is the index of the focused tab.
export function tabKeyTarget(items, from, key, orientation = 'horizontal') {
  if (!AXIS_KEYS[orientation]?.has(key)) return -1
  return radioKeyTarget(items, from, key)
}

// The ids that tie a tab to its panel: `<prefix>-tab-<id>` and
// `<prefix>-panel-<id>`. A list with one shared panel passes `panelId`.
export const tabId = (prefix, id) => `${prefix}-tab-${id}`
export const tabPanelId = (prefix, id, panelId) => panelId || `${prefix}-panel-${id}`
