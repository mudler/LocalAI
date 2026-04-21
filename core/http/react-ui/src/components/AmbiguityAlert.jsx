// AmbiguityAlert renders the inline picker shown when the import endpoint
// returns a 400 with { modality, candidates }. It turns a failure into
// forward progress by letting the user pick one of the candidate backends
// inline — no separate dialog, no disappearing toast. Chips carry a
// download icon when the candidate isn't installed yet so the user isn't
// surprised by an implicit backend download.

const MODALITY_MESSAGES = {
  tts: 'This looks like a text-to-speech model. Pick one of the backends below to continue.',
  asr: 'This looks like a speech-recognition model. Pick one of the backends below to continue.',
  embeddings: 'This looks like an embeddings model. Pick one of the backends below to continue.',
  image: 'This looks like an image-generation model. Pick one of the backends below to continue.',
  reranker: 'This looks like a reranker or classifier model. Pick one of the backends below to continue.',
  detection: 'This looks like an object-detection model. Pick one of the backends below to continue.',
}

export default function AmbiguityAlert({ modality, candidates = [], knownBackends = [], onPick, onDismiss }) {
  const message = MODALITY_MESSAGES[modality]
    || `We detected this is a \`${modality || 'unknown'}\` model but can't pick a backend automatically. Pick one of the backends below to continue.`

  const installed = new Set(
    (knownBackends || []).filter(b => b && b.installed).map(b => b.name)
  )

  return (
    <div
      data-testid="ambiguity-alert"
      className="card"
      role="status"
      style={{
        marginBottom: 'var(--spacing-md)',
        padding: 'var(--spacing-md)',
        borderColor: 'var(--color-primary)',
        background: 'var(--color-bg-secondary)',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--spacing-sm)' }}>
        <i
          className="fas fa-lightbulb"
          aria-hidden="true"
          style={{ color: 'var(--color-primary)', marginTop: '2px' }}
        />
        <div style={{ flex: 1, fontSize: '0.875rem' }}>
          <div style={{ marginBottom: candidates.length > 0 ? 'var(--spacing-sm)' : 0 }}>
            {message}
          </div>
          {candidates.length > 0 && (
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: '6px' }}>
              {candidates.map(name => {
                const isInstalled = installed.has(name)
                return (
                  <button
                    key={name}
                    type="button"
                    data-testid={`ambiguity-chip-${name}`}
                    onClick={() => onPick && onPick(name)}
                    title={isInstalled
                      ? `Use ${name} for this import`
                      : `Use ${name} — LocalAI will install it first`}
                    style={{
                      display: 'inline-flex',
                      alignItems: 'center',
                      gap: '6px',
                      padding: '4px 10px',
                      borderRadius: '999px',
                      fontSize: '0.8125rem',
                      cursor: 'pointer',
                      background: 'var(--color-bg-primary)',
                      color: 'var(--color-text-primary)',
                      border: '1px solid var(--color-border-default)',
                    }}
                  >
                    <span>{name}</span>
                    {!isInstalled && (
                      <i
                        className="fas fa-download"
                        aria-hidden="true"
                        title="Not installed yet — LocalAI will download it"
                        style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}
                      />
                    )}
                  </button>
                )
              })}
            </div>
          )}
        </div>
        {onDismiss && (
          <button
            type="button"
            data-testid="ambiguity-dismiss"
            onClick={onDismiss}
            aria-label="Dismiss"
            title="Dismiss"
            style={{
              background: 'none',
              border: 'none',
              color: 'var(--color-text-muted)',
              cursor: 'pointer',
              padding: '2px 6px',
              fontSize: '0.9rem',
            }}
          >
            <i className="fas fa-times" aria-hidden="true" />
          </button>
        )}
      </div>
    </div>
  )
}
