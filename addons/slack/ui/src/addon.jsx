/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { RouteProvider } from 'react-router-dom'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import ConnectorPage from '../../../internal/connector-ui/src/ConnectorPage'
import { Field, FieldError, TextInput } from '../../../internal/connector-ui/src/controls'
import en from './i18n/slack.en.json'
import { APP_MANIFEST, SLACK_LISTS, slackSecret, tokenProblem } from './tokens'

registerLocales({ slack: { en } })

// The two tokens of a Slack app. They are typed only to replace them.
function SlackTokens({ form, setForm, t }) {
  const problem = tokenProblem(form)
  const placeholder = form.id ? t('tokenSaved') : ''
  return (
    <>
      <Field label={t('botToken')} hint={t('botTokenHelp')}>
        {(p) => (
          <TextInput {...p} mono type="password" autoComplete="off" value={form.bot_token || ''} required={!form.id}
            placeholder={placeholder} aria-invalid={problem === 'botShape' || (problem === 'tokensBoth' && !(form.bot_token || '').trim()) ? 'true' : undefined}
            onChange={(e) => setForm({ ...form, bot_token: e.target.value })} />
        )}
      </Field>
      <Field label={t('appToken')} hint={t('appTokenHelp')}>
        {(p) => (
          <TextInput {...p} mono type="password" autoComplete="off" value={form.app_token || ''} required={!form.id}
            placeholder={placeholder} aria-invalid={problem === 'appShape' || (problem === 'tokensBoth' && !(form.app_token || '').trim()) ? 'true' : undefined}
            onChange={(e) => setForm({ ...form, app_token: e.target.value })} />
        )}
      </Field>
      {problem && <FieldError role="status" data-testid="token-problem">{t(problem)}</FieldError>}
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
      <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm"
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
