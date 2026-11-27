/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import { Button, Callout, LoadingSpinner, Segmented, Tag } from '../ui'
import { formatBytes } from '../../utils/format'
import { CodeFile, ComponentList, ContributionTags, PackageFacts, Sha, SkippedList } from './PluginBits'
import { errorKey, scopeChoices } from './pluginState'

// The review before an install (spec 9.3). It fetches and checks the plugin
// at a full commit (inspect), then shows what it adds, the commit, the digest
// and, for a plugin that runs code, every code file in full. Install sends
// the one-use token of that review: the server fetches the same commit again
// and refuses files that differ.
//
//   entry       the catalog item ({ plugin, name, marketplace, ... })
//   api         the plugins client
//   isAdmin     an administrator chooses the scope
//   onReviewed(preview)   the preview, so the card can show what it adds
//   onQueued(job, preview), onClose
export default function InstallReview({ entry, api, isAdmin, onReviewed, onQueued, onClose }) {
  const { t } = useTranslation('addons')
  const [state, setState] = useState({ status: 'loading', preview: null, error: null })
  const [scope, setScope] = useState('')
  const [read, setRead] = useState(false)
  const [sending, setSending] = useState(false)
  const [sendError, setSendError] = useState(null)
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    const ctl = new AbortController()
    setState({ status: 'loading', preview: null, error: null })
    setSendError(null)
    setRead(false)
    api.inspect(entry.plugin, ctl.signal)
      .then((preview) => {
        setState({ status: 'ready', preview, error: null })
        setScope(scopeChoices({ isAdmin, runsCode: preview.runsCode })[0])
        onReviewed?.(preview)
      })
      .catch((e) => { if (!ctl.signal.aborted) setState({ status: 'error', preview: null, error: e }) })
    return () => ctl.abort()
  }, [entry.plugin, attempt]) // eslint-disable-line react-hooks/exhaustive-deps

  const p = state.preview
  const choices = p ? scopeChoices({ isAdmin, runsCode: p.runsCode }) : []
  // A user does not install a plugin that runs code: the review says who can.
  const userCode = Boolean(p && p.runsCode && !isAdmin)
  const blocked = p && p.runsCode && !read
  const loadFile = useCallback((path, signal) => api.previewFile(p.token, path, signal), [api, p])
  const say = (e) => (errorKey(e) ? t(errorKey(e), { defaultValue: e.message }) : e?.message || t('plugins.errors.generic'))

  const install = async () => {
    setSending(true)
    setSendError(null)
    try {
      const job = await api.install(p, scope)
      onQueued?.(job, p)
    } catch (e) {
      setSendError(e)
    } finally {
      setSending(false)
    }
  }

  const foot = (
    <>
      <Button variant="ghost" onClick={onClose} disabled={sending} data-autofocus>{t('plugins.review.cancel')}</Button>
      {state.status === 'ready' && !userCode && (
        <Button variant="primary" icon="download" busy={sending} disabled={sending || blocked} onClick={install} data-testid="plugin-install-confirm">
          {t('plugins.review.install')}
        </Button>
      )}
    </>
  )

  return (
    <Dialog
      wide
      title={t('plugins.review.title', { name: entry.name })}
      description={entry.description || undefined}
      onClose={onClose}
      pending={sending}
      closeLabel={t('plugins.review.cancel')}
      foot={foot}
      testId="plugin-install-review"
      labelId="plugin-review-title"
      bodyClass="pl-dialog"
    >
      {state.status === 'loading' && (
        <div className="pl-loading" role="status" data-testid="plugin-review-loading">
          <LoadingSpinner size="sm" /> <span>{t('plugins.review.checking')}</span>
        </div>
      )}
      {state.status === 'error' && (
        <Callout tone="error" title={t('plugins.review.failed')} role="alert" testId="plugin-review-error"
          actions={<Button size="sm" icon="refresh" onClick={() => setAttempt((n) => n + 1)}>{t('plugins.review.retry')}</Button>}>
          {say(state.error)}
        </Callout>
      )}
      {p && (
        <div className="pl-review">
          <Callout tone="info" icon="shield" title={t('plugins.review.unsigned')} testId="plugin-review-unsigned">
            {t('plugins.review.unsignedBody')}
          </Callout>

          <PackageFacts
            line={[
              ...(p.version ? [{ key: 'version', node: t('plugins.facts.versionValue', { version: p.version }) }] : []),
              { key: 'commit', node: <>{t('plugins.facts.commit')} <Sha sha={p.sha} testId="plugin-review-sha" /></> },
              { key: 'size', node: t('plugins.facts.sizeValue', { size: formatBytes(p.sizeBytes), count: p.fileCount }) },
            ]}
            more={[
              { key: 'plugin', label: t('plugins.facts.plugin'), node: p.plugin, className: 'pl-mono' },
              { key: 'digest', label: t('plugins.facts.digest'), node: p.digest, className: 'pl-mono pl-digest', testId: 'plugin-digest' },
            ]}
          />

          <section className="pl-section" aria-labelledby="pl-adds">
            <h3 className="pl-section__title" id="pl-adds">{t('plugins.review.adds')}</h3>
            <ContributionTags model={p.model} hosts={p.hosts} runsCode={p.runsCode} />
            <ComponentList model={p.model} />
          </section>

          {(p.model?.skipped || []).length > 0 && (
            <section className="pl-section" aria-labelledby="pl-skipped">
              <h3 className="pl-section__title" id="pl-skipped">{t('plugins.review.skipped')}</h3>
              <SkippedList skipped={p.model.skipped} />
            </section>
          )}

          {p.runsCode && (
            <section className="pl-section" aria-labelledby="pl-code" data-testid="plugin-review-code">
              <h3 className="pl-section__title" id="pl-code">{t('plugins.review.code')}</h3>
              <Callout tone="warn" icon="code" title={t('plugins.review.codeTitle')}>
                {t('plugins.review.codeBody')}
                <span className="ui-tags pl-tags">
                  {(p.codeReasons || []).map((r) => <Tag key={r} mono>{t(`plugins.reasons.${r}`, { defaultValue: r })}</Tag>)}
                </span>
              </Callout>
              {(p.codeFiles || []).map((f, i) => <CodeFile key={f} path={f} load={loadFile} open={i === 0} />)}
              {userCode ? (
                <Callout tone="info" icon="user-shield" testId="plugin-review-admin-only">{t('plugins.browse.adminOnly')}</Callout>
              ) : (
              <label className="pl-check">
                <input type="checkbox" className="dk-check" checked={read} onChange={(e) => setRead(e.target.checked)} data-testid="plugin-review-read" />
                <span>{t('plugins.review.read')}</span>
              </label>
              )}
            </section>
          )}

          {userCode ? null : choices.length > 1 ? (
            <section className="pl-section" aria-labelledby="pl-scope">
              <h3 className="pl-section__title" id="pl-scope">{t('plugins.review.scope')}</h3>
              <Segmented mode="radio" labelledBy="pl-scope" value={scope} onChange={setScope} itemTestId="plugin-scope-"
                options={choices.map((id) => ({ id, label: t(`plugins.scope.${id}`) }))} />
            </section>
          ) : (
            <p className="pl-quiet" data-testid="plugin-review-scope">{t(`plugins.review.scopeOnly.${choices[0] || 'user'}`)}</p>
          )}

          {sendError && (
            <Callout tone="error" role="alert" testId="plugin-install-error"
              actions={sendError.type === 'invalid_token' ? <Button size="sm" icon="refresh" onClick={() => setAttempt((n) => n + 1)}>{t('plugins.review.again')}</Button> : null}>
              {say(sendError)}
            </Callout>
          )}
        </div>
      )}
    </Dialog>
  )
}
