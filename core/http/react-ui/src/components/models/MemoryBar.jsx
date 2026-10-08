import { cssVars, gbLabel } from '../../utils/modelLedger'
import './MemoryBar.css'

// One bar for one pool of memory (a GPU, or system memory).
//
//   capacity   what the pool holds, in bytes. The tick sits here.
//   segments   [{ key, label, bytes, tone }] in the order they are drawn. tone
//              is 'other' (what else uses the pool), 'model' (the model's own
//              share), 'kv' (the part that grows with context) or 'cpu'.
//   free       bytes left after the segments, or negative when over.
//
// The bar is as wide as the pool until the segments outgrow it. Then it grows
// past the tick, and the tick turns red: over is shown as over, never clipped
// to look like it fits. Solid segments, a surface gap between them, no glow.
export default function MemoryBar({ capacity, segments, ariaLabel, testId, legend = true }) {
  const sum = segments.reduce((total, seg) => total + Math.max(0, seg.bytes), 0)
  const scale = Math.max(capacity, sum) || 1
  const over = sum > capacity
  const share = bytes => `${Math.max(0, (bytes / scale) * 100).toFixed(2)}%`
  return (
    <div className="memorybar" data-over={over ? 'true' : 'false'} data-testid={testId}>
      <div className="dk-meter memorybar__meter" role="img" aria-label={ariaLabel}>
        {segments.filter(seg => seg.bytes > 0).map(seg => (
          <span
            key={seg.key}
            className={`dk-meter-seg memorybar__seg memorybar__seg--${seg.tone}`}
            style={cssVars({ '--dk-w': share(seg.bytes) })}
            data-segment={seg.key}
          />
        ))}
        <span className="dk-meter-limit memorybar__limit" style={cssVars({ '--dk-at': share(capacity) })} />
      </div>
      {legend && (
        <ul className="dk-meter-legend memorybar__legend">
          {segments.filter(seg => seg.bytes > 0).map(seg => (
            <li key={seg.key}>
              <span className={`dk-swatch memorybar__swatch memorybar__swatch--${seg.tone}`} aria-hidden="true" />
              {seg.label} <span className="dk-mono">{gbLabel(seg.bytes)}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
