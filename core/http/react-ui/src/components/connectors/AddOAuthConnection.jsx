/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, Callout } from '../ui'
import { MAX_CONNECTION_NAME, canAddConnection, newConnectionBody } from '../../utils/connectorOAuth'

// Adds a connection of a kind whose add-on logs in with OAuth, on the
// Connectors page. The connection needs only a name: it has no token and no
// agent, and its row then offers Connect. The add-on's own page edits the
// rest (resources, write mode) as for any other connection.
//
//   kind       the group of the kind ({ kind, title })
//   create     posts the body; resolves with the new binding
//   onAdded(binding)   called after the server made it
//   openLabel, title, hint, submitLabel, initialName   optional words of the
//       add-on's own page, which logs in at once ("Connect with ...");
//       without them the Connectors page words apply
export default function AddOAuthConnection({ kind, create, onAdded, openLabel, title, hint, submitLabel, initialName = '' }) {
  const { t } = useTranslation('chats')
  const ids = useId()
  const input = useRef(null)
  const [open, setOpen] = useState(false)
  const [name, setName] = useState(initialName)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => { if (open) input.current?.focus() }, [open])

  const close = () => { setOpen(false); setName(initialName); setError('') }
  const submit = async (e) => {
    e.preventDefault()
    if (!canAddConnection(name) || saving) return
    setSaving(true)
    setError('')
    try {
      const b = await create(newConnectionBody(kind, name))
      close()
      onAdded?.(b)
    } catch (err) {
      // A 400 carries the host's own words (a name in use, a name too long).
      setError(err?.status === 400 && err.body?.error?.message ? err.body.error.message : t('connectorsPage.oauth.add.failed'))
    } finally {
      setSaving(false)
    }
  }

  if (!open) {
    return (
      <Button size="sm" variant="primary" icon={openLabel ? 'log-in' : 'plus'} onClick={() => setOpen(true)} data-testid="oauth-add-open">
        {openLabel || t('connectorsPage.oauth.add.open')}
      </Button>
    )
  }

  return (
    <form className="cn-add" onSubmit={submit} data-testid="oauth-add-form" aria-labelledby={`${ids}-title`}>
      <h3 className="cn-add__title" id={`${ids}-title`}>{title || t('connectorsPage.oauth.add.title', { name: kind.title })}</h3>
      <div className="dk-field">
        <label className="dk-label" htmlFor={`${ids}-name`}>{t('connectorsPage.oauth.add.name')}</label>
        <input id={`${ids}-name`} ref={input} className="dk-input" type="text" autoComplete="off" maxLength={MAX_CONNECTION_NAME} value={name} disabled={saving}
          aria-describedby={`${ids}-hint`} onChange={(e) => { setName(e.target.value); setError('') }} data-testid="oauth-add-name" />
        <p className="dk-hint" id={`${ids}-hint`}>{hint || t('connectorsPage.oauth.add.hint')}</p>
      </div>
      {error && <Callout tone="error" role="alert" testId="oauth-add-error">{error}</Callout>}
      <div className="cn-add__actions">
        <Button type="button" variant="ghost" size="sm" disabled={saving} onClick={close} data-testid="oauth-add-cancel">{t('connectorsPage.oauth.add.cancel')}</Button>
        <Button type="submit" variant="primary" size="sm" icon="plus" busy={saving} disabled={!canAddConnection(name)} data-testid="oauth-add-submit">
          {submitLabel || t('connectorsPage.oauth.add.submit')}
        </Button>
      </div>
    </form>
  )
}
