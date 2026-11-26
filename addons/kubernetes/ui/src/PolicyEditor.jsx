/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The policy editor of the Kubernetes add-on: for one cluster (by server URL),
// or for the default policy of every cluster with none, the namespaces the
// tools may touch, the write tools that work and whether Secret values may be
// read. Out of the box the default allows reads and no write. The add-on
// keeps the policy and enforces it on every call; an administrator saves it.
// Everyone who opens the page sees it.
//
// The choices apply when the administrator presses Save, so they are
// checkboxes, not switches (a switch applies at once).
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getSdk } from '../../../internal/host-compat/src/runtime.js'
import Icon from '../../../internal/host-compat/src/components/Icon'
import { connectorsApi } from '../../../internal/connector-ui/src/api'
import { Check, Field, Notice, Select, TextInput } from '../../../internal/connector-ui/src/controls'
import { ALL, DEFAULTS, DEFAULT_KEY, WRITE_TOOLS, addNamespace, argsOf, clusterKeys, contextsOf, draftOf, isPowerful, namespaceIssue, policyOf, removeNamespace, same, stateOf } from './policy'
import { subscribe } from './results'

const call = (args) => getSdk().api.read({ arguments: args })

const STATE_TEXT = { own: 'policy.ownPolicy', default: 'policy.defaultPolicy', off: 'policy.notConfigured', closed: 'policy.noNamespace' }

function Namespaces({ list, onChange, disabled, t, error }) {
  const [text, setText] = useState('')
  const add = () => { onChange(addNamespace(list, text)); setText('') }
  return (
    <Field label={t('policy.namespaces')} hint={t('policy.namespacesHint')} error={error}>
      {(p) => (
        <div className="kube-ns">
          <ul className="kube-chips" aria-label={t('policy.namespaces')} data-testid="kubernetes-policy-namespaces">
            {list.map((n) => (
              <li key={n} className="kube-chips__item">
                <code>{n === ALL ? `* (${t('policy.allNamespaces')})` : n}</code>
                <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" disabled={disabled} aria-label={t('policy.removeNamespace', { name: n })}
                  data-testid={`kubernetes-policy-ns-remove-${n}`} onClick={() => onChange(removeNamespace(list, n))}><Icon name="close" /></button>
              </li>
            ))}
          </ul>
          <div className="kube-ns__add">
            <TextInput {...p} mono autoComplete="off" spellCheck={false} maxLength={63} disabled={disabled} value={text} placeholder={t('policy.namespacePlaceholder')}
              data-testid="kubernetes-policy-ns-input" onChange={(e) => setText(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); add() } }} />
            <button type="button" className="dk-btn dk-btn--secondary" disabled={disabled || !text.trim()} data-testid="kubernetes-policy-ns-add" onClick={add}>{t('policy.addNamespace')}</button>
          </div>
        </div>
      )}
    </Field>
  )
}

export default function PolicyEditor() {
  const { t } = useTranslation('kubernetes')
  const [view, setView] = useState(null)
  const [keys, setKeys] = useState([])
  const [connectors, setConnectors] = useState([])
  // The default entry stays chosen only when the person chose it; a first
  // cluster that appears later takes its place otherwise.
  const pickedDefault = useRef(false)
  const [key, setKey] = useState('')
  const [draft, setDraft] = useState(null)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      const res = await call({ tool: 'get-policy' })
      let connectors = []
      try { connectors = (await connectorsApi.list()).connectors || [] } catch (_e) { /* the saved policies still show */ }
      const list = clusterKeys(connectors, res.data?.clusters)
      setView(res.data || {})
      setKeys(list)
      setConnectors(connectors)
      const first = list[0] || DEFAULT_KEY
      setKey((k) => ((k === DEFAULT_KEY && pickedDefault.current) || list.includes(k) ? k : first))
      setError('')
    } catch (e) { setError(e?.message || t('policy.loadFailed')) }
  }, [t])
  useEffect(() => { load() }, [load])
  // A saved connection runs its check: read the clusters again then.
  useEffect(() => subscribe(() => { load() }), [load])
  useEffect(() => { if (view && key) setDraft(draftOf(policyOf(view, key))) }, [view, key])

  if (!view) {
    return (
      <section className="kube-settings" data-testid="kubernetes-policy" aria-busy={!error}>
        <h2 className="kube-settings__title">{t('policy.title')}</h2>
        {error ? <Notice tone="error" role="alert">{error}</Notice> : <p className="cn-muted" role="status">{t('policy.loading')}</p>}
      </section>
    )
  }
  const editable = view.admin === true
  const current = key ? policyOf(view, key) : null
  const state = key ? stateOf(view, key) : 'off'
  const isDefault = key === DEFAULT_KEY
  const labelOf = (k) => {
    if (k === DEFAULT_KEY) return t('policy.defaultTarget')
    const names = contextsOf(connectors, k)
    return names.length ? `${k} (${names.join(', ')})` : k
  }
  const stateText = isDefault ? t(view.defaultSet ? 'policy.defaultSet' : 'policy.defaultBuiltin') : t(STATE_TEXT[state])
  const legacy = Array.isArray(view.legacy) ? view.legacy : []
  const invalid = Array.isArray(view.invalid) ? view.invalid : []
  const issue = draft ? namespaceIssue(draft.namespaces) : null
  const dirty = Boolean(draft && current && !same(draft, current))
  const edit = (patch) => { setDraft({ ...draft, ...patch }); setSaved(false) }
  const toggleTool = (tool, on) => edit({ writeTools: on ? [...draft.writeTools, tool] : draft.writeTools.filter((x) => x !== tool) })

  const save = async (e) => {
    e.preventDefault()
    if (issue || !editable || !key) return
    setBusy(true)
    setError('')
    try {
      await call(argsOf(key, draft))
      setSaved(true)
    } catch (err) {
      setError(err?.message || t('policy.saveFailed'))
      setBusy(false)
      return
    }
    // The add-on is the authority: a save can change more than this policy
    // (a damaged file is replaced, entries that never apply are dropped), so
    // read every policy again.
    await load()
    setBusy(false)
  }

  return (
    <section className="kube-settings" data-testid="kubernetes-policy" aria-labelledby="kube-policy-title">
      <h2 className="kube-settings__title" id="kube-policy-title">{t('policy.title')}</h2>
      <p className="cn-muted">{t('policy.intro')}</p>
      {!editable && <Notice tone="info" role="note" testId="kubernetes-policy-readonly">{t('policy.readOnly')}</Notice>}
      {view.broken && <Notice tone="error" role="alert" testId="kubernetes-policy-broken">{t('policy.broken')}</Notice>}
      {error && <Notice tone="error" role="alert" testId="kubernetes-policy-error">{error}</Notice>}
      {saved && <Notice tone="ok" role="status" testId="kubernetes-policy-saved">{t('policy.saved')}</Notice>}
      {legacy.length > 0 && <Notice tone="warn" role="note" testId="kubernetes-policy-legacy">{t('policy.legacy', { list: legacy.join(', ') })}</Notice>}
      {invalid.length > 0 && <Notice tone="warn" role="note" testId="kubernetes-policy-invalid">{t('policy.invalid', { list: invalid.join(', ') })}</Notice>}
      {keys.length === 0 && <p className="cn-muted" data-testid="kubernetes-policy-none">{t('policy.noClusters')}</p>}
      <form className="kube-policy__form" onSubmit={save} aria-describedby="kube-policy-title">
        <Field label={t('policy.cluster')} hint={t('policy.clusterHint')}>
          {(p) => (
            <Select {...p} data-testid="kubernetes-policy-cluster" value={key} onChange={(e) => { pickedDefault.current = e.target.value === DEFAULT_KEY; setKey(e.target.value); setSaved(false) }}>
              {[...keys, DEFAULT_KEY].map((k) => <option key={k} value={k}>{labelOf(k)}</option>)}
            </Select>
          )}
        </Field>
        {(state === 'off' || state === 'closed') && !isDefault
          ? <Notice tone="info" role="note" testId="kubernetes-policy-own">{stateText}</Notice>
          : <p className="cn-muted" data-testid="kubernetes-policy-own" data-state={state}>{stateText}</p>}
        {draft && (
          <>
            <fieldset className="kube-fieldset" disabled={!editable || busy}>
              <legend className="kube-legend">{t('policy.namespaces')}</legend>
              <Namespaces list={draft.namespaces} onChange={(namespaces) => edit({ namespaces })} disabled={!editable || busy} t={t}
                error={issue ? t(`policy.${issue.key}`, { entry: issue.entry }) : undefined} />
            </fieldset>
            <fieldset className="kube-fieldset" disabled={!editable || busy}>
              <legend className="kube-legend">{t('policy.tools')}</legend>
              <p className="cn-muted">{t('policy.toolsHint')}</p>
              <ul className="kube-tools">
                {WRITE_TOOLS.map((tool) => (
                  <li key={tool} className="kube-tools__item">
                    <Check checked={draft.writeTools.includes(tool)} onChange={(on) => toggleTool(tool, on)} testId={`kubernetes-policy-tool-${tool}`}>
                      {t(`tool.${tool}`)} <code className="kube-tools__name">{tool}</code>
                    </Check>
                    {isPowerful(tool) && <Notice tone="warn" role="note" testId={`kubernetes-policy-powerful-${tool}`}>{t('policy.powerful')}</Notice>}
                  </li>
                ))}
              </ul>
            </fieldset>
            <fieldset className="kube-fieldset" disabled={!editable || busy}>
              <legend className="kube-legend">{t('policy.secrets')}</legend>
              <Check checked={draft.readSecretValues} onChange={(on) => edit({ readSecretValues: on })} testId="kubernetes-policy-secrets">{t('policy.secrets')}</Check>
              <p className="cn-muted">{t('policy.secretsHint')}</p>
              {draft.readSecretValues && <Notice tone="warn" role="note" testId="kubernetes-policy-secrets-warn">{t('policy.secretsWarn')}</Notice>}
            </fieldset>
            <div className="kube-actions">
              <button type="submit" className="dk-btn dk-btn--primary" data-testid="kubernetes-policy-save" disabled={!editable || busy || !dirty || Boolean(issue)}>{t('policy.save')}</button>
              <button type="button" className="dk-btn dk-btn--ghost" data-testid="kubernetes-policy-restore" onClick={() => edit(draftOf(isDefault ? (view.builtinDefault || DEFAULTS) : policyOf(view, DEFAULT_KEY)))} disabled={!editable || busy}>{t('policy.restore')}</button>
            </div>
          </>
        )}
      </form>
    </section>
  )
}
