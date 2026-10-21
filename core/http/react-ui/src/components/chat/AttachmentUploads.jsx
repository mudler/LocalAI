import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { UPLOAD_FAILED, UPLOAD_SAVED } from '../../utils/attachmentCollection'

const ICONS = { [UPLOAD_SAVED]: 'check-circle', [UPLOAD_FAILED]: 'alert-circle' }

// Where each attachment of the last message went: still uploading, saved to
// the chosen collection, or failed with the server's reason. The chat reply
// never waits for these.
export default function AttachmentUploads({ uploads, onDismiss }) {
  const { t } = useTranslation('chat')
  if (!uploads || uploads.length === 0) return null
  const busy = uploads.some(u => u.state !== UPLOAD_SAVED && u.state !== UPLOAD_FAILED)
  return (
    <div className="cx-uploads" data-testid="chat-attachment-uploads" role="status" aria-live="polite">
      <ul className="cx-uploads__list">
        {uploads.map(u => (
          <li key={u.id} className="cx-uploads__item" data-state={u.state} data-testid="chat-attachment-upload">
            <Icon name={ICONS[u.state] || 'spinner'} spin={!ICONS[u.state]} />
            <span className="cx-uploads__name">{u.name}</span>
            <span className="cx-uploads__state">
              {u.state === UPLOAD_SAVED
                ? t('attachmentCollection.saved', { collection: u.collection })
                : u.state === UPLOAD_FAILED
                  ? t('attachmentCollection.failed', { reason: u.error })
                  : t('attachmentCollection.uploading', { collection: u.collection })}
            </span>
          </li>
        ))}
      </ul>
      {!busy && (
        <button
          type="button"
          className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm"
          aria-label={t('attachmentCollection.dismiss')}
          onClick={onDismiss}
        >
          <Icon name="close" />
        </button>
      )}
    </div>
  )
}
