/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Button, Callout } from '../ui'
import InstallDialog from './InstallDialog'
import { useAddonActions } from './useAddonActions'
import { describeError } from './errors'
import { addonsApi, addonsCatalog, addonFilesApi } from '../../utils/addonsClient'
import { fileEntry, fileIdFromParams, normalizeFile } from './fileInstall'

// Install from a file. The button takes the add-on zip that `make addons-zip`
// writes; the owner checks it and the person sees the same dialog as for a
// library entry, with the host access step and the notice that nothing signs a
// local file. A link from `local-ai addons install` carries ?file=<id> of a
// file the command line staged, and opens the dialog straight away.
export default function InstallFromFile({ onInstalled, filesApi = addonFilesApi }) {
  const { t } = useTranslation('addons')
  const [params, setParams] = useSearchParams()
  const input = useRef(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)
  const [entry, setEntry] = useState(null)
  const actions = useAddonActions(addonsApi, { onSettled: () => onInstalled && onInstalled(), adapter: addonsCatalog })
  const linked = fileIdFromParams(params)
  const asked = useRef('')

  const show = useCallback((raw) => {
    const file = normalizeFile(raw)
    if (!file.fileId || !file.addonId) throw new Error('unexpected answer')
    setEntry(fileEntry(file))
  }, [])

  const fail = useCallback((e) => {
    setError(e && e.type === 'not_found' ? t('file.gone') : describeError(e, t))
  }, [t])

  const pick = async (e) => {
    const file = e.target.files && e.target.files[0]
    e.target.value = ''
    if (!file) return
    setError(null)
    setBusy(true)
    try { show(await filesApi.upload(file)) } catch (err) { fail(err) } finally { setBusy(false) }
  }

  // A staged file the link names. Read once per id.
  useEffect(() => {
    if (!linked || asked.current === linked) return undefined
    asked.current = linked
    const ctl = new AbortController()
    setBusy(true)
    filesApi.describe(linked, ctl.signal)
      .then(show)
      .catch(err => { if (!err || err.name !== 'AbortError') fail(err) })
      .finally(() => setBusy(false))
    return () => ctl.abort()
  }, [linked, filesApi, show, fail])

  const close = () => {
    const done = entry
    setEntry(null)
    if (linked) setParams(prev => { const out = new URLSearchParams(prev); out.delete('file'); return out }, { replace: true })
    // A file that was not installed is not kept for the next visit.
    const flow = done && actions.flows[`flow:${done.key}`]
    if (done && !(flow && flow.step === 'done')) filesApi.remove(done.local.fileId).catch(() => {})
  }

  return (
    <>
      <input ref={input} type="file" accept=".zip,application/zip" hidden onChange={pick} aria-label={t('file.pick')} data-testid="install-file-input" />
      <Button icon="upload" busy={busy} disabled={busy} onClick={() => input.current && input.current.click()} data-testid="addons-install-file">
        {busy ? t('file.checking') : t('file.button')}
      </Button>
      {error && (
        <div className="ad-file-error" role="alert" data-testid="install-file-error">
          <Callout tone="error" actions={<Button size="sm" onClick={() => setError(null)}>{t('catalog.close')}</Button>}>
            {t('file.rejected')} {error}
          </Callout>
        </div>
      )}
      <InstallDialog entry={entry} installed={{}} flow={entry ? actions.flows[`flow:${entry.key}`] : undefined} actions={actions} onClose={close} />
    </>
  )
}
