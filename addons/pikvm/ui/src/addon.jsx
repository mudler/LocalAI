/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { RouteProvider } from 'react-router-dom'
import { getSdk } from '../../../internal/host-compat/src/runtime.js'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import Icon from '../../../internal/host-compat/src/components/Icon'
import ConnectorPage from '../../../internal/connector-ui/src/ConnectorPage'
import ApprovalsView from '../../../internal/connector-ui/src/ApprovalsView'
import { answerT } from '../../../internal/connector-ui/src/AnswerFields'
import { connectorsApi } from '../../../internal/connector-ui/src/api'
import { Field, Notice, RadioCards, TextInput } from '../../../internal/connector-ui/src/controls'
import { pageLocale } from '../../../internal/connector-ui/src/connector'
import en from './i18n/pikvm.en.json'
import it from './i18n/pikvm.it.json'
import es from './i18n/pikvm.es.json'
import de from './i18n/pikvm.de.json'
import fr from './i18n/pikvm.fr.json'
import zh from './i18n/pikvm.zh-CN.json'
import id from './i18n/pikvm.id.json'
import ko from './i18n/pikvm.ko.json'
import pt from './i18n/pikvm.pt-BR.json'
import SettingsEditor from './SettingsEditor'
import ApprovalSettings from './ApprovalSettings'
import { approvalRevision, onApprovalsChanged } from './approval'
import ProposalCard from './ProposalCard'
import { addressProblem, checkFailure, checkItem, credentialProblem, extraBody, extraForm, extraValid, followName, needsPinHint, pikvmSecret, WRITE_MODES, writeModeOf } from './pikvm'
import { finishTest, resultOf, startTest, subscribe } from './results'
import './pikvm.css'

// The words of the page, in every language of the web UI; a key a language lacks reads in English.
registerLocales({ pikvm: { en, it, es, de, fr, 'zh-CN': zh, id, ko, 'pt-BR': pt } })

// The user and the password of the device. They are typed only to replace the
// saved pair, and the pair is sealed as one credential.
function PikvmCredential({ form, setForm, t }) {
  const problem = credentialProblem(form)
  return (
    <>
      <Field label={t('user')} hint={t('userHelp')} error={problem === 'userMissing' || problem === 'userColon' ? t(problem) : undefined}>
        {(p) => (
          <TextInput {...p} mono autoComplete="off" spellCheck={false} value={form.user || ''} required={!form.id}
            placeholder={form.id ? t('credSaved') : ''} data-testid="pikvm-user" onChange={(e) => setForm({ ...form, user: e.target.value })} />
        )}
      </Field>
      <Field label={t('passwd')} hint={t('passwdHelp')} error={problem && problem !== 'userMissing' && problem !== 'userColon' ? t(problem) : undefined}>
        {(p) => (
          <TextInput {...p} mono type="password" autoComplete="new-password" value={form.passwd || ''} required={!form.id}
            placeholder={form.id ? t('credSaved') : ''} data-testid="pikvm-passwd" onChange={(e) => setForm({ ...form, passwd: e.target.value })} />
        )}
      </Field>
    </>
  )
}

// The address of the device, and the write mode. The host refuses any other
// address before the add-on is called, so one connection reaches one device.
function PikvmFields({ form, setForm, t }) {
  const problem = addressProblem(form.address_text)
  const change = (text) => setForm({ ...form, address_text: text, name: followName(form.name, form.address_text, text) })
  return (
    <>
      <Field label={t('address')} hint={t('addressHelp')} error={problem ? t(problem) : undefined}>
        {(p) => (
          <TextInput {...p} mono type="url" inputMode="url" autoComplete="off" spellCheck={false} data-testid="pikvm-address"
            value={form.address_text || ''} placeholder="https://pikvm.local#sha256=..." required onChange={(e) => change(e.target.value)} />
        )}
      </Field>
      {!problem && needsPinHint(form.address_text) && <Notice tone="info" role="note" testId="pikvm-pin-hint">{t('pinHint')}</Notice>}
      <WriteMode form={form} setForm={setForm} t={t} />
    </>
  )
}

function WriteMode({ form, setForm, t }) {
  const mode = writeModeOf(form.write_mode)
  return (
    <div data-testid="pikvm-write-mode" className="pk-mode-block">
      <Field label={t('writeMode.label')} hint={t('writeMode.hint')}>
        {() => (
          <RadioCards name="write-mode" legend={t('writeMode.label')} value={mode} onChange={(m) => setForm({ ...form, write_mode: m })}
            options={WRITE_MODES.map((m) => ({
              value: m,
              label: (<span className="pk-modecard"><span className="pk-modecard__title">{t(`writeMode.${m}`)}</span><span className="pk-modecard__line">{t(`writeMode.line.${m}`)}</span></span>),
            }))} />
        )}
      </Field>
      <Notice tone={mode === 'off' ? 'info' : 'warn'} role="note" testId="pikvm-write-note" className="pk-note">{t(`writeMode.note.${mode}`)}</Notice>
    </div>
  )
}

function Help({ t }) {
  const [open, setOpen] = useState(null)
  useEffect(() => {
    let live = true
    connectorsApi.list().then((r) => { if (live && open === null) setOpen(!(r.connectors || []).some((c) => c.kind === 'pikvm')) }).catch(() => {})
    return () => { live = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- runs once; the person's own toggle wins afterwards
  }, [])
  return (
    <div className="pk-help">
      <Notice tone="warn" role="note" testId="pikvm-limits-warning">{t('limits')}</Notice>
      <details className="cn-help" data-testid="pikvm-setup" open={open === true} onToggle={(e) => setOpen(e.currentTarget.open)}>
        <summary>{t('setup.title')}</summary>
        <ol>
          <li>{t('setup.step1')}</li>
          <li>{t('setup.step2')}</li>
          <li>{t('setup.step3')}</li>
          <li>{t('setup.step4')}</li>
        </ol>
        <p>{t('setup.tls')}</p>
        <p>{t('setup.twofa')}</p>
      </details>
    </div>
  )
}

function useResult(id) {
  const [, tick] = useState(0)
  useEffect(() => subscribe(() => tick((n) => n + 1)), [])
  return resultOf(id)
}

function Checks({ c, t }) {
  const r = useResult(c.id)
  if (!r) return null
  if (r.busy) return <p className="cn-muted" role="status" aria-busy="true" data-testid="pikvm-checking">{t('check.running')}</p>
  return (
    <div data-testid="pikvm-checks">
      <ul className="pk-checks" aria-label={t('check.title', { name: c.name })}>
        {r.items.map((i, n) => (
          <li key={n} className={`pk-check pk-check--${i.tone}`}>
            <Icon name={i.tone === 'ok' ? 'check-circle' : 'warning'} />
            <span>{i.text}</span>
          </li>
        ))}
      </ul>
      {r.at > 0 && <p className="cn-muted">{t('check.at', { time: new Date(r.at).toLocaleTimeString(pageLocale()) })}</p>}
    </div>
  )
}

// The effective view of a connection: which of its tools run without asking.
// The host reports it; the choice is made in the settings below.
function useApprovals(id, version) {
  const [approvals, setApprovals] = useState([])
  const [seen, setSeen] = useState(approvalRevision())
  useEffect(() => onApprovalsChanged(() => setSeen(approvalRevision())), [])
  useEffect(() => {
    let live = true
    connectorsApi.access(id).then((v) => { if (live) setApprovals(v?.approvals || []) }).catch(() => { if (live) setApprovals([]) })
    return () => { live = false }
  }, [id, version, seen])
  return approvals
}

function RowInfo({ c, t }) {
  const approvals = useApprovals(c.id, c.write_mode)
  const address = (c.resources || [])[0]
  const shown = Boolean(resultOf(c.id)) && Boolean(address)
  const mode = writeModeOf(c.write_mode)
  return (
    <>
      {!shown && <span className="cn-muted" data-testid="pikvm-row-address">{address ? t('addressOf', { address: address.split('#')[0] }) : t('noAddress')}</span>}
      <span><span className={`pk-chip pk-chip--${mode}`} data-testid="pikvm-row-write">{t(`writeMode.row.${mode}`)}</span></span>
      <ApprovalsView approvals={approvals} s={answerT(t)} testId="pikvm-row-approvals" />
      <Checks c={c} t={t} />
    </>
  )
}

// The Test button: the host checks that a credential exists; this asks the
// device itself, with it, for its identity. The result goes to the row.
async function testExtra(c, t) {
  startTest(c.id)
  try {
    const all = await connectorsApi.list()
    if ((all.connectors || []).filter((x) => x.kind === 'pikvm').length > 1) {
      finishTest(c.id, [])
      return t('checkSeveral')
    }
  } catch (_e) { /* the check below says what it can */ }
  const address = (c.resources || [])[0]
  let r
  try {
    const res = await getSdk().api.read({ arguments: { tool: 'get-info', device: address } })
    r = { ok: true, ...(res?.data || {}) }
  } catch (e) {
    r = { failed: checkFailure(e) }
  }
  const i = checkItem(r)
  finishTest(c.id, [{ tone: i.tone, text: t(i.key, i.vars) }])
  return ''
}

export function createPikvm() {
  function Component({ page, path, search }) {
    if (page !== 'apps') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <ConnectorPage kind="pikvm" ns="pikvm" testId="pikvm-page" TokenFields={PikvmCredential} tokenOf={pikvmSecret} messaging={false}
          ExtraFields={PikvmFields} extraBody={extraBody} extraForm={extraForm} extraValid={extraValid} testExtra={testExtra}
          Help={Help} RowInfo={RowInfo} />
        <ApprovalSettings />
        <SettingsEditor />
      </RouteProvider>
    )
  }
  return { Component, components: { ProposalCard }, chatActions: [], dispose() {} }
}
