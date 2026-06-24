// EnrollmentList — grid of enrolled subjects (face or voice).
// entries: [{ id, name, labels?, thumbnail?, registeredAt?, sampleUrl? }]
// mode: 'image' | 'audio' — controls the card visual.
export default function EnrollmentList({ entries, onDelete, mode = 'image', highlightId }) {
  if (!entries || entries.length === 0) {
    return (
      <div className="biometrics-enroll__empty">
        <i className={`fas ${mode === 'image' ? 'fa-user-plus' : 'fa-microphone-lines'}`} aria-hidden="true" />
        <p>No one enrolled yet. Add a sample using the form on the left to start building your identification store.</p>
      </div>
    )
  }

  return (
    <ul className="biometrics-enroll__grid" role="list">
      {entries.map((e) => {
        const highlight = e.id === highlightId
        return (
          <li key={e.id} className={`biometrics-enroll__card ${highlight ? 'highlight' : ''}`}>
            <div className="biometrics-enroll__media">
              {mode === 'image' && e.thumbnail
                ? <img src={e.thumbnail} alt="" />
                : mode === 'audio' && e.sampleUrl
                  ? <audio controls src={e.sampleUrl} />
                  : <div className="biometrics-enroll__initials" aria-hidden="true">{initials(e.name)}</div>}
            </div>
            <div className="biometrics-enroll__body">
              <div className="biometrics-enroll__name">{e.name}</div>
              {e.labels && Object.keys(e.labels).length > 0 && (
                <ul className="biometrics-enroll__labels" aria-label="labels">
                  {Object.entries(e.labels).slice(0, 3).map(([k, v]) => (
                    <li key={k}><span>{k}</span>{v}</li>
                  ))}
                </ul>
              )}
              {e.registeredAt && (
                <div className="biometrics-enroll__meta">
                  <i className="fas fa-clock" aria-hidden="true" /> {formatTime(e.registeredAt)}
                </div>
              )}
            </div>
            <button type="button" className="biometrics-enroll__delete" onClick={() => onDelete(e)}
              aria-label={`Forget ${e.name}`} title="Forget this enrollment">
              <i className="fas fa-trash" aria-hidden="true" />
            </button>
          </li>
        )
      })}
    </ul>
  )
}

function initials(name) {
  if (!name) return '?'
  return name.trim().split(/\s+/).map(p => p[0] || '').join('').slice(0, 2).toUpperCase()
}

function formatTime(ts) {
  try {
    const d = new Date(ts)
    return d.toLocaleString()
  } catch (_) {
    return ts
  }
}
