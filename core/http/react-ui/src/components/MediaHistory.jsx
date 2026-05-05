import { memo, useState } from 'react'
import { relativeTime } from '../utils/format'

const ICONS = {
  image: 'fa-image',
  video: 'fa-video',
  tts: 'fa-headphones',
  sound: 'fa-music',
  'audio-transform': 'fa-wave-square',
}

export default memo(function MediaHistory({ entries, selectedId, onSelect, onDelete, onClearAll, mediaType }) {
  const [expanded, setExpanded] = useState(true)

  return (
    <div className="media-history" data-testid="media-history">
      <div
        className={`collapsible-header ${expanded ? 'open' : ''}`}
        onClick={() => setExpanded(!expanded)}
        style={{ display: 'flex', alignItems: 'center' }}
      >
        <i className="fas fa-chevron-right" />
        <span style={{ flex: 1 }}>History ({entries.length})</span>
        {entries.length > 0 && (
          <button
            className="media-history-clear-btn"
            title="Clear all"
            onClick={(e) => { e.stopPropagation(); onClearAll() }}
          >
            <i className="fas fa-trash" />
          </button>
        )}
      </div>
      {expanded && (
        <div className="media-history-list">
          {entries.length === 0 ? (
            <div className="media-history-empty">No history yet</div>
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
                    <i className={`fas ${ICONS[mediaType] || 'fa-file'}`} />
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
                  title="Delete"
                  onClick={(e) => { e.stopPropagation(); onDelete(entry.id) }}
                  data-testid="media-history-delete"
                >
                  <i className="fas fa-times" />
                </button>
              </div>
            ))
          )}
        </div>
      )}
    </div>
  )
})
