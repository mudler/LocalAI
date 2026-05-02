import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { SUPPORTED_LANGUAGES } from '../i18n'

export default function LanguageSwitcher() {
  const { i18n, t } = useTranslation('nav')
  const [open, setOpen] = useState(false)
  const ref = useRef(null)
  const current =
    SUPPORTED_LANGUAGES.find((l) => l.code === i18n.resolvedLanguage) ||
    SUPPORTED_LANGUAGES.find((l) => l.code === i18n.language) ||
    SUPPORTED_LANGUAGES[0]

  useEffect(() => {
    if (!open) return
    const onDoc = (e) => {
      if (ref.current && !ref.current.contains(e.target)) setOpen(false)
    }
    const onKey = (e) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDoc)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  const select = (code) => {
    i18n.changeLanguage(code)
    setOpen(false)
  }

  const label = t('changeLanguage', { defaultValue: 'Change language' })

  return (
    <div className="language-switcher" ref={ref}>
      <button
        type="button"
        className="theme-toggle language-switcher-trigger"
        onClick={() => setOpen((o) => !o)}
        title={label}
        aria-label={label}
        aria-haspopup="listbox"
        aria-expanded={open}
      >
        <i className="fas fa-globe" aria-hidden="true" />
        <span className="language-switcher-code">{current.flag}</span>
      </button>
      {open && (
        <ul className="language-switcher-menu" role="listbox" aria-label={label}>
          {SUPPORTED_LANGUAGES.map((l) => (
            <li key={l.code}>
              <button
                type="button"
                role="option"
                aria-selected={l.code === current.code}
                className={`language-switcher-option ${l.code === current.code ? 'active' : ''}`}
                onClick={() => select(l.code)}
              >
                <span className="language-switcher-flag">{l.flag}</span>
                <span className="language-switcher-name">{l.name}</span>
                {l.code === current.code && (
                  <i className="fas fa-check language-switcher-check" aria-hidden="true" />
                )}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
