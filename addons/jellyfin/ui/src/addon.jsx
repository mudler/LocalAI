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
import en from './i18n/jellyfin.en.json'
import it from './i18n/jellyfin.it.json'
import es from './i18n/jellyfin.es.json'
import de from './i18n/jellyfin.de.json'
import fr from './i18n/jellyfin.fr.json'
import zh from './i18n/jellyfin.zh-CN.json'
import id from './i18n/jellyfin.id.json'
import ko from './i18n/jellyfin.ko.json'
import pt from './i18n/jellyfin.pt-BR.json'
import ProposalCard from './ProposalCard'
import { checkFailure, checkItem, composeServerKey, extraBody, extraForm, extraValid, followName, isPlainHttp, jellyfinSecret, serverIssue, tokenProblem, WRITE_MODES, writeModeOf } from './server'
import { finishTest, resultOf, startTest, subscribe } from './results'
import './jellyfin.css'

// The words of the page, in every language of the web UI; a key a language lacks reads in English.
registerLocales({ jellyfin: { en, it, es, de, fr, 'zh-CN': zh, id, ko, 'pt-BR': pt } })

// The API key. It is typed only to replace it.
function JellyfinToken({ form, setForm, t }) {
  const problem = tokenProblem(form)
  return (
    <Field label={t('token')} hint={t('tokenHelp')} error={problem ? t(problem) : undefined}>
      {(p) => (
        <TextInput {...p} mono type="password" autoComplete="off" value={form.token || ''} required={!form.id}
          placeholder={form.id ? t('tokenSaved') : ''}
          onChange={(e) => setForm({ ...form, token: e.target.value })} />
      )}
    </Field>
  )
}

// The server of this connection and, for a self-signed certificate, the
// fingerprint to accept. The host refuses any other address before the
// add-on is called.
function JellyfinFields({ form, setForm, t }) {
  const issue = serverIssue(form.server_text, form.pin_text)
  const pinIssue = issue === 'pinHttp' || issue === 'pinShape'
  const change = (text) => setForm({ ...form, server_text: text, name: followName(form.name, form.server_text, text) })
  return (
    <>
      <Field label={t('server')} hint={t('serverHelp')} error={issue && !pinIssue ? t(issue) : undefined}>
        {(p) => (
          <TextInput {...p} mono type="url" autoComplete="off" data-testid="jellyfin-server" value={form.server_text || ''}
            placeholder="http://192.168.1.10:8096" required onChange={(e) => change(e.target.value)} />
        )}
      </Field>
      {isPlainHttp(form.server_text) && <Notice tone="warn" role="note" testId="jellyfin-http-note" className="jf-note">{t('httpNote')}</Notice>}
      <Field label={t('pin')} hint={t('pinHelp')} error={pinIssue ? t(issue) : undefined}>
        {(p) => (
          <TextInput {...p} mono autoComplete="off" data-testid="jellyfin-pin" value={form.pin_text || ''}
            placeholder="AB:CD:..." onChange={(e) => setForm({ ...form, pin_text: e.target.value })} />
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
    <div data-testid="jellyfin-write-mode" className="jf-mode-block">
      <Field label={t('writeMode.label')} hint={t('writeMode.hint')}>
        {() => (
          <RadioCards name="write-mode" legend={t('writeMode.label')} value={mode} onChange={(m) => setForm({ ...form, write_mode: m })}
            options={WRITE_MODES.map((m) => ({
              value: m,
              label: (<span className="jf-modecard"><span className="jf-modecard__title">{t(`writeMode.${m}`)}</span><span className="jf-modecard__line">{t(`writeMode.line.${m}`)}</span></span>),
            }))} />
        )}
      </Field>
      <Notice tone={mode === 'off' ? 'info' : 'warn'} role="note" testId="jellyfin-write-note" className="jf-note">{t(`writeMode.note.${mode}`)}</Notice>
    </div>
  )
}

// What the page does not do, always shown; and how to make the key, open
// while there is no connection yet.
function Help({ t }) {
  const [open, setOpen] = useState(null)
  useEffect(() => {
    let live = true
    connectorsApi.list().then((r) => { if (live && open === null) setOpen(!(r.connectors || []).some((c) => c.kind === 'jellyfin')) }).catch(() => {})
    return () => { live = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- runs once; the person's own toggle wins afterwards
  }, [])
  return (
    <div className="jf-help">
      <p className="cn-muted" data-testid="jellyfin-limits">{t('limits')}</p>
      <details className="cn-help" data-testid="jellyfin-setup" open={open === true} onToggle={(e) => setOpen(e.currentTarget.open)}>
        <summary>{t('setup.title')}</summary>
        <ol>
          <li>{t('setup.step1')}</li>
          <li>{t('setup.step2')}</li>
          <li>{t('setup.step3')}</li>
          <li>{t('setup.step4')}</li>
        </ol>
        <p className="cn-muted">{t('setup.admin')}</p>
      </details>
    </div>
  )
}

function useResult(id) {
  const [, tick] = useState(0)
  useEffect(() => subscribe(() => tick((n) => n + 1)), [])
  return resultOf(id)
}

// The Test result of one connection, drawn in its row with a tone.
function Checks({ c, t }) {
  const r = useResult(c.id)
  if (!r) return null
  if (r.busy) return <p className="cn-muted" role="status" aria-busy="true" data-testid="jellyfin-checking">{t('check.running')}</p>
  return (
    <div data-testid="jellyfin-checks">
      <ul className="jf-checks" aria-label={t('check.title', { name: c.name })}>
        {r.items.map((i) => (
          <li key={i.server} className={`jf-check jf-check--${i.tone}`}>
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
  const servers = c.resources || []
  const shown = Boolean(resultOf(c.id)) && servers.length > 0
  const mode = writeModeOf(c.write_mode)
  return (
    <>
      {!shown && <span className="cn-muted" data-testid="jellyfin-row-server">{servers.length === 0 ? t('noServer') : t('serverOf', { server: servers[0].split('#')[0], pinned: servers[0].includes('#') ? t('pinnedMark') : '' })}</span>}
      <span><span className={`jf-chip jf-chip--${mode}`} data-testid="jellyfin-row-write">{t(`writeMode.row.${mode}`)}</span></span>
      <Checks c={c} t={t} />
    </>
  )
}

// The Test button: the host checks the key shape; this asks the server. The
// result goes to the row, so the text it returns for the top notice is empty.
async function testExtra(c, t) {
  startTest(c.id)
  // The check runs from this page, not from a chat, so the host has no chat
  // to read a choice from and uses the connection only when there is exactly
  // one. With several, it would be a guess, and the host does not guess.
  try {
    const all = await connectorsApi.list()
    if ((all.connectors || []).filter((x) => x.kind === 'jellyfin').length > 1) {
      finishTest(c.id, [])
      return t('checkSeveral')
    }
  } catch (_e) { /* the check below says what it can */ }
  const items = []
  for (const server of c.resources || []) {
    let r
    try {
      const res = await getSdk().api.read({ arguments: { tool: 'check', server } })
      r = res?.data ? { ...res.data } : { reachable: false }
    } catch (e) {
      r = { failed: checkFailure(e) }
    }
    const i = checkItem(r)
    items.push({ server, tone: i.tone, text: t(i.key, i.vars) })
  }
  finishTest(c.id, items)
  return ''
}

export function createJellyfin() {
  function Component({ page, path, search }) {
    if (page !== 'jellyfin') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <ConnectorPage kind="jellyfin" ns="jellyfin" testId="jellyfin-page" TokenFields={JellyfinToken} tokenOf={jellyfinSecret} messaging={false}
          ExtraFields={JellyfinFields} extraBody={extraBody} extraForm={extraForm} extraValid={extraValid} testExtra={testExtra}
          Help={Help} RowInfo={RowInfo} />
      </RouteProvider>
    )
  }
  return { Component, components: { ProposalCard }, chatActions: [], dispose() {} }
}
