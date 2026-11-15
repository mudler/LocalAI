/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { RouteProvider } from 'react-router-dom'
import { getSdk } from '../../../internal/host-compat/src/runtime.js'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import Icon from '../../../internal/host-compat/src/components/Icon'
import ConnectorPage from '../../../internal/connector-ui/src/ConnectorPage'
import { connectorsApi } from '../../../internal/connector-ui/src/api'
import { Check, Field, Notice, RadioCards, TextInput } from '../../../internal/connector-ui/src/controls'
import { pageLocale } from '../../../internal/connector-ui/src/connector'
import en from './i18n/ssh.en.json'
import it from './i18n/ssh.it.json'
import es from './i18n/ssh.es.json'
import de from './i18n/ssh.de.json'
import fr from './i18n/ssh.fr.json'
import zh from './i18n/ssh.zh-CN.json'
import id from './i18n/ssh.id.json'
import ko from './i18n/ssh.ko.json'
import pt from './i18n/ssh.pt-BR.json'
import ProposalCard from './ProposalCard'
import { checkFailure, checkItem, credentialProblem, extraBody, extraForm, extraValid, fingerprintOfPin, followName, hostProblem, METHODS, methodOf, needsSecretAgain, normalizePin, sshSecret, targetKey, userProblem, WRITE_MODES, writeModeOf, addressOf } from './ssh'
import { finishTest, resultOf, startTest, subscribe } from './results'
import './ssh.css'

// The words of the page, in every language of the web UI; a key a language lacks reads in English.
registerLocales({ ssh: { en, it, es, de, fr, 'zh-CN': zh, id, ko, 'pt-BR': pt } })

// How the page logs in: a password, or a private key. The secret is typed only
// to replace the saved one, and it is sealed as one credential.
function SshCredential({ form, setForm, t }) {
  const method = methodOf(form.method)
  const problem = credentialProblem(form)
  const again = needsSecretAgain(form)
  return (
    <>
      <Field label={t('login.label')} hint={t('login.hint')}>
        {() => (
          <RadioCards name="ssh-login" legend={t('login.label')} value={method} testId="ssh-login" onChange={(m) => setForm({ ...form, method: m })}
            options={METHODS.map((m) => ({ value: m, label: t(`login.${m}`) }))} />
        )}
      </Field>
      {method === 'password' ? (
        <Field label={t('passwd')} hint={t('passwdHelp')} error={problem ? t(problem) : again ? t('secretAgain') : undefined}>
          {(p) => (
            <TextInput {...p} mono type="password" autoComplete="new-password" value={form.passwd || ''} required={!form.id}
              placeholder={form.id ? t('credSaved') : ''} data-testid="ssh-passwd" onChange={(e) => setForm({ ...form, passwd: e.target.value })} />
          )}
        </Field>
      ) : (
        <Field label={t('key')} hint={t('keyHelp')} error={problem ? t(problem) : again ? t('secretAgain') : undefined}>
          {(p) => (
            <textarea {...p} className="cn-input cn-input--mono" rows={6} spellCheck={false} autoComplete="off" value={form.key || ''} required={!form.id}
              placeholder={form.id ? t('credSaved') : '-----BEGIN OPENSSH PRIVATE KEY-----'} data-testid="ssh-key" onChange={(e) => setForm({ ...form, key: e.target.value })} />
          )}
        </Field>
      )}
    </>
  )
}

// Reads the host key of the address and shows it. Nothing secret is sent: the
// check stops at the key. The person compares the fingerprint with the one the
// machine shows and trusts it; the connection then accepts only that key.
function HostKey({ form, setForm, t }) {
  const [scan, setScan] = useState({ busy: false, error: '' })
  const address = addressOf(form.user, form.host_text, '')
  const pin = normalizePin(form.pin_text)
  const run = async () => {
    setScan({ busy: true, error: '' })
    try {
      const res = await getSdk().api.read({ arguments: { tool: 'scan-host-key', address } })
      const got = normalizePin(res?.data?.pin)
      if (!got) throw new Error('no key')
      setForm({ ...form, pin_text: got, pin_ok: false, pin_type: String(res?.data?.keyType || '') })
      setScan({ busy: false, error: '' })
    } catch (e) {
      setScan({ busy: false, error: checkFailure(e) === 'unreachable' ? 'hostKey.unreachable' : 'hostKey.failed' })
    }
  }
  return (
    <div className="ssh-hostkey" data-testid="ssh-hostkey">
      <Field label={t('hostKey.label')} hint={t('hostKey.hint')} error={scan.error ? t(scan.error) : undefined}>
        {() => (
          <div className="ssh-hostkey__actions">
            <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={!address || scan.busy} aria-busy={scan.busy ? 'true' : undefined}
              data-testid="ssh-hostkey-check" onClick={run}>{scan.busy ? t('hostKey.checking') : t('hostKey.check')}</button>
          </div>
        )}
      </Field>
      {pin && (
        <>
          <code className="ssh-hostkey__print" data-testid="ssh-hostkey-print">{form.pin_type ? `${form.pin_type} ` : ''}{fingerprintOfPin(pin)}</code>
          <Check checked={Boolean(form.pin_ok)} onChange={(v) => setForm({ ...form, pin_ok: v })} testId="ssh-hostkey-trust">{t('hostKey.trust')}</Check>
        </>
      )}
      {!pin && <Notice tone="info" role="note" testId="ssh-hostkey-needed">{t('hostKey.needed')}</Notice>}
    </div>
  )
}

// The user and the host of the connection, its host key and the write mode.
// The host refuses any other address before the add-on is called, so one
// connection reaches one machine.
function SshFields({ form, setForm, t }) {
  const userIssue = userProblem(form.user)
  const hostIssue = hostProblem(form.host_text)
  // A new user or host is a new machine: the key must be checked again.
  const change = (patch) => setForm({
    ...form, ...patch, pin_text: '', pin_ok: false, pin_type: '',
    name: followName(form.name, form.user, form.host_text, patch.user ?? form.user, patch.host_text ?? form.host_text),
  })
  return (
    <>
      <Field label={t('host')} hint={t('hostHelp')} error={hostIssue ? t(hostIssue) : undefined}>
        {(p) => (
          <TextInput {...p} mono autoComplete="off" spellCheck={false} data-testid="ssh-host" value={form.host_text || ''}
            placeholder="server.example.com:22" required onChange={(e) => change({ host_text: e.target.value })} />
        )}
      </Field>
      <Field label={t('user')} hint={t('userHelp')} error={userIssue ? t(userIssue) : undefined}>
        {(p) => (
          <TextInput {...p} mono autoComplete="off" spellCheck={false} data-testid="ssh-user" value={form.user || ''}
            required onChange={(e) => change({ user: e.target.value })} />
        )}
      </Field>
      <HostKey form={form} setForm={setForm} t={t} />
      <WriteMode form={form} setForm={setForm} t={t} />
    </>
  )
}

function WriteMode({ form, setForm, t }) {
  const mode = writeModeOf(form.write_mode)
  return (
    <div data-testid="ssh-write-mode" className="ssh-mode-block">
      <Field label={t('writeMode.label')} hint={t('writeMode.hint')}>
        {() => (
          <RadioCards name="write-mode" legend={t('writeMode.label')} value={mode} onChange={(m) => setForm({ ...form, write_mode: m })}
            options={WRITE_MODES.map((m) => ({
              value: m,
              label: (<span className="ssh-modecard"><span className="ssh-modecard__title">{t(`writeMode.${m}`)}</span><span className="ssh-modecard__line">{t(`writeMode.line.${m}`)}</span></span>),
            }))} />
        )}
      </Field>
      <Notice tone={mode === 'auto' ? 'error' : mode === 'off' ? 'info' : 'warn'} role="note" testId="ssh-write-note" className="ssh-note">{t(`writeMode.note.${mode}`)}</Notice>
    </div>
  )
}

function Help({ t }) {
  const [open, setOpen] = useState(null)
  useEffect(() => {
    let live = true
    connectorsApi.list().then((r) => { if (live && open === null) setOpen(!(r.connectors || []).some((c) => c.kind === 'ssh')) }).catch(() => {})
    return () => { live = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- runs once; the person's own toggle wins afterwards
  }, [])
  return (
    <div className="ssh-help">
      <Notice tone="warn" role="note" testId="ssh-limits-warning">{t('limits')}</Notice>
      <details className="cn-help" data-testid="ssh-setup" open={open === true} onToggle={(e) => setOpen(e.currentTarget.open)}>
        <summary>{t('setup.title')}</summary>
        <ol>
          <li>{t('setup.step1')}</li>
          <li>{t('setup.step2')}</li>
          <li>{t('setup.step3')}</li>
          <li>{t('setup.step4')}</li>
        </ol>
        <p>{t('setup.fingerprint')}</p>
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
  if (r.busy) return <p className="cn-muted" role="status" aria-busy="true" data-testid="ssh-checking">{t('check.running')}</p>
  return (
    <div data-testid="ssh-checks">
      <ul className="ssh-checks" aria-label={t('check.title', { name: c.name })}>
        {r.items.map((i, n) => (
          <li key={n} className={`ssh-check ssh-check--${i.tone}`}>
            <Icon name={i.tone === 'ok' ? 'check-circle' : 'warning'} />
            <span>{i.text}</span>
          </li>
        ))}
      </ul>
      {r.at > 0 && <p className="cn-muted">{t('check.at', { time: new Date(r.at).toLocaleTimeString(pageLocale()) })}</p>}
    </div>
  )
}

function RowInfo({ c, t }) {
  const address = (c.resources || [])[0]
  const shown = Boolean(resultOf(c.id)) && Boolean(address)
  const mode = writeModeOf(c.write_mode)
  return (
    <>
      {!shown && <span className="cn-muted" data-testid="ssh-row-host">{address ? t('hostOf', { host: targetKey(address) }) : t('noHost')}</span>}
      <span><span className={`ssh-chip ssh-chip--${mode}`} data-testid="ssh-row-write">{t(`writeMode.row.${mode}`)}</span></span>
      <Checks c={c} t={t} />
    </>
  )
}

// The Test button: the host checks that a credential exists; this asks the
// machine itself, with it, for its name and user. The result goes to the row.
async function testExtra(c, t) {
  startTest(c.id)
  try {
    const all = await connectorsApi.list()
    if ((all.connectors || []).filter((x) => x.kind === 'ssh').length > 1) {
      finishTest(c.id, [])
      return t('checkSeveral')
    }
  } catch (_e) { /* the check below says what it can */ }
  const address = (c.resources || [])[0]
  let r
  try {
    const res = await getSdk().api.read({ arguments: { tool: 'get-info', host: address } })
    r = { ok: true, ...(res?.data || {}) }
  } catch (e) {
    r = { failed: checkFailure(e) }
  }
  const i = checkItem(r)
  finishTest(c.id, [{ tone: i.tone, text: t(i.key, i.vars) }])
  return ''
}

export function createSsh() {
  function Component({ page, path, search }) {
    if (page !== 'ssh') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <ConnectorPage kind="ssh" ns="ssh" testId="ssh-page" TokenFields={SshCredential} tokenOf={sshSecret} messaging={false}
          ExtraFields={SshFields} extraBody={extraBody} extraForm={extraForm} extraValid={extraValid} testExtra={testExtra}
          Help={Help} RowInfo={RowInfo} />
      </RouteProvider>
    )
  }
  return { Component, components: { ProposalCard }, chatActions: [], dispose() {} }
}
