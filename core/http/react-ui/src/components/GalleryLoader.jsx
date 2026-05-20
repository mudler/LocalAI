import { useState, useEffect } from 'react'

const LOADING_PHRASES = [
  { text: 'Loading models...', icon: 'fa-brain' },
  { text: 'Fetching gallery...', icon: 'fa-download' },
  { text: 'Checking availability...', icon: 'fa-circle-check' },
  { text: 'Almost ready...', icon: 'fa-hourglass-half' },
  { text: 'Preparing gallery...', icon: 'fa-store' },
]

// GalleryLoader is the animated skeleton used while the gallery list loads.
// Used by Models, Backends, and (now) the Manage page so an empty fetch state
// reads the same everywhere instead of one tab showing pulsing dots and the
// other showing "Loading...".
export default function GalleryLoader() {
  const [idx, setIdx] = useState(() => Math.floor(Math.random() * LOADING_PHRASES.length))
  const [fade, setFade] = useState(true)

  useEffect(() => {
    const interval = setInterval(() => {
      setFade(false)
      setTimeout(() => {
        setIdx(prev => (prev + 1) % LOADING_PHRASES.length)
        setFade(true)
      }, 300)
    }, 2800)
    return () => clearInterval(interval)
  }, [])

  const phrase = LOADING_PHRASES[idx]

  return (
    <div style={{
      display: 'flex', flexDirection: 'column', alignItems: 'center',
      justifyContent: 'center', padding: 'var(--spacing-xl) var(--spacing-md)',
      minHeight: '280px', gap: 'var(--spacing-lg)',
    }}>
      <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
        {[0, 1, 2, 3, 4].map(i => (
          <div key={i} style={{
            width: 10, height: 10, borderRadius: '50%',
            background: 'var(--color-primary)',
            animation: `galleryDot 1.4s ease-in-out ${i * 0.15}s infinite`,
          }} />
        ))}
      </div>
      <div style={{
        display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)',
        opacity: fade ? 1 : 0,
        transition: 'opacity 300ms ease',
        color: 'var(--color-text-secondary)',
        fontSize: '0.9375rem',
        fontWeight: 500,
      }}>
        <i className={`fas ${phrase.icon}`} style={{ color: 'var(--color-accent)', fontSize: '1.125rem' }} />
        {phrase.text}
      </div>
      <div style={{ width: '100%', maxWidth: '700px', display: 'flex', flexDirection: 'column', gap: '12px' }}>
        {[0.9, 0.7, 0.5].map((opacity, i) => (
          <div key={i} style={{
            height: '48px', borderRadius: 'var(--radius-md)',
            background: 'var(--color-bg-tertiary)', opacity,
            animation: `galleryShimmer 1.8s ease-in-out ${i * 0.2}s infinite`,
          }} />
        ))}
      </div>
      <style>{`
        @keyframes galleryDot {
          0%, 80%, 100% { transform: scale(0.4); opacity: 0.3; }
          40% { transform: scale(1); opacity: 1; }
        }
        @keyframes galleryShimmer {
          0%, 100% { opacity: var(--shimmer-base, 0.15); }
          50% { opacity: var(--shimmer-peak, 0.3); }
        }
      `}</style>
    </div>
  )
}
