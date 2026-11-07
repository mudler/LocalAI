/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useLocation, useSearchParams } from 'react-router-dom'
import ConfirmDialog from '../ConfirmDialog'
import { Button, Callout } from '../ui'
import AddOAuthConnection from './AddOAuthConnection'
import OAuthConnection from './OAuthConnection'
import OAuthNotices from './OAuthNotices'
import { useAuth } from '../../context/AuthContext'
import { useListedAddons } from '../../addons/useAddonRegistry'
import { useOAuthConnections } from '../../hooks/useOAuthConnections'
import { chatConnectorsApi } from '../../utils/api'
import { addonSettingsAddress } from '../../utils/addonSettings'
import { clientMissing, loginBindings } from '../../utils/connectorOAuth'
import '../../pages/connectors.css'

// The login of an add-on that uses OAuth, on the add-on's own page (the host
// component sdk.ui.ConnectionLogin). It is the Connectors page's login for one
// kind: it adds a connection and sends the person to the service at once,
// shows the login of each connection (Connect, Reconnect, Disconnect), reads the
// result of a login that came back to this page, and says when no administrator
// has approved the add-on's client yet. The add-on page keeps editing the rest.
//
//   kind        { kind, title, addon } of the add-on's connection kind
//   bindings    the person's connections of this kind, from the page's list
//   onChanged() the page reloads its list
//   connectLabel, addTitle, addHint, defaultName   the words of the add-on
//               ("Connect with ..."); the rest are the host's
export default function ConnectionLogin({ kind, bindings = [], onChanged, connectLabel, addTitle, addHint, defaultName }) {
  const { t } = useTranslation('chats')
  const location = useLocation()
  const [, setParams] = useSearchParams()
  const { isAdmin } = useAuth()
  const apps = useListedAddons()
  const login = useMemo(() => apps.find((a) => a.id === kind?.addon)?.oauth || null, [apps, kind])
  const [error, setError] = useState('')
  // The start was refused because no client is approved: the list showed none.
  const [refused, setRefused] = useState(false)
  const fail = useCallback((e) => { setError(e?.message || t('errors.send')) }, [t])
  const reload = useCallback(async () => { await onChanged?.() }, [onChanged])

  const { returned, setReturned, starting, leaving, setLeaving, disconnecting, notice, setNotice, connect, disconnect } =
    useOAuthConnections({ t, location, setParams, setError, fail, load: reload, onTypedError: (key) => { if (key !== 'needsApproval') return false; setRefused(true); return true } })

  const mine = loginBindings(bindings, kind)
  const unapproved = refused || clientMissing(mine)
  // Rows that wait for approval say so themselves; the add area says it when no row does.
  const rowsSayIt = mine.some((b) => b?.oauth?.state === 'needs_approval')

  const added = async (b) => {
    setError('')
    setReturned(null)
    await reload()
    // The person asked to connect: go to the service now.
    if (b) await connect(b, [])
  }

  return (
    <section className="cn-login" data-testid="connection-login" data-approval={unapproved ? 'missing' : 'ok'}>
      <OAuthNotices returned={returned} setReturned={setReturned} notice={notice} setNotice={setNotice} />
      {error && (
        <Callout tone="error" role="alert" className="cn-gap" testId="connection-login-error"
          actions={<Button variant="ghost" size="sm" onClick={() => setError('')}>{t('errors.dismiss')}</Button>}>
          {error}
        </Callout>
      )}
      {mine.length > 0 && (
        <ul className="cn-login__list">
          {mine.map((b) => (
            <li key={b.id} className="cn-login__item" data-testid="connection-login-row">
              <span className="cn-login__name">{b.name}</span>
              <OAuthConnection binding={b} login={login} isAdmin={isAdmin}
                starting={starting === b.id} disabled={!!starting && starting !== b.id}
                onConnect={connect} onDisconnect={setLeaving} />
            </li>
          ))}
        </ul>
      )}
      {unapproved
        ? !rowsSayIt && (
          isAdmin
            ? (
              <Callout tone="warn" testId="connection-login-approval"
                actions={<Button size="sm" icon="settings" to={addonSettingsAddress(kind.addon)} data-testid="connection-login-settings">{t('connectorsPage.oauth.review')}</Button>}>
                {t('connectorsPage.oauth.approvalAdmin')}
              </Callout>
            )
            : <Callout tone="info" testId="connection-login-approval">{t('connectorsPage.oauth.approvalUser')}</Callout>
        )
        : (
          <AddOAuthConnection kind={kind} create={chatConnectorsApi.create} onAdded={added}
            openLabel={connectLabel} title={addTitle} hint={addHint || t('connectorsPage.oauth.login.hint')}
            submitLabel={connectLabel} initialName={defaultName} />
        )}
      <ConfirmDialog
        open={!!leaving}
        title={leaving ? t('connectorsPage.oauth.disconnectTitle', { name: leaving.name }) : ''}
        message={t('connectorsPage.oauth.disconnectText')}
        confirmLabel={t('connectorsPage.oauth.disconnect')}
        pendingLabel={t('connectorsPage.oauth.disconnecting')}
        danger
        pending={disconnecting}
        onConfirm={disconnect}
        onCancel={() => { if (!disconnecting) setLeaving(null) }}
      />
    </section>
  )
}
