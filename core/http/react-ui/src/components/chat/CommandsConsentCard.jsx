/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import Button from '../ui/Button'
import Callout from '../ui/Callout'
import { useAuth } from '../../context/AuthContext'
import { chatHostExecApi, settingsApi } from '../../utils/api'
import { SANDBOX_INSTALL_LINK, consentExpired } from '../../utils/chatCommands'

// The card a command of the built-in bash waits on, when the server asks before
// each command (chats_host_exec ask). The command runs directly on this
// machine, with no isolation and with its network. An administrator can run it
// once, allow commands on this instance from now on, or refuse it; anyone else
// can only refuse it and is told to ask an administrator. The card lives as
// long as the call waits; the tool line keeps what happened.
export default function CommandsConsentCard({ consent }) {
  const { t } = useTranslation('chats')
  const { isAdmin } = useAuth()
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [expired, setExpired] = useState(false)
  if (!consent?.id) return null

  const answer = async (decision, always = false) => {
    setBusy(always ? 'always' : decision)
    setError('')
    try {
      if (always) {
        const res = await settingsApi.save({ chats_host_exec: 'on' })
        if (res && res.success === false) throw new Error(res.error || 'error')
      }
      await chatHostExecApi.consent(consent.chatId, consent.id, decision)
    } catch (e) {
      setBusy('')
      // The turn that asked has ended (or its server stopped): nobody waits
      // on the card, so it shows as not answered, as an expired network card
      // does, instead of buttons that reach no one.
      if (consentExpired(e)) {
        setExpired(true)
        return
      }
      setError(e.message || 'error')
    }
  }

  if (expired) {
    return (
      <div className="cmd-consent" data-testid="chat-host-consent-expired" data-state="expired">
        <pre className="cmd-consent__command">{consent.command}</pre>
        <p className="cmd-consent__hint" role="status">{t('tools.consent.expired')}</p>
      </div>
    )
  }

  return (
    <Callout
      tone="warn"
      icon="terminal"
      className="cmd-consent"
      role="alertdialog"
      title={t('tools.consent.title')}
      testId="chat-host-consent"
      actions={isAdmin ? (
        <>
          <Button size="sm" variant="primary" disabled={!!busy} onClick={() => answer('once')} data-testid="chat-host-consent-once">{t('tools.consent.once')}</Button>
          <Button size="sm" disabled={!!busy} onClick={() => answer('once', true)} data-testid="chat-host-consent-always">{t('tools.consent.always')}</Button>
          <Button size="sm" variant="ghost" disabled={!!busy} onClick={() => answer('deny')} data-testid="chat-host-consent-deny">{t('tools.consent.deny')}</Button>
        </>
      ) : (
        <Button size="sm" variant="ghost" disabled={!!busy} onClick={() => answer('deny')} data-testid="chat-host-consent-deny">{t('tools.consent.deny')}</Button>
      )}
    >
      <p>{t('tools.consent.body')}</p>
      <pre className="cmd-consent__command" data-testid="chat-host-consent-command">{consent.command}</pre>
      {isAdmin
        ? <p className="cmd-consent__hint">{t('tools.consent.alwaysHint')} <Link to={SANDBOX_INSTALL_LINK}>{t('tools.commands.install')}</Link></p>
        : <p className="cmd-consent__hint" data-testid="chat-host-consent-ask">{t('tools.consent.askAdmin')}</p>}
      {error && <p className="cmd-consent__error" role="alert">{error}</p>}
    </Callout>
  )
}
