// Summary of one multi-node install run, computed from the per-node outcomes
// the run itself collected. It must not be read back from React state: a
// functional state updater runs later, during render, so counts read right
// after calling it are still zero.
//
// outcomes: { [nodeId]: { status: 'done' | 'error' } }
// Returns { success, fail, toast: { message, type } | null, autoClose }.
export function summarizeInstall(outcomes) {
  let success = 0
  let fail = 0
  for (const v of Object.values(outcomes || {})) {
    if (v?.status === 'done') success++
    else if (v?.status === 'error') fail++
  }
  const plural = (n) => (n === 1 ? '' : 's')
  let toast = null
  if (fail === 0 && success > 0) {
    toast = { message: `Installed on ${success} node${plural(success)}`, type: 'success' }
  } else if (success === 0 && fail > 0) {
    toast = { message: `Install failed on all ${fail} node${plural(fail)}`, type: 'error' }
  } else if (success > 0 && fail > 0) {
    toast = { message: `Installed on ${success}, failed on ${fail}`, type: 'warning' }
  }
  return { success, fail, toast, autoClose: fail === 0 && success > 0 }
}
