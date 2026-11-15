/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { Button, Callout } from '../ui'

// The two notices of the OAuth connections: the result of a login the service
// sent the browser back with, and the line after an add or a disconnect. The
// Connectors page and the add-on pages that log in draw the same ones, from
// the state of useOAuthConnections.
export default function OAuthNotices({ returned, setReturned, notice, setNotice }) {
  const { t } = useTranslation('chats')
  return (
    <>
      {returned && (
        <Callout tone={returned.outcome === 'ok' ? 'ok' : (returned.outcome === 'denied' ? 'info' : 'error')} role={returned.outcome === 'error' ? 'alert' : 'status'}
          className="cn-gap" testId="oauth-returned" data-outcome={returned.outcome}
          actions={<Button variant="ghost" size="sm" onClick={() => setReturned(null)}>{t('errors.dismiss')}</Button>}>
          {returned.outcome === 'error' ? t(`connectorsPage.oauth.returned.error.${returned.classKey}`) : t(`connectorsPage.oauth.returned.${returned.outcome}`)}
        </Callout>
      )}
      {notice && (
        <Callout tone={notice.tone} role="status" className="cn-gap" testId="oauth-notice" data-tone={notice.tone}
          actions={<Button variant="ghost" size="sm" onClick={() => setNotice(null)}>{t('errors.dismiss')}</Button>}>
          {notice.text}
        </Callout>
      )}
    </>
  )
}
