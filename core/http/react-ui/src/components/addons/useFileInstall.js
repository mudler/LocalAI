import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useAddonActions } from './useAddonActions'
import { addonsApi, addonsCatalog, addonFilesApi } from '../../utils/addonsClient'
import { fileEntry, fileIdFromParams, normalizeFile } from './fileInstall'

// Install from a file. The button takes the add-on zip that `make addons-zip`
// writes; the owner checks it and the person sees the same dialog as for a
// library entry, with the host access step and the notice that nothing signs a
// local file. A link from `local-ai addons install` carries ?file=<id> of a
// file the command line staged, and opens the dialog straight away.
//
// The install runs in the actions of this hook, not in the dialog, so the
// dialog can be closed while it runs. What the person closed is then `kept`:
// the page shows it as an install in progress, and as a failure that stays
// until it is dismissed, with the reason and a way to try again.
export function useFileInstall({ onInstalled, filesApi = addonFilesApi } = {}) {
  const [params, setParams] = useSearchParams()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)
  const [entry, setEntry] = useState(null)
  const [kept, setKept] = useState(null)
  const actions = useAddonActions(addonsApi, { onSettled: () => onInstalled && onInstalled(), adapter: addonsCatalog })
  const linked = fileIdFromParams(params)
  const asked = useRef('')

  const show = useCallback((raw) => {
    const file = normalizeFile(raw)
    if (!file.fileId || !file.addonId) throw new Error('unexpected answer')
    setKept(null)
    setEntry(fileEntry(file))
  }, [])

  const pick = async (file) => {
    if (!file) return
    setError(null)
    setBusy(true)
    try { show(await filesApi.upload(file)) } catch (err) { setError(err) } finally { setBusy(false) }
  }

  // A staged file the link names. Read once per id.
  useEffect(() => {
    if (!linked || asked.current === linked) return undefined
    asked.current = linked
    const ctl = new AbortController()
    setBusy(true)
    filesApi.describe(linked, ctl.signal)
      .then(show)
      .catch(err => { if (!err || err.name !== 'AbortError') setError(err) })
      .finally(() => setBusy(false))
    return () => ctl.abort()
  }, [linked, filesApi, show])

  const flowOf = (e) => (e ? actions.flows[`flow:${e.key}`] : undefined)
  const close = () => {
    const done = entry
    setEntry(null)
    if (linked) setParams(prev => { const out = new URLSearchParams(prev); out.delete('file'); return out }, { replace: true })
    // A file that was not installed is not kept for the next visit.
    const flow = flowOf(done)
    // A file still being installed is needed until that ends, and the page
    // keeps telling the person how it goes.
    const running = flow && (flow.step === 'install' || flow.step === 'turn-on')
    if (done && running) setKept(done)
    else if (done && !(flow && flow.step === 'done')) filesApi.remove(done.local.fileId).catch(() => {})
  }

  const keptFlow = flowOf(kept)
  // The install of a closed dialog ended well: nothing more to show.
  useEffect(() => {
    if (kept && (!keptFlow || keptFlow.step === 'done')) setKept(null)
  }, [kept, keptFlow])

  // Try again: the dialog opens on the same file at its first step with a new
  // preview, so the person gives the approval again. The old approval is spent
  // and is never sent a second time.
  const retryKept = () => {
    actions.clearFlow(kept)
    setEntry(kept)
    setKept(null)
  }
  const dismissKept = () => {
    const done = kept
    setKept(null)
    if (!done) return
    actions.clearFlow(done)
    filesApi.remove(done.local.fileId).catch(() => {})
  }

  // What the list shows on the row of the add-on: the install in progress or its failure.
  const step = keptFlow ? keptFlow.step : ''
  const failedOutcome = keptFlow && keptFlow.step === 'failed' ? keptFlow : null
  const activity = useMemo(() => {
    if (!kept || !step) return null
    return { addonId: kept.id, title: kept.title, upgrade: Boolean(kept.local && kept.local.installed), state: step === 'failed' ? 'failed' : 'running', outcome: failedOutcome ? failedOutcome.outcome : '', error: failedOutcome ? failedOutcome.error : null }
  }, [kept, step, failedOutcome])

  return { busy, error, clearError: () => setError(null), entry, actions, flow: flowOf(entry), pick, close, kept, keptFlow, activity, retryKept, dismissKept }
}
