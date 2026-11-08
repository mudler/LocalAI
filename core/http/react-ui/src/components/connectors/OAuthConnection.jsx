/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button, Callout } from '../ui'
import { addonSettingsAddress } from '../../utils/addonSettings'
import { formatWhen, oauthRowState, scopeChoices } from '../../utils/connectorOAuth'

// The login of one binding that uses OAuth, on the Connectors page. It shows
// one state: connected (who, the access, when the token ends, Disconnect),
// reconnect needed (why, Reconnect), not connected (the optional access to
// add, Connect) or waiting for an administrator's approval. The scopes come
// from the add-on's login in the apps list and the account from the server;
// nothing here names a service.
//
//   binding     the binding JSON, with auth_kind oauth2 and its oauth object
//   login       the add-on's login from the apps list ({ requiredScopes,
//               optionalScopes }), or null while unknown
//   isAdmin     an administrator gets a link to the approval form
//   starting    a start of this binding is running: its button spins
//   onConnect(binding, scopes)   start the login with the ticked optional scopes
//   onDisconnect(binding)        ask to disconnect
export default function OAuthConnection({ binding, login, isAdmin, starting = false, disabled = false, onConnect, onDisconnect }) {
  const { t, i18n } = useTranslation('chats')
  const state = oauthRowState(binding, { isAdmin })
  const choices = scopeChoices(login, binding?.oauth?.scopes)
  // The person's ticks, over the default of each scope (on when the login
  // already has it), so a list that arrives later still gets its defaults.
  const [picked, setPicked] = useState({})
  const isOn = (c) => (Object.hasOwn(picked, c.scope) ? picked[c.scope] : c.on)
  const tick = (scope, on) => setPicked((prev) => ({ ...prev, [scope]: on }))
  const connect = () => onConnect?.(binding, choices.filter(isOn).map((c) => c.scope))

  const ticks = choices.length > 0 && (
    <fieldset className="cn-oauth__scopes" data-testid="oauth-scopes" disabled={starting || disabled}>
      <legend className="cn-oauth__legend">{t('connectorsPage.oauth.alsoAllow')}</legend>
      {choices.map((c) => (
        <label key={c.scope} className="dk-choice cn-choice">
          <input type="checkbox" className="dk-check" checked={isOn(c)} data-scope={c.scope}
            onChange={(e) => tick(c.scope, e.target.checked)} />
          <span className="cn-mono">{c.scope}</span>
        </label>
      ))}
    </fieldset>
  )

  if (state.kind === 'connected') {
    const when = formatWhen(state.expiresAt, i18n.language)
    return (
      <div className="cn-oauth" data-testid="oauth-connection" data-state="connected">
        <div className="cn-oauth__text">
          <span className="cn-result cn-result--ok cn-oauth__status">
            <Icon name="check-circle" />
            <span data-testid="oauth-account">{state.account ? t('connectorsPage.oauth.connectedAs', { account: state.account }) : t('connectorsPage.oauth.connected')}</span>
          </span>
          <span className="cn-row__meta">
            {state.scopes.length > 0 && <span data-testid="oauth-granted">{t('connectorsPage.oauth.access', { scopes: state.scopes.join(', ') })}</span>}
            {when && <span data-testid="oauth-expires">{t('connectorsPage.oauth.expires', { time: when })}</span>}
          </span>
        </div>
        <Button size="sm" variant="danger-ghost" icon="plug-off" disabled={disabled} onClick={() => onDisconnect?.(binding)} data-testid="oauth-disconnect">
          {t('connectorsPage.oauth.disconnect')}
        </Button>
      </div>
    )
  }

  if (state.kind === 'reconnect') {
    return (
      <div className="cn-oauth cn-oauth--callout" data-testid="oauth-connection" data-state="reconnect_needed">
        <Callout tone="warn" title={t('connectorsPage.oauth.reconnectTitle')} testId="oauth-reconnect" data-reason={state.reasonKey}
          actions={<Button size="sm" icon="refresh" busy={starting} disabled={disabled} onClick={connect} data-testid="oauth-connect">{t('connectorsPage.oauth.reconnect')}</Button>}>
          {t(`connectorsPage.oauth.reason.${state.reasonKey}`)}
        </Callout>
        {ticks}
      </div>
    )
  }

  if (state.kind === 'approval') {
    const key = state.reasonKey === 'changed' ? 'Changed' : ''
    return (
      <div className="cn-oauth cn-oauth--callout" data-testid="oauth-connection" data-state="needs_approval">
        {state.admin
          ? (
            <Callout tone="warn" testId="oauth-approval"
              actions={<Button size="sm" icon="settings" to={addonSettingsAddress(binding.addon)} data-testid="oauth-approval-link">{t('connectorsPage.oauth.review')}</Button>}>
              {t(`connectorsPage.oauth.approvalAdmin${key}`)}
            </Callout>
          )
          : <Callout tone="info" testId="oauth-approval">{t(`connectorsPage.oauth.approvalUser${key}`)}</Callout>}
      </div>
    )
  }

  return (
    <div className="cn-oauth" data-testid="oauth-connection" data-state="not_connected">
      <div className="cn-oauth__text">
        <span className="cn-oauth__status cn-oauth__muted">{t('connectorsPage.oauth.notConnected')}</span>
        {ticks}
      </div>
      <Button size="sm" icon="log-in" busy={starting} disabled={disabled} onClick={connect} data-testid="oauth-connect">
        {t('connectorsPage.oauth.connect')}
      </Button>
    </div>
  )
}
