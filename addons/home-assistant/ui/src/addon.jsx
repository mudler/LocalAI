/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { RouteProvider } from 'react-router-dom'
import { getSdk } from '../../../internal/host-compat/src/runtime.js'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import Icon from '../../../internal/host-compat/src/components/Icon'
import ConnectorPage from '../../../internal/connector-ui/src/ConnectorPage'
import { pageLocale } from '../../../internal/connector-ui/src/connector'
import { connectorsApi } from '../../../internal/connector-ui/src/api'
import { Field, Notice, RadioCards, TextInput } from '../../../internal/connector-ui/src/controls'
import en from './i18n/home-assistant.en.json'
import it from './i18n/home-assistant.it.json'
import es from './i18n/home-assistant.es.json'
import de from './i18n/home-assistant.de.json'
import fr from './i18n/home-assistant.fr.json'
import zh from './i18n/home-assistant.zh-CN.json'
import id from './i18n/home-assistant.id.json'
import ko from './i18n/home-assistant.ko.json'
import pt from './i18n/home-assistant.pt-BR.json'
import PolicyEditor from './PolicyEditor'
import ProposalCard from './ProposalCard'
import { addressProblem, checkFailure, checkItem, extraBody, extraForm, extraValid, followName, haSecret, parseAddress, tokenProblem, WRITE_MODES, writeModeOf } from './ha'
import { finishTest, resultOf, startTest, subscribe } from './results'
import './home-assistant.css'

// The words of the page, in every language of the web UI; a key a language lacks reads in English.
registerLocales({ 'home-assistant': { en, it, es, de, fr, 'zh-CN': zh, id, ko, 'pt-BR': pt } })

// The long-lived access token. It is typed only to replace it.
function HaToken({ form, setForm, t }) {
  const problem = tokenProblem(form)
  return (
    <Field label={t('token')} hint={t('tokenHelp')} error={problem ? t(problem) : undefined}>
      {(p) => (
        <TextInput {...p} mono type="password" autoComplete="off" value={form.token || ''} required={!form.id}
          placeholder={form.id ? t('tokenSaved') : ''} data-testid="home-assistant-token"
          onChange={(e) => setForm({ ...form, token: e.target.value })} />
      )}
    </Field>
  )
}

// The address of the instance, and the write mode. The host refuses any other
// address before the add-on is called, so one connection reaches one home.
function HaFields({ form, setForm, t }) {
  const problem = addressProblem(form.address_text)
  const change = (text) => setForm({ ...form, address_text: text, name: followName(form.name, form.address_text, text) })
  return (
    <>
      <Field label={t('address')} hint={t('addressHelp')} error={problem ? t(problem) : undefined}>
        {(p) => (
          <TextInput {...p} mono type="url" inputMode="url" autoComplete="off" spellCheck={false} data-testid="home-assistant-address"
            value={form.address_text || ''} placeholder="https://homeassistant.local:8123" required onChange={(e) => change(e.target.value)} />
        )}
      </Field>
      <WriteMode form={form} setForm={setForm} t={t} />
    </>
  )
}

// The write mode: the host enforces it for every write tool of this add-on.
function WriteMode({ form, setForm, t }) {
  const mode = writeModeOf(form.write_mode)
  return (
    <div data-testid="home-assistant-write-mode" className="ha-mode-block">
      <Field label={t('writeMode.label')} hint={t('writeMode.hint')}>
        {() => (
          <RadioCards name="write-mode" legend={t('writeMode.label')} value={mode} onChange={(m) => setForm({ ...form, write_mode: m })}
            options={WRITE_MODES.map((m) => ({
              value: m,
              label: (<span className="ha-modecard"><span className="ha-modecard__title">{t(`writeMode.${m}`)}</span><span className="ha-modecard__line">{t(`writeMode.line.${m}`)}</span></span>),
            }))} />
        )}
      </Field>
      <Notice tone={mode === 'off' ? 'info' : 'warn'} role="note" testId="home-assistant-write-note" className="ha-note">{t(`writeMode.note.${mode}`)}</Notice>
    </div>
  )
}

// What the page does not do, always shown; and how to make the token, open
// while there is no connection yet.
function Help({ t }) {
  const [open, setOpen] = useState(null)
  useEffect(() => {
    let live = true
    connectorsApi.list().then((r) => { if (live && open === null) setOpen(!(r.connectors || []).some((c) => c.kind === 'home-assistant')) }).catch(() => {})
    return () => { live = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- runs once; the person's own toggle wins afterwards
  }, [])
  return (
    <div className="ha-help">
      <p className="cn-muted" data-testid="home-assistant-limits">{t('limits')}</p>
      <details className="cn-help" data-testid="home-assistant-setup" open={open === true} onToggle={(e) => setOpen(e.currentTarget.open)}>
        <summary>{t('setup.title')}</summary>
        <ol>
          <li>{t('setup.step1')}</li>
          <li>{t('setup.step2')}</li>
          <li>{t('setup.step3')}</li>
          <li>{t('setup.step4')}</li>
        </ol>
        <p>{t('setup.tls')}</p>
      </details>
    </div>
  )
}

function useResult(id) {
  const [, tick] = useState(0)
  useEffect(() => subscribe(() => tick((n) => n + 1)), [])
  return resultOf(id)
}

// The Test result of one connection, drawn in its row, with a tone.
function Checks({ c, t }) {
  const r = useResult(c.id)
  if (!r) return null
  if (r.busy) return <p className="cn-muted" role="status" aria-busy="true" data-testid="home-assistant-checking">{t('check.running')}</p>
  return (
    <div data-testid="home-assistant-checks">
      <ul className="ha-checks" aria-label={t('check.title', { name: c.name })}>
        {r.items.map((i, n) => (
          <li key={n} className={`ha-check ha-check--${i.tone}`}>
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
      {!shown && <span className="cn-muted" data-testid="home-assistant-row-address">{address ? t('addressOf', { address }) : t('noAddress')}</span>}
      <span><span className={`ha-chip ha-chip--${mode}`} data-testid="home-assistant-row-write">{t(`writeMode.row.${mode}`)}</span></span>
      <Checks c={c} t={t} />
    </>
  )
}

// The Test button: the host checks that a token exists; this asks the instance
// itself, with the token, for its configuration summary. The result goes to the
// row, so the text it returns for the top notice is empty.
async function testExtra(c, t) {
  startTest(c.id)
  // The check runs from this page, not from a chat, so the host has no chat
  // to read a choice from and uses the connection only when there is exactly
  // one. With several, it would be a guess, and the host does not guess.
  try {
    const all = await connectorsApi.list()
    if ((all.connectors || []).filter((x) => x.kind === 'home-assistant').length > 1) {
      finishTest(c.id, [])
      return t('checkSeveral')
    }
  } catch (_e) { /* the check below says what it can */ }
  const address = (c.resources || [])[0]
  let r
  try {
    const res = await getSdk().api.read({ arguments: { tool: 'get-config', instance: address } })
    r = res?.data || { ok: false }
  } catch (e) {
    r = { failed: checkFailure(e) }
  }
  const i = checkItem(r)
  finishTest(c.id, [{ tone: i.tone, text: t(i.key, i.vars) }])
  return ''
}

export function createHomeAssistant() {
  function Component({ page, path, search }) {
    if (page !== 'apps') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <ConnectorPage kind="home-assistant" ns="home-assistant" testId="home-assistant-page" TokenFields={HaToken} tokenOf={haSecret} messaging={false}
          ExtraFields={HaFields} extraBody={extraBody} extraForm={extraForm} extraValid={extraValid} testExtra={testExtra}
          Help={Help} RowInfo={RowInfo} />
        <PolicyEditor />
      </RouteProvider>
    )
  }
  return { Component, components: { ProposalCard }, chatActions: [], dispose() {} }
}
