import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { copyToClipboard } from '../../utils/clipboard'
import Icon from '../Icon'

// A command to paste into a terminal, with a Copy button. The block scrolls
// sideways on a narrow screen instead of wrapping, because a wrapped command
// is a broken command. The text is real text: select it, or copy it.
export default function CommandBlock({ command, label, addToast }) {
  const { t } = useTranslation('swarm')
  const [copied, setCopied] = useState(false)
  const timer = useRef(null)
  useEffect(() => () => clearTimeout(timer.current), [])

  const copy = async () => {
    const ok = await copyToClipboard(command)
    if (ok) {
      setCopied(true)
      clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), 2000)
      addToast?.(t('command.copied'), 'success', 2000)
    } else {
      addToast?.(t('command.copyFailed'), 'error')
    }
  }

  return (
    <div className="sw-cmd" data-testid="command-block">
      <pre className="sw-cmd__text" tabIndex={0} aria-label={label}><code>{command}</code></pre>
      <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm sw-cmd__copy" onClick={copy} aria-label={t('command.copyAria')}>
        <Icon name={copied ? 'check' : 'copy'} /> {copied ? t('command.done') : t('command.copy')}
      </button>
    </div>
  )
}
