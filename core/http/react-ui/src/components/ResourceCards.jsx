import { useState } from 'react'
import { getArtifactIcon, inferMetadataType } from '../utils/artifacts'
import { apiUrl } from '../utils/basePath'

export default function ResourceCards({ metadata, onOpenArtifact, messageIndex, agentName }) {
  const [expanded, setExpanded] = useState(false)

  if (!metadata) return null

  const items = []
  const fileUrl = (absPath) => {
    if (!agentName) return absPath
    return apiUrl(`/api/agents/${encodeURIComponent(agentName)}/files?path=${encodeURIComponent(absPath)}`)
  }

  Object.entries(metadata).forEach(([key, values]) => {
    if (!Array.isArray(values)) return
    values.forEach((v, i) => {
      if (typeof v !== 'string') return
      const type = inferMetadataType(key, v)
      const isWeb = v.startsWith('http://') || v.startsWith('https://')
      const url = isWeb ? v : fileUrl(v)
      let title
      if (type === 'url') {
        try { title = new URL(v).hostname } catch (_e) { title = v }
      } else {
        title = v.split('/').pop() || key
      }
      items.push({ id: `meta-${messageIndex}-${key}-${i}`, type, url, title })
    })
  })

  if (items.length === 0) return null

  const shown = expanded ? items : items.slice(0, 3)
  const hasMore = items.length > 3

  return (
    <div className="resource-cards">
      {shown.map(item => (
        <div
          key={item.id}
          className={`resource-card resource-card-${item.type}`}
          role="button"
          tabIndex={0}
          onClick={() => onOpenArtifact && onOpenArtifact(item.id)}
          onKeyDown={(e) => { if ((e.key === 'Enter' || e.key === ' ') && onOpenArtifact) { e.preventDefault(); onOpenArtifact(item.id) } }}
        >
          {item.type === 'image' ? (
            <img src={item.url} alt={item.title} className="resource-card-thumb" />
          ) : (
            <i className={`fas ${getArtifactIcon(item.type)}`} />
          )}
          <span className="resource-card-label">{item.title}</span>
        </div>
      ))}
      {hasMore && !expanded && (
        <button className="resource-cards-more" onClick={(e) => { e.stopPropagation(); setExpanded(true) }}>
          +{items.length - 3} more
        </button>
      )}
      {hasMore && expanded && (
        <button className="resource-cards-more" onClick={(e) => { e.stopPropagation(); setExpanded(false) }}>
          Show less
        </button>
      )}
    </div>
  )
}
