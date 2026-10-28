/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { RouteProvider } from 'react-router-dom'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import ConnectorPage from '../../../internal/connector-ui/src/ConnectorPage'
import en from './i18n/slack.en.json'
import { APP_MANIFEST, SLACK_LISTS, slackSecret, tokenProblem } from './tokens'

registerLocales({ slack: { en } })

// The two tokens of a Slack app. They are typed only to replace them.
function SlackTokens({ form, setForm, t }) {
  const problem = tokenProblem(form)
  const placeholder = form.id ? t('tokenSaved') : ''
  return (
    <>
      <label className="cn-field">
        <span>{t('botToken')}</span>
        <input className="input" type="password" autoComplete="off" value={form.bot_token || ''} required={!form.id}
          placeholder={placeholder} onChange={(e) => setForm({ ...form, bot_token: e.target.value })} />
        <span className="cn-muted">{t('botTokenHelp')}</span>
      </label>
      <label className="cn-field">
        <span>{t('appToken')}</span>
        <input className="input" type="password" autoComplete="off" value={form.app_token || ''} required={!form.id}
          placeholder={placeholder} onChange={(e) => setForm({ ...form, app_token: e.target.value })} />
        <span className="cn-muted">{t('appTokenHelp')}</span>
      </label>
      {problem && <p className="cn-muted" role="status" data-testid="token-problem">{t(problem)}</p>}
    </>
  )
}

// How to make the Slack app, with its manifest to paste.
function SlackHelp({ t }) {
  const [copied, setCopied] = useState(false)
  return (
    <details className="cn-help" data-testid="slack-setup">
      <summary>{t('setup.title')}</summary>
      <ol>
        <li>{t('setup.step1')}</li>
        <li>{t('setup.step2')}</li>
        <li>{t('setup.step3')}</li>
        <li>{t('setup.step4')}</li>
      </ol>
      <button type="button" className="btn btn-secondary btn-sm"
        onClick={async () => { try { await navigator.clipboard.writeText(APP_MANIFEST); setCopied(true) } catch (_e) { setCopied(false) } }}>
        {copied ? t('setup.copied') : t('setup.copy')}
      </button>
      <pre className="cn-code" aria-label={t('setup.manifest')}>{APP_MANIFEST}</pre>
    </details>
  )
}

export function createSlack() {
  function Component({ page, path, search }) {
    if (page !== 'apps') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <ConnectorPage kind="slack" ns="slack" testId="slack-page" TokenFields={SlackTokens} tokenOf={slackSecret}
          lists={SLACK_LISTS} Help={SlackHelp} />
      </RouteProvider>
    )
  }
  return { Component, components: {}, chatActions: [], dispose() {} }
}
