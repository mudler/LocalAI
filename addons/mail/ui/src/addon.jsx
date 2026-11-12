/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { RouteProvider } from 'react-router-dom'
import { getSdk } from '../../../internal/host-compat/src/runtime.js'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import Icon from '../../../internal/host-compat/src/components/Icon'
import ConnectorPage from '../../../internal/connector-ui/src/ConnectorPage'
import { pageLocale } from '../../../internal/connector-ui/src/connector'
import { connectorsApi } from '../../../internal/connector-ui/src/api'
import { Check, Field, Notice, Select, TextInput } from '../../../internal/connector-ui/src/controls'
import { WriteModeChip, WriteModeField } from '../../../internal/connector-ui/src/addonparts'
import en from './i18n/mail.en.json'
import it from './i18n/mail.it.json'
import es from './i18n/mail.es.json'
import de from './i18n/mail.de.json'
import fr from './i18n/mail.fr.json'
import zh from './i18n/mail.zh-CN.json'
import id from './i18n/mail.id.json'
import ko from './i18n/mail.ko.json'
import pt from './i18n/mail.pt-BR.json'
import ProposalCard from './ProposalCard'
import { checkItems, fieldProblems, extraBody, extraForm, extraValid, followName, mailSecret, portAfterSecurity, SECURITY, WRITE_MODES, writeModeOf } from './settings'
import { finishTest, resultOf, startTest, subscribe } from './results'
import './mail.css'

// The words of the page, in every language of the web UI; a key a language lacks reads in English.
registerLocales({ mail: { en, it, es, de, fr, 'zh-CN': zh, id, ko, 'pt-BR': pt } })

// No token field: the whole connection is the secret, made by the fields below.
function NoToken() { return null }

function Server({ kind, form, setForm, t }) {
  const sec = form[`${kind}_sec`]
  const set = (patch) => setForm({ ...form, ...patch })
  const problems = fieldProblems(form)
  const hostError = form[`${kind}_host`] && problems[`${kind}Host`] ? t(problems[`${kind}Host`]) : undefined
  const portError = problems[`${kind}Port`] ? t(problems[`${kind}Port`]) : undefined
  return (
    <fieldset className="ml-group" data-testid={`mail-${kind}`}>
      <legend>{t(`${kind}.title`)}</legend>
      <div className="ml-pair">
        <Field label={t('host')} error={hostError}>
          {(p) => <TextInput {...p} mono autoComplete="off" data-testid={`mail-${kind}-host`} value={form[`${kind}_host`]} placeholder={t(`${kind}.hostExample`)}
            onChange={(e) => set({ [`${kind}_host`]: e.target.value })} />}
        </Field>
        <Field label={t('port')} error={portError}>
          {(p) => <TextInput {...p} mono inputMode="numeric" data-testid={`mail-${kind}-port`} value={String(form[`${kind}_port`])}
            onChange={(e) => set({ [`${kind}_port`]: e.target.value.replace(/\D/g, '') })} />}
        </Field>
        <Field label={t('security.label')}>
          {(p) => (
            <Select {...p} data-testid={`mail-${kind}-security`} value={sec}
              onChange={(e) => set({ [`${kind}_sec`]: e.target.value, [`${kind}_port`]: portAfterSecurity(kind, sec, e.target.value, form[`${kind}_port`]) })}>
              {SECURITY.map((s) => <option key={s} value={s}>{t(`security.${s}`)}</option>)}
            </Select>
          )}
        </Field>
      </div>
      {sec === 'none' && (
        <>
          <Notice tone="warn" role="alert" testId={`mail-${kind}-plain`}>{t('security.plainWarning')}</Notice>
          <Check checked={Boolean(form.plain_ok)} onChange={(v) => set({ plain_ok: v })} testId="mail-plain-ok">{t('security.plainConfirm')}</Check>
        </>
      )}
    </fieldset>
  )
}

// The connection: address, login and the two servers. A saved connection
// cannot be read back, so changing any of it means entering all of it again.
function MailFields({ form, setForm, t }) {
  const set = (patch) => setForm({ ...form, ...patch })
  const problems = fieldProblems(form)
  const saved = Boolean(form.id)
  return (
    <>
      {saved && (
        <Check checked={Boolean(form.replace)} onChange={(v) => set({ replace: v })} testId="mail-replace">{t('replace')}</Check>
      )}
      {saved && !form.replace && <p className="cn-muted" data-testid="mail-saved">{t('savedAs', { address: form.mail_address })}</p>}
      {form.replace && (
        <>
          <Field label={t('address')} hint={t('addressHelp')} error={form.mail_address && problems.address ? t(problems.address) : undefined}>
            {(p) => <TextInput {...p} mono type="email" autoComplete="off" data-testid="mail-address" value={form.mail_address}
              onChange={(e) => setForm({ ...form, mail_address: e.target.value, name: followName(form.name, form.mail_address, e.target.value),
                mail_user: form.mail_user === '' || form.mail_user === form.mail_address ? e.target.value : form.mail_user })} />}
          </Field>
          <Field label={t('display')} hint={t('displayHelp')} optional error={problems.display ? t(problems.display) : undefined}>
            {(p) => <TextInput {...p} autoComplete="off" data-testid="mail-display" value={form.mail_display} onChange={(e) => set({ mail_display: e.target.value })} />}
          </Field>
          <Field label={t('username')} hint={t('usernameHelp')} error={form.mail_user && problems.user ? t(problems.user) : undefined}>
            {(p) => <TextInput {...p} mono autoComplete="off" data-testid="mail-user" value={form.mail_user} onChange={(e) => set({ mail_user: e.target.value })} />}
          </Field>
          <Field label={t('password')} hint={t('passwordHelp')} error={form.mail_pass && problems.pass ? t(problems.pass) : undefined}>
            {(p) => <TextInput {...p} mono type="password" autoComplete="new-password" data-testid="mail-pass" value={form.mail_pass} onChange={(e) => set({ mail_pass: e.target.value })} />}
          </Field>
          <Server kind="imap" form={form} setForm={setForm} t={t} />
          <Server kind="smtp" form={form} setForm={setForm} t={t} />
          <Field label={t('folders')} hint={t('foldersHelp')} optional error={problems.folders ? t(problems.folders) : undefined}>
            {(p) => <textarea {...p} className="cn-input cn-input--mono" rows={3} data-testid="mail-folders" value={form.folders_text} placeholder="INBOX" onChange={(e) => set({ folders_text: e.target.value })} />}
          </Field>
          <Field label={t('drafts')} hint={t('draftsHelp')} optional>
            {(p) => <TextInput {...p} mono autoComplete="off" data-testid="mail-drafts" value={form.drafts_folder} placeholder="Drafts" onChange={(e) => set({ drafts_folder: e.target.value })} />}
          </Field>
          {problems.size && <Notice tone="warn" role="alert">{t('tooLong')}</Notice>}
        </>
      )}
      <WriteModeField ns="mail" form={form} setForm={setForm} t={t} modes={WRITE_MODES} modeOf={writeModeOf} />
    </>
  )
}

function Help({ t }) {
  const [open, setOpen] = useState(null)
  useEffect(() => {
    let live = true
    connectorsApi.list().then((r) => { if (live && open === null) setOpen(!(r.connectors || []).some((c) => c.kind === 'mail')) }).catch(() => {})
    return () => { live = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- runs once; the person's own toggle wins afterwards
  }, [])
  return (
    <div className="ml-help">
      <p className="cn-muted" data-testid="mail-limits">{t('limits')}</p>
      <details className="cn-help" data-testid="mail-setup" open={open === true} onToggle={(e) => setOpen(e.currentTarget.open)}>
        <summary>{t('setup.title')}</summary>
        <ol>
          <li>{t('setup.step1')}</li>
          <li>{t('setup.step2')}</li>
          <li>{t('setup.step3')}</li>
          <li>{t('setup.step4')}</li>
        </ol>
        <p className="cn-muted">{t('setup.private')}</p>
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
  if (r.busy) return <p className="cn-muted" role="status" aria-busy="true" data-testid="mail-checking">{t('check.running')}</p>
  return (
    <div data-testid="mail-checks">
      <ul className="ml-checks" aria-label={t('check.title', { name: c.name })}>
        {r.items.map((i) => (
          <li key={i.part} className={`ml-check ml-check--${i.tone}`} data-testid={`mail-check-${i.part}`}>
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
  const mode = writeModeOf(c.write_mode)
  return (
    <>
      <span className="cn-muted" data-testid="mail-row-address">{address || t('noAddress')}</span>
      <span><WriteModeChip ns="mail" mode={mode} t={t} /></span>
      <Checks c={c} t={t} />
    </>
  )
}

// The Test button: the host logs in to both servers (the same call as a save);
// this asks the add-on for each login on its own, so the page can say which of
// the two failed.
async function testExtra(c, t) {
  startTest(c.id)
  try {
    const all = await connectorsApi.list()
    if ((all.connectors || []).filter((x) => x.kind === 'mail').length > 1) {
      finishTest(c.id, [])
      return t('checkSeveral')
    }
  } catch (_e) { /* the checks below say what they can */ }
  let data = null
  try {
    const res = await getSdk().api.read({ arguments: { tool: 'check', mailbox: (c.resources || [])[0] } })
    data = res?.data || null
  } catch (_e) { /* both parts then show as not checked */ }
  const items = checkItems(data).map((i) => ({ part: i.part, tone: i.tone, text: t(i.key, i.vars) }))
  finishTest(c.id, items)
  return ''
}

export function createMail() {
  function Component({ page, path, search }) {
    if (page !== 'apps') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <ConnectorPage kind="mail" ns="mail" testId="mail-page" TokenFields={NoToken} tokenOf={mailSecret} messaging={false}
          ExtraFields={MailFields} extraBody={extraBody} extraForm={extraForm} extraValid={extraValid} testExtra={testExtra}
          Help={Help} RowInfo={RowInfo} />
      </RouteProvider>
    )
  }
  return { Component, components: { ProposalCard }, chatActions: [], dispose() {} }
}
