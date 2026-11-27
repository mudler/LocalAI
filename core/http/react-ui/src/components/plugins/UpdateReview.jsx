/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import Icon from '../Icon'
import { Button, Callout, LoadingSpinner, Tag } from '../ui'
import { CodeFile, DiffView, PackageFacts, Sha } from './PluginBits'
import { addedReasons, diffLines, errorKey, updateReady } from './pluginState'

// The review of an update (spec 9.4): the commits, the files that change
// with their line counts, and the diff of each code file. When the preview
// asks for a code review, each changed code file needs its own tick before
// Update sends.
//
//   install     the install ({ id, name, plugin })
//   api, onQueued(job), onClose
export default function UpdateReview({ install, api, onQueued, onClose }) {
  const { t } = useTranslation('addons')
  const [state, setState] = useState({ status: 'loading', preview: null, error: null })
  const [ticked, setTicked] = useState(() => new Set())
  const [itemsTicked, setItemsTicked] = useState(false)
  const [sending, setSending] = useState(false)
  const [sendError, setSendError] = useState(null)
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let live = true
    setState({ status: 'loading', preview: null, error: null })
    setTicked(new Set())
    setItemsTicked(false)
    setSendError(null)
    api.previewUpdate(install.id)
      .then((preview) => { if (live) setState({ status: 'ready', preview, error: null }) })
      .catch((e) => { if (live) setState({ status: 'error', preview: null, error: e }) })
    return () => { live = false }
  }, [install.id, attempt]) // eslint-disable-line react-hooks/exhaustive-deps

  const p = state.preview
  const loadFile = (path, signal) => api.previewFile(p.token, path, signal)
  const say = (e) => (errorKey(e) ? t(errorKey(e), { defaultValue: e.message }) : e?.message || t('plugins.errors.generic'))
  const tick = (path, on) => setTicked((s) => { const n = new Set(s); if (on) n.add(path); else n.delete(path); return n })
  const gains = p ? addedReasons(p) : []
  const files = p ? [
    ...(p.added || []).map((f) => ({ ...f, change: 'added' })),
    ...(p.changed || []).map((f) => ({ ...f, change: 'changed' })),
    ...(p.removed || []).map((f) => ({ ...f, change: 'removed' })),
  ] : []

  const send = async () => {
    setSending(true)
    setSendError(null)
    try {
      onQueued?.(await api.update(install.id, p, [...ticked]))
    } catch (e) {
      setSendError(e)
    } finally {
      setSending(false)
    }
  }

  return (
    <Dialog
      wide
      title={t('plugins.update.title', { name: install.name || install.plugin })}
      onClose={onClose}
      pending={sending}
      closeLabel={t('plugins.review.cancel')}
      testId="plugin-update-review"
      labelId="plugin-update-title"
      bodyClass="pl-dialog"
      foot={(
        <>
          <Button variant="ghost" onClick={onClose} disabled={sending} data-autofocus>{t('plugins.review.cancel')}</Button>
          {p && (
            <Button variant="primary" icon="arrow-up" busy={sending} disabled={sending || !updateReady(p, ticked, itemsTicked)} onClick={send} data-testid="plugin-update-confirm">
              {t('plugins.update.confirm')}
            </Button>
          )}
        </>
      )}
    >
      {state.status === 'loading' && (
        <div className="pl-loading" role="status"><LoadingSpinner size="sm" /> <span>{t('plugins.update.checking')}</span></div>
      )}
      {state.status === 'error' && (
        <Callout tone={state.error?.type === 'blocked' ? 'warn' : 'error'} title={t('plugins.update.failed')} role="alert" testId="plugin-update-error"
          actions={state.error?.type === 'blocked' || state.error?.type === 'no_update' ? null : <Button size="sm" icon="refresh" onClick={() => setAttempt((n) => n + 1)}>{t('plugins.review.retry')}</Button>}>
          {say(state.error)}
        </Callout>
      )}
      {p && (
        <div className="pl-review">
          <PackageFacts
            testId="plugin-update-facts"
            line={[{ key: 'commits', node: <span className="pl-commits">{t('plugins.facts.commit')} <Sha sha={p.fromSha} /> <Icon name="arrow-right" aria-hidden="true" /> <Sha sha={p.toSha} testId="plugin-update-to" /></span> }]}
            more={[{ key: 'digest', label: t('plugins.facts.digest'), node: p.toDigest, className: 'pl-mono pl-digest', testId: 'plugin-digest' }]}
          />

          {gains.length > 0 && (
            <Callout tone="warn" icon="code" title={t('plugins.update.gainsCode')} testId="plugin-update-gains">
              {t('plugins.update.gainsCodeBody')}
              <span className="ui-tags pl-tags">{gains.map((r) => <Tag key={r} mono>{t(`plugins.reasons.${r}`, { defaultValue: r })}</Tag>)}</span>
            </Callout>
          )}

          <section className="pl-section" aria-labelledby="pl-upd-files">
            <h3 className="pl-section__title" id="pl-upd-files">{t('plugins.update.files', { count: files.length })}</h3>
            {files.length === 0 ? <p className="pl-quiet">{t('plugins.update.noFiles')}</p> : (
              <ul className="pl-rows" data-testid="plugin-update-files">
                {files.map((f) => (
                  <li key={`${f.change}:${f.path}`} className="pl-row pl-row--file" data-change={f.change}>
                    <span className="pl-row__name pl-mono"><Icon name={f.change === 'added' ? 'plus' : f.change === 'removed' ? 'minus' : 'pencil'} aria-hidden="true" /> {f.path}</span>
                    <span className="pl-row__hint">{t(`plugins.update.change.${f.change}`, { old: f.oldLines, new: f.newLines })}</span>
                    {f.code && <Tag tone="warn" icon="code">{t('plugins.update.codeFile')}</Tag>}
                  </li>
                ))}
              </ul>
            )}
          </section>

          {(p.diffs || []).length > 0 && (
            <section className="pl-section" aria-labelledby="pl-upd-diffs">
              <h3 className="pl-section__title" id="pl-upd-diffs">{t('plugins.update.diffs')}</h3>
              {p.needsCodeReview && <p className="pl-quiet">{t('plugins.update.tickEach')}</p>}
              {p.diffs.map((d) => (
                <div key={d.path} className="pl-diffbox" data-testid="plugin-update-diff" data-path={d.path}>
                  <p className="pl-mono pl-diffbox__path">{d.path}</p>
                  <DiffView lines={diffLines(d.diff)} />
                </div>
              ))}
              {p.truncated && <p className="pl-quiet" data-testid="plugin-update-truncated">{t('plugins.update.truncated')}</p>}
            </section>
          )}

          {(p.newCodeFiles || []).length > 0 && (
            <section className="pl-section" aria-labelledby="pl-upd-newcode" data-testid="plugin-update-new-code">
              <h3 className="pl-section__title" id="pl-upd-newcode">{t('plugins.update.newCodeFiles')}</h3>
              <p className="pl-quiet">{t('plugins.update.newCodeFilesBody')}</p>
              {p.newCodeFiles.map((f) => <CodeFile key={f} path={f} load={loadFile} />)}
            </section>
          )}

          {(p.codeItems || []).length > 0 && (
            <section className="pl-section" aria-labelledby="pl-upd-items" data-testid="plugin-update-items">
              <h3 className="pl-section__title" id="pl-upd-items">{t('plugins.update.codeItems')}</h3>
              <p className="pl-quiet">{t('plugins.update.codeItemsBody')}</p>
              <ul className="pl-rows">
                {p.codeItems.map((it) => <li key={it} className="pl-row"><span className="pl-row__name pl-mono ad-wrap">{it}</span></li>)}
              </ul>
            </section>
          )}

          {p.needsCodeReview && (
            <section className="pl-section" aria-labelledby="pl-upd-ticks" data-testid="plugin-update-ticks">
              <h3 className="pl-section__title" id="pl-upd-ticks">{t('plugins.update.approve')}</h3>
              {(p.codeItems || []).length > 0 && (
                <label className="pl-check">
                  <input type="checkbox" className="dk-check" checked={itemsTicked} onChange={(e) => setItemsTicked(e.target.checked)} data-testid="plugin-update-tick-items" />
                  <span>{t('plugins.update.reviewedItems')}</span>
                </label>
              )}
              {(p.codeFiles || []).map((f) => (
                <label key={f} className="pl-check">
                  <input type="checkbox" className="dk-check" checked={ticked.has(f)} onChange={(e) => tick(f, e.target.checked)} data-testid="plugin-update-tick" data-path={f} />
                  <span>{t('plugins.update.reviewed')} <span className="pl-mono">{f}</span></span>
                </label>
              ))}
            </section>
          )}

          {sendError && <Callout tone="error" role="alert" testId="plugin-update-send-error">{say(sendError)}</Callout>}
        </div>
      )}
    </Dialog>
  )
}
