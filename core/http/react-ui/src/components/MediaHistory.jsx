import { memo, useState } from 'react'
import { relativeTime } from '../utils/format'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'

const ICONS = {
  image: 'image',
  video: 'video',
  tts: 'headphones',
  sound: 'music',
  'audio-transform': 'waveform',
}

export default memo(function MediaHistory({ entries, selectedId, onSelect, onDelete, onClearAll, mediaType }) {
  const { t } = useTranslation('media')
  const [expanded, setExpanded] = useState(true)

  return (
    <div className="media-history" data-testid="media-history">
      <div
        className={`collapsible-header ${expanded ? 'open' : ''}`}
        onClick={() => setExpanded(!expanded)}
        style={{ display: 'flex', alignItems: 'center' }}
      >
        <Icon name="chevron-right" />
        <span className="flex-1">{t('history.title')} ({entries.length})</span>
        {entries.length > 0 && (
          <button
            className="media-history-clear-btn"
            title={t('history.clearTitle')}
            onClick={(e) => { e.stopPropagation(); onClearAll() }}
          >
            <Icon name="trash" />
          </button>
        )}
      </div>
      {expanded && (
        <div className="media-history-list">
          {entries.length === 0 ? (
            <div className="media-history-empty">{t('history.empty')}</div>
          ) : (
            entries.map(entry => (
              <div
                key={entry.id}
                className={`media-history-item ${selectedId === entry.id ? 'active' : ''}`}
                onClick={() => onSelect(entry.id)}
                data-testid="media-history-item"
              >
                <div className="media-history-item-thumb">
                  {mediaType === 'image' && entry.results?.[0]?.url ? (
                    <img src={entry.results[0].url} alt="" />
                  ) : (
                    <Icon name={ICONS[mediaType] || 'file'} />
                  )}
                </div>
                <div className="media-history-item-info">
                  <div className="media-history-item-top">
                    <span className="media-history-item-prompt">{entry.prompt}</span>
                    <span className="media-history-item-time">{relativeTime(entry.createdAt)}</span>
                  </div>
                  <div className="media-history-item-model">{entry.model}</div>
                </div>
                <button
                  className="media-history-item-delete"
                  title={t('history.deleteEntry')}
                  onClick={(e) => { e.stopPropagation(); onDelete(entry.id) }}
                  data-testid="media-history-delete"
                >
                  <Icon name="close" />
                </button>
              </div>
            ))
          )}
        </div>
      )}
    </div>
  )
})
