/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, Callout, LoadError, SkeletonRows } from '../ui'
import { chatOAuthClientsApi } from '../../utils/api'
import { canSaveClient, clientBody, clientStatus, formatWhen, needsSecret, oauthErrorText, staleDigest } from '../../utils/connectorOAuth'
import { copyToClipboard } from '../../utils/clipboard'
import { UntrustedNotice } from '../addons/AddonBits'
import { untrustedSource } from '../addons/addonState'

// The OAuth client of an add-on whose credential slot logs people in with
// OAuth, on the add-on's Settings tab (administrators only). It shows the
// login the add-on declares in plain words, the callback address to register
// with the service, and a client ID and a write-only secret. Saving is the
// approval of the login shown: the save carries the digest the form read, and
// the server refuses it when the login changed since. Every label of the
// login comes from the server; the form names no service. An add-on that does
// not come from the official library gets the untrusted-source warning first:
// the approval hands it the access tokens of everyone who connects.
export default function OAuthClientSettings({ app, api = chatOAuthClientsApi }) {
  const { t, i18n } = useTranslation('addons')
  const ids = useId()
  const [client, setClient] = useState(null)
  const [loadError, setLoadError] = useState(null)
  const [clientId, setClientId] = useState('')
  const [secret, setSecret] = useState('')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [stale, setStale] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)

  const load = useCallback(async ({ keepDraft = false } = {}) => {
    try {
      const c = await api.get(app.id)
      setClient(c)
      setLoadError(null)
      if (!keepDraft) setClientId(c.client_id || '')
    } catch (e) {
      setLoadError(e)
    }
  }, [api, app.id])

  useEffect(() => { load() }, [load])

  useEffect(() => {
    if (!copied) return undefined
    const id = setTimeout(() => setCopied(false), 1500)
    return () => clearTimeout(id)
  }, [copied])

  if (loadError) return <LoadError title={t('oauthClient.loadFailed')} error={loadError} onRetry={() => load()} />
  if (!client) return <SkeletonRows rows={3} />

  const d = client.descriptor || {}
  const status = clientStatus(client)
  const secretNeeded = needsSecret(client)
  const ready = canSaveClient(client, { clientId, secret })

  const save = async (e) => {
    e.preventDefault()
    if (!ready || saving) return
    setSaving(true)
    setSaved(false)
    setError('')
    setStale(false)
    try {
      const next = await api.save(app.id, clientBody({ clientId, secret, digest: client.current_digest }))
      setClient(next)
      setClientId(next.client_id || clientId)
      setSecret('')
      setSaved(true)
    } catch (err) {
      if (staleDigest(err)) setStale(true)
      else setError(oauthErrorText(t, err, 'oauthClient.errors') || err?.message || t('oauthClient.errors.save'))
    } finally {
      setSaving(false)
    }
  }

  // copyToClipboard also works on plain http, where navigator.clipboard is
  // not defined. On a failure the address stays selectable.
  const copy = async () => {
    if (await copyToClipboard(client.callback_url)) setCopied(true)
  }

  const scopes = (list) => (list && list.length ? list.join(', ') : t('oauthClient.none'))
  const approvedWhen = formatWhen(client.approved_at, i18n.language)

  return (
    <section className="ad-host-control ad-oauth" data-testid="oauth-client-settings" aria-labelledby={`${ids}-title`}>
      <div className="ad-oauth__head">
        <h3 id={`${ids}-title`} className="ad-oauth__title">{t('oauthClient.title')}</h3>
        <p className="dk-hint">{t('oauthClient.intro')}</p>
      </div>

      {untrustedSource({ official: app.admin?.official }) && (
        <UntrustedNotice library={app.admin?.sourceId || t('oauthClient.unknownLibrary')} testId="oauth-client-untrusted" />
      )}
      {status === 'unconfigured' && <Callout tone="info" testId="oauth-client-status" data-status={status}>{t('oauthClient.status.unconfigured')}</Callout>}
      {status === 'changed' && <Callout tone="warn" testId="oauth-client-status" data-status={status}>{t('oauthClient.status.changed')}</Callout>}
      {status === 'approved' && (
        <Callout tone="ok" testId="oauth-client-status" data-status={status}>
          {client.approved_by
            ? t('oauthClient.status.approvedBy', { who: client.approved_by, when: approvedWhen })
            : t('oauthClient.status.approved', { when: approvedWhen })}
        </Callout>
      )}

      <dl className="dk-kv ad-oauth__facts" data-testid="oauth-client-descriptor">
        <dt>{t('oauthClient.sentTo')}</dt>
        <dd data-testid="oauth-client-authorize">{d.authorize_url}</dd>
        <dt>{t('oauthClient.hosts')}</dt>
        <dd>{scopes(d.hosts)}</dd>
        <dt>{t('oauthClient.required')}</dt>
        <dd data-testid="oauth-client-required">{scopes(d.required_scopes)}</dd>
        <dt>{t('oauthClient.optional')}</dt>
        <dd data-testid="oauth-client-optional">{scopes(d.optional_scopes)}</dd>
        <dt>{t('oauthClient.tokens')}</dt>
        <dd>{d.token_url}</dd>
        <dt>{t('oauthClient.revoke')}</dt>
        <dd>{d.revoke_url || t('oauthClient.noRevoke')}</dd>
        <dt>{t('oauthClient.clientAuth')}</dt>
        <dd className="ad-oauth__plain">{t(`oauthClient.auth.${['basic', 'post', 'none'].includes(d.client_auth) ? d.client_auth : 'other'}`)}</dd>
        {(d.authorize_params || []).length > 0 && <>
          <dt>{t('oauthClient.params')}</dt>
          <dd>{d.authorize_params.map((p) => `${p.name}=${p.value}`).join(', ')}</dd>
        </>}
      </dl>

      {client.callback_url
        ? (
          <div className="dk-field">
            <label className="dk-label" htmlFor={`${ids}-callback`}>{t('oauthClient.callback')}</label>
            <div className="ad-oauth__copy">
              <input id={`${ids}-callback`} className="dk-input ad-oauth__mono" type="text" readOnly value={client.callback_url} data-testid="oauth-client-callback"
                onFocus={(e) => e.target.select()} />
              <Button size="sm" icon={copied ? 'check' : 'copy'} onClick={copy} data-testid="oauth-client-copy">{copied ? t('oauthClient.copied') : t('oauthClient.copy')}</Button>
            </div>
            <p className="dk-hint">{t('oauthClient.callbackHint')}</p>
          </div>
        )
        : <Callout tone="warn" testId="oauth-client-base-url">{t('oauthClient.baseUrlRequired')}</Callout>}

      <form className="ad-oauth__form" onSubmit={save} data-testid="oauth-client-form">
        <div className="dk-field">
          <label className="dk-label" htmlFor={`${ids}-id`}>{t('oauthClient.clientId')}</label>
          <input id={`${ids}-id`} className="dk-input" type="text" autoComplete="off" spellCheck={false} value={clientId} disabled={saving}
            onChange={(e) => { setClientId(e.target.value); setSaved(false) }} data-testid="oauth-client-id" />
        </div>
        {secretNeeded
          ? (
            <div className="dk-field">
              <label className="dk-label" htmlFor={`${ids}-secret`}>{t('oauthClient.clientSecret')}</label>
              <input id={`${ids}-secret`} className="dk-input" type="password" autoComplete="new-password" spellCheck={false} value={secret} disabled={saving}
                placeholder={client.has_secret ? t('oauthClient.secretKeepPlaceholder') : ''} aria-describedby={`${ids}-secret-hint`}
                onChange={(e) => { setSecret(e.target.value); setSaved(false) }} data-testid="oauth-client-secret" />
              <p className="dk-hint" id={`${ids}-secret-hint`} data-testid="oauth-client-secret-hint" data-saved={client.has_secret ? 'true' : 'false'}>
                {client.has_secret ? t('oauthClient.secretSaved') : t('oauthClient.secretHint')}
              </p>
            </div>
          )
          : <p className="dk-hint">{t('oauthClient.noSecret')}</p>}

        {stale && (
          <Callout tone="warn" role="alert" testId="oauth-client-stale"
            actions={<Button size="sm" icon="refresh" onClick={() => { setStale(false); load({ keepDraft: true }) }}>{t('oauthClient.refresh')}</Button>}>
            {t('oauthClient.stale')}
          </Callout>
        )}
        {error && <Callout tone="error" role="alert" testId="oauth-client-error">{error}</Callout>}
        {saved && <Callout tone="ok" testId="oauth-client-saved">{t('oauthClient.saved')}</Callout>}

        <div className="ad-oauth__actions">
          <Button type="submit" variant="primary" icon="check" busy={saving} disabled={!ready || stale} data-testid="oauth-client-approve">{t('oauthClient.approve')}</Button>
        </div>
      </form>
    </section>
  )
}
