// Pure rules of the "Review updates" sheet, shared by the add-ons and the
// backends pages, so the node unit tests drive them without a browser.
//
// An item is { key, name, from, to, blocked: string[] }. `blocked` holds the
// words of why it cannot be upgraded here (for an add-on, the incompatibility
// reasons the owner reported); an item with any is shown but never selectable
// and never part of "Upgrade all".

export function isSelectable(item) {
  return Boolean(item) && !(Array.isArray(item.blocked) && item.blocked.length > 0)
}

export function selectableKeys(items) {
  return items.filter(isSelectable).map(item => item.key)
}

// The checked keys when the sheet opens: the ones the caller preselected,
// minus any that cannot be upgraded.
export function initialSelection(items, preselect = []) {
  const ok = new Set(selectableKeys(items))
  return new Set(preselect.filter(key => ok.has(key)))
}

// A new selection with `key` flipped. A blocked or unknown key never changes it.
export function toggleKey(selection, items, key) {
  const item = items.find(i => i.key === key)
  if (!isSelectable(item)) return selection
  const next = new Set(selection)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  return next
}

// Every selectable item selected, or none when they all are already.
export function toggleAll(selection, items) {
  const keys = selectableKeys(items)
  if (keys.length > 0 && keys.every(k => selection.has(k))) return new Set()
  return new Set(keys)
}

// The items an "Upgrade selected" click runs, in list order.
export function selectedItems(items, selection) {
  return items.filter(item => selection.has(item.key) && isSelectable(item))
}

// The items an "Upgrade all" click runs: everything that can be upgraded.
export function upgradableItems(items) {
  return items.filter(isSelectable)
}

// Runs the items one after the other. One failure never stops the others: a
// run that throws counts as a failure of that item and the next one starts.
// `runOne(item)` resolves { ok, message? }. `onProgress(key, state)` is called
// with 'running' and then 'done' or 'failed' for each item. Resolves the
// per-item results in the order of the items.
export async function runBatch(items, runOne, { onProgress } = {}) {
  const results = []
  for (const item of items) {
    if (onProgress) onProgress(item.key, 'running')
    let result
    try {
      const out = await runOne(item)
      result = { key: item.key, ok: Boolean(out && out.ok), message: (out && out.message) || '' }
    } catch (error) {
      result = { key: item.key, ok: false, message: (error && error.message) || '' }
    }
    results.push(result)
    if (onProgress) onProgress(item.key, result.ok ? 'done' : 'failed')
  }
  return results
}

export function summarize(results) {
  const failed = results.filter(r => !r.ok).length
  return { total: results.length, succeeded: results.length - failed, failed }
}
