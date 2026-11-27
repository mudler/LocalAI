/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { RouteProvider } from 'react-router-dom'
import { getSdk } from '../../../internal/host-compat/src/runtime.js'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import Icon from '../../../internal/host-compat/src/components/Icon'
import ConnectorPage from '../../../internal/connector-ui/src/ConnectorPage'
import { connectorsApi } from '../../../internal/connector-ui/src/api'
import { Field, Notice, RadioCards, Select, TextInput } from '../../../internal/connector-ui/src/controls'
import { pageLocale } from '../../../internal/connector-ui/src/connector'
import en from './i18n/kubernetes.en.json'
import it from './i18n/kubernetes.it.json'
import es from './i18n/kubernetes.es.json'
import de from './i18n/kubernetes.de.json'
import fr from './i18n/kubernetes.fr.json'
import zh from './i18n/kubernetes.zh-CN.json'
import id from './i18n/kubernetes.id.json'
import ko from './i18n/kubernetes.ko.json'
import pt from './i18n/kubernetes.pt-BR.json'
import bn from './i18n/kubernetes.bn.json'
import ProposalCard from './ProposalCard'
import PolicyEditor from './PolicyEditor'
import ApprovalSettings from './ApprovalSettings'
import { withName, checkFailure, checkItem, credentialBytes, extraBody, extraForm, extraValid, kubeconfigContexts, kubeconfigProblem, kubeconfigTarget, kubeSecret, MAX_CREDENTIAL, METHODS, methodOf, normalizeServer, rbacRows, splitKey, WRITE_MODES, writeModeOf } from './kube'
import { finishTest, resultOf, startTest, subscribe } from './results'
import './kubernetes.css'

// The words of the page, in every language of the web UI; a key a language lacks reads in English.
registerLocales({ kubernetes: { en, it, es, de, fr, 'zh-CN': zh, id, ko, 'pt-BR': pt, bn } })

// The pasted kubeconfig, its size, the context the connection uses and the
// server of that context. The add-on refuses the same fields again on save.
function KubeconfigFields({ form, setForm, t }) {
  const text = form.kubeconfig || ''
  const bytes = credentialBytes(text)
  const problem = kubeconfigProblem(text)
  const { contexts } = kubeconfigContexts(text)
  const target = kubeconfigTarget(form)
  const change = (kubeconfig) => {
    const found = kubeconfigContexts(kubeconfig)
    const keep = found.contexts.includes(form.context) ? form.context : ''
    setForm({ ...form, kubeconfig, context: keep })
  }
  return (
    <>
      <Field label={t('kubeconfig')} hint={t('kubeconfigHelp')} error={problem && problem !== 'tooLarge' ? t(`problem.${problem}`) : undefined}>
        {(p) => (
          <textarea {...p} className="cn-input cn-input--mono" rows={8} spellCheck={false} autoComplete="off" value={text} required={!form.id}
            placeholder={form.id ? t('credSaved') : 'apiVersion: v1\nkind: Config\n...'} data-testid="kubernetes-kubeconfig" onChange={(e) => change(e.target.value)} />
        )}
      </Field>
      {text !== '' && (
        <p className={`kube-bytes${bytes > MAX_CREDENTIAL ? ' kube-bytes--over' : ''}`} data-testid="kubernetes-bytes" aria-live="polite">
          {t('bytes', { count: bytes, max: MAX_CREDENTIAL })}
        </p>
      )}
      {problem === 'tooLarge' && (
        <Notice tone="error" role="alert" testId="kubernetes-too-large">{t('problem.tooLarge')} {t('tooLargeHint', { context: target.context || '<name>' })}</Notice>
      )}
      {(text !== '' || form.id) && (
        <Field label={t('context')} hint={t('contextHelp')}>
          {(p) => (contexts.length > 0 ? (
            <Select {...p} data-testid="kubernetes-context" value={target.context} onChange={(e) => setForm({ ...form, context: e.target.value })}>
              {!target.context && <option value="">{t('contextPick')}</option>}
              {contexts.map((c) => <option key={c} value={c}>{c}</option>)}
            </Select>
          ) : (
            <TextInput {...p} mono autoComplete="off" spellCheck={false} data-testid="kubernetes-context" value={form.context || ''}
              required onChange={(e) => setForm({ ...form, context: e.target.value.trim() })} />
          ))}
        </Field>
      )}
      {(text !== '' || form.id) && (target.serverFound ? (
        <p className="cn-muted kube-server" data-testid="kubernetes-server-found">{t('serverFromKubeconfig', { server: target.server })}</p>
      ) : (
        <ServerField form={form} setForm={setForm} t={t} />
      ))}
    </>
  )
}

function ServerField({ form, setForm, t }) {
  const bad = form.server && !normalizeServer(form.server)
  return (
    <Field label={t('server')} hint={t('serverHelp')} error={bad ? t('serverShape') : undefined}>
      {(p) => (
        <TextInput {...p} mono autoComplete="off" spellCheck={false} inputMode="url" data-testid="kubernetes-server" value={form.server || ''}
          placeholder="https://cluster.example:6443" required onChange={(e) => setForm({ ...form, server: e.target.value.trim() })} />
      )}
    </Field>
  )
}

// A server, its CA and a bearer token; the page builds a one-context kubeconfig from them.
function TokenFieldsOf({ form, setForm, t }) {
  return (
    <>
      <ServerField form={form} setForm={setForm} t={t} />
      <Field label={t('ca')} hint={t('caHelp')} optional>
        {(p) => (
          <textarea {...p} className="cn-input cn-input--mono" rows={5} spellCheck={false} autoComplete="off" value={form.ca || ''}
            placeholder="-----BEGIN CERTIFICATE-----" data-testid="kubernetes-ca" onChange={(e) => setForm({ ...form, ca: e.target.value })} />
        )}
      </Field>
      <Field label={t('token')} hint={t('tokenHelp')}>
        {(p) => (
          <TextInput {...p} mono type="password" autoComplete="new-password" value={form.token || ''} required={!form.id}
            placeholder={form.id ? t('tokenSaved') : ''} data-testid="kubernetes-token" onChange={(e) => setForm({ ...form, token: e.target.value.trim() })} />
        )}
      </Field>
    </>
  )
}

// How the page logs in: a pasted kubeconfig, or a server, a CA and a token.
// Either way it is sealed as one credential, the kubeconfig text.
function KubeCredential({ form, setForm: set, t }) {
  const setForm = (next) => set(withName(form, next))
  const method = methodOf(form.method)
  return (
    <>
      <Field label={t('method.label')} hint={t('method.hint')}>
        {() => (
          <RadioCards name="kubernetes-method" legend={t('method.label')} value={method} testId="kubernetes-method" onChange={(m) => setForm({ ...form, method: m })}
            options={METHODS.map((m) => ({ value: m, label: t(`method.${m}`) }))} />
        )}
      </Field>
      {method === 'token' ? <TokenFieldsOf form={form} setForm={setForm} t={t} /> : <KubeconfigFields form={form} setForm={setForm} t={t} />}
    </>
  )
}

function WriteMode({ form, setForm, t }) {
  const mode = writeModeOf(form.write_mode)
  return (
    <div data-testid="kubernetes-write-mode" className="kube-mode-block">
      <Field label={t('writeMode.label')} hint={t('writeMode.hint')}>
        {() => (
          <RadioCards name="write-mode" legend={t('writeMode.label')} value={mode} onChange={(m) => setForm({ ...form, write_mode: m })}
            options={WRITE_MODES.map((m) => ({
              value: m,
              label: (<span className="kube-modecard"><span className="kube-modecard__title">{t(`writeMode.${m}`)}</span><span className="kube-modecard__line">{t(`writeMode.line.${m}`)}</span></span>),
            }))} />
        )}
      </Field>
      <Notice tone={mode === 'auto' ? 'error' : mode === 'off' ? 'info' : 'warn'} role="note" testId="kubernetes-write-note" className="kube-note">{t(`writeMode.note.${mode}`)}</Notice>
    </div>
  )
}

function Help({ t }) {
  const [open, setOpen] = useState(null)
  useEffect(() => {
    let live = true
    connectorsApi.list().then((r) => { if (live && open === null) setOpen(!(r.connectors || []).some((c) => c.kind === 'kubernetes')) }).catch(() => {})
    return () => { live = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- runs once; the person's own toggle wins afterwards
  }, [])
  return (
    <div className="kube-help">
      <Notice tone="warn" role="note" testId="kubernetes-limits-warning">{t('limits')}</Notice>
      <details className="cn-help" data-testid="kubernetes-setup" open={open === true} onToggle={(e) => setOpen(e.currentTarget.open)}>
        <summary>{t('setup.title')}</summary>
        <ol>
          <li>{t('setup.step1')}</li>
          <li>{t('setup.step2')}</li>
          <li>{t('setup.step3')}</li>
          <li>{t('setup.step4')}</li>
        </ol>
      </details>
    </div>
  )
}

function useResult(id) {
  const [, tick] = useState(0)
  useEffect(() => subscribe(() => tick((n) => n + 1)), [])
  return resultOf(id)
}

// The RBAC rules of the credential in one namespace, as the check read them.
function Rules({ item, t }) {
  return (
    <div className="kube-rbac" data-testid="kubernetes-rbac">
      <p className="kube-rbac__title">{t('check.rbac', { namespace: item.namespace })}</p>
      {item.rows.length === 0 ? <p className="cn-muted">{t('check.none')}</p> : (
        <table className="kube-rbac__table">
          <thead><tr><th scope="col">{t('check.verbs')}</th><th scope="col">{t('check.resources')}</th></tr></thead>
          <tbody>
            {item.rows.map((r, n) => <tr key={n}><td><code>{r.verbs}</code></td><td><code>{r.resources}</code></td></tr>)}
          </tbody>
        </table>
      )}
      {item.more > 0 && <p className="cn-muted">{t('check.more', { count: item.more })}</p>}
      {item.cut && <p className="cn-muted">{t('check.cut')}</p>}
      {item.incomplete && <p className="cn-muted" data-testid="kubernetes-rbac-incomplete">{t('check.incomplete')}</p>}
    </div>
  )
}

function Checks({ c, t }) {
  const r = useResult(c.id)
  if (!r) return null
  if (r.busy) return <p className="cn-muted" role="status" aria-busy="true" data-testid="kubernetes-checking">{t('check.running')}</p>
  const lines = r.items.filter((i) => i.tone !== 'rbac')
  const rules = r.items.find((i) => i.tone === 'rbac')
  return (
    <div data-testid="kubernetes-checks">
      <ul className="kube-checks" aria-label={t('check.title', { name: c.name })}>
        {lines.map((i, n) => (
          <li key={n} className={`kube-check kube-check--${i.tone}`}>
            <Icon name={i.tone === 'ok' ? 'check-circle' : 'warning'} />
            <span>{i.text}</span>
          </li>
        ))}
      </ul>
      {rules && <Rules item={rules} t={t} />}
      {r.at > 0 && <p className="cn-muted">{t('check.at', { time: new Date(r.at).toLocaleTimeString(pageLocale()) })}</p>}
    </div>
  )
}

function RowInfo({ c, t }) {
  const { server, context } = splitKey((c.resources || [])[0])
  const mode = writeModeOf(c.write_mode)
  return (
    <>
      {server ? (
        <>
          <span className="cn-muted kube-row" data-testid="kubernetes-row-server">{t('serverOf', { server })}</span>
          <span className="cn-muted kube-row" data-testid="kubernetes-row-context">{t('contextOf', { context })}</span>
        </>
      ) : <span className="cn-muted" data-testid="kubernetes-row-server">{t('noCluster')}</span>}
      <span><span className={`kube-chip kube-chip--${mode}`} data-testid="kubernetes-row-write">{t(`writeMode.row.${mode}`)}</span></span>
      <Checks c={c} t={t} />
    </>
  )
}

// The Test button, and the check after Save: the host checks that a
// credential exists; this asks the cluster itself, with it, for its version
// and the RBAC rules of the credential. The result goes to the row.
async function testExtra(c, t) {
  startTest(c.id)
  try {
    const all = await connectorsApi.list()
    if ((all.connectors || []).filter((x) => x.kind === 'kubernetes').length > 1) {
      finishTest(c.id, [])
      return t('checkSeveral')
    }
  } catch (_e) { /* the check below says what it can */ }
  const cluster = (c.resources || [])[0]
  let r
  try {
    const res = await getSdk().api.read({ arguments: { tool: 'check', cluster } })
    r = { ok: true, ...(res?.data || {}) }
  } catch (e) {
    r = { failed: checkFailure(e) }
  }
  const i = checkItem(r)
  const items = [{ tone: i.tone, text: t(i.key, i.vars) }]
  if (r.ok && r.check) {
    const { rows, more } = rbacRows(r.check.rules)
    items.push({ tone: 'rbac', text: '', namespace: String(r.check.namespace || 'default'), rows, more, cut: r.check.rulesCut === true, incomplete: r.check.incomplete === true })
  }
  finishTest(c.id, items)
  return ''
}

export function createKubernetes() {
  function Component({ page, path, search }) {
    if (page !== 'kubernetes') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <ConnectorPage kind="kubernetes" ns="kubernetes" testId="kubernetes-page" TokenFields={KubeCredential} tokenOf={kubeSecret} messaging={false}
          ExtraFields={WriteMode} extraBody={extraBody} extraForm={extraForm} extraValid={extraValid} testExtra={testExtra}
          Help={Help} RowInfo={RowInfo} />
        <ApprovalSettings />
        <PolicyEditor />
      </RouteProvider>
    )
  }
  return { Component, components: { ProposalCard }, chatActions: [], dispose() {} }
}
