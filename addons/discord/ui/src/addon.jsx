/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { RouteProvider } from 'react-router-dom'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import ConnectorPage from '../../../internal/connector-ui/src/ConnectorPage'
import { Field, FieldError, Notice, TextInput } from '../../../internal/connector-ui/src/controls'
import en from './i18n/discord.en.json'
import it from './i18n/discord.it.json'
import es from './i18n/discord.es.json'
import de from './i18n/discord.de.json'
import fr from './i18n/discord.fr.json'
import zh from './i18n/discord.zh-CN.json'
import id from './i18n/discord.id.json'
import ko from './i18n/discord.ko.json'
import pt from './i18n/discord.pt-BR.json'
import { DISCORD_LISTS, PERMISSIONS, isChannelId, PERMISSION_INTEGER, badEntries, discordSecret, inviteUrl, tokenProblem } from './ids'

// The words of the page, in every language of the web UI; a key a language lacks reads in English.
registerLocales({ discord: { en, it, es, de, fr, 'zh-CN': zh, id, ko, 'pt-BR': pt } })

// The bot token. It is typed only to replace it.
function DiscordToken({ form, setForm, t }) {
  const problem = tokenProblem(form)
  return (
    <>
      <Field label={t('token')} hint={t('tokenHelp')}>
        {(p) => (
          <TextInput {...p} mono type="password" autoComplete="off" value={form.token || ''} required={!form.id}
            placeholder={form.id ? t('tokenSaved') : ''} aria-invalid={problem ? 'true' : undefined}
            onChange={(e) => setForm({ ...form, token: e.target.value })} />
        )}
      </Field>
      {problem && <FieldError role="status" data-testid="token-problem">{t(problem)}</FieldError>}
    </>
  )
}

// What this version fixes, and what is wrong with the typed IDs.
function DiscordRules({ form, t }) {
  const bad = badEntries(form)
  return (
    <>
      <Notice tone="info" role="note" testId="discord-rules">
        <p className="cn-notice__text">{t('rules')}</p>
      </Notice>
      {bad.length > 0 && (
        <Notice tone="warn" role="alert" testId="discord-bad-ids">
          <p className="cn-notice__text">{t('badIds', { list: bad.join(', ') })}</p>
        </Notice>
      )}
    </>
  )
}

// How to make the bot in the developer portal, with the link that adds it to a server.
function DiscordHelp({ t }) {
  const [appId, setAppId] = useState('')
  const [copied, setCopied] = useState(false)
  const link = inviteUrl(appId)
  return (
    <details className="cn-help" data-testid="discord-setup">
      <summary>{t('setup.title')}</summary>
      <ol>
        <li>{t('setup.step1')}</li>
        <li>{t('setup.step2')}</li>
        <li>{t('setup.step3')}</li>
        <li>{t('setup.step4')}</li>
        <li>{t('setup.step5')}</li>
        <li>{t('setup.step6')}</li>
      </ol>
      <Field label={t('setup.appId')} hint={t('setup.appIdHelp')}>
        {(p) => <TextInput {...p} mono inputMode="numeric" value={appId} data-testid="discord-app-id" onChange={(e) => { setAppId(e.target.value); setCopied(false) }} />}
      </Field>
      {appId.trim() && !link && <FieldError role="status">{t('setup.appIdBad')}</FieldError>}
      {link && (
        <>
          <pre className="cn-code" data-testid="discord-invite" aria-label={t('setup.invite')}>{link}</pre>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm"
            onClick={async () => { try { await navigator.clipboard.writeText(link); setCopied(true) } catch (_e) { setCopied(false) } }}>
            {copied ? t('setup.copied') : t('setup.copy')}
          </button>
        </>
      )}
      <p className="cn-muted">{t('setup.permissions', { number: PERMISSION_INTEGER })}</p>
      <ul>
        {PERMISSIONS.map((p) => <li key={p.key}>{t(`setup.perm.${p.key}`)}</li>)}
      </ul>
      <p className="cn-muted">{t('setup.never')}</p>
    </details>
  )
}

export function createDiscord() {
  function Component({ page, path, search }) {
    if (page !== 'bots') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <ConnectorPage kind="discord" ns="discord" testId="discord-page" TokenFields={DiscordToken} tokenOf={discordSecret}
          lists={DISCORD_LISTS} isChannelEntry={isChannelId} Help={DiscordHelp} notifyMode="text"
          ExtraFields={DiscordRules} extraValid={(form) => badEntries(form).length === 0 && !tokenProblem(form)} />
      </RouteProvider>
    )
  }
  return { Component, components: {}, chatActions: [], dispose() {} }
}
