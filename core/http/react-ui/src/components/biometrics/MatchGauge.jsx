// MatchGauge — distance vs threshold as a single horizontal meter.
// distance, threshold numeric (cosine distance, lower = closer).
// Scale is 0 → max (default 2× threshold or 1.0) so the threshold sits near the middle.
export default function MatchGauge({ distance, threshold, confidence, verified, label }) {
  const max = Math.max(1.0, (threshold || 0.3) * 2)
  const clamp = (v) => Math.max(0, Math.min(max, v))
  const tPct = (clamp(threshold || 0) / max) * 100
  const dPct = distance == null ? null : (clamp(distance) / max) * 100
  const tone = verified ? 'success' : 'error'

  return (
    <div className={`biometrics-gauge tone-${tone}`} role="img"
      aria-label={`${label || 'Match'}: ${verified ? 'match' : 'no match'} at distance ${distance?.toFixed?.(3) ?? '?'} (threshold ${threshold?.toFixed?.(3) ?? '?'})`}>
      <div className="biometrics-gauge__head">
        <div className="biometrics-gauge__verdict">
          <i className={`fas ${verified ? 'fa-circle-check' : 'fa-circle-xmark'}`} aria-hidden="true" />
          <span>{verified ? 'Match' : 'No match'}</span>
        </div>
        {confidence != null && (
          <div className="biometrics-gauge__confidence">
            <strong>{typeof confidence === 'number' ? confidence.toFixed(1) : confidence}</strong>
            <span>confidence</span>
          </div>
        )}
      </div>
      <div className="biometrics-gauge__track" aria-hidden="true">
        <div className="biometrics-gauge__zone biometrics-gauge__zone--match"
          style={{ width: `${tPct}%` }} />
        <div className="biometrics-gauge__zone biometrics-gauge__zone--miss"
          style={{ left: `${tPct}%`, width: `${100 - tPct}%` }} />
        <div className="biometrics-gauge__threshold" style={{ left: `${tPct}%` }}>
          <span>threshold</span>
        </div>
        {dPct != null && (
          <div className="biometrics-gauge__marker" style={{ left: `${dPct}%` }}>
            <span>distance</span>
          </div>
        )}
      </div>
      <div className="biometrics-gauge__footer">
        <span><em>distance</em> <code>{distance?.toFixed?.(4) ?? '—'}</code></span>
        <span><em>threshold</em> <code>{threshold?.toFixed?.(4) ?? '—'}</code></span>
      </div>
    </div>
  )
}
