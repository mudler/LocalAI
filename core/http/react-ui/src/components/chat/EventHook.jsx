import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { chatTriggersApi } from '../../utils/api'
import { apiUrl } from '../../utils/basePath'
import { copyToClipboard } from '../../utils/clipboard'
import { curlExample, eventSentence, hookUrl } from '../../utils/chatTrigger'
// eslint-disable-next-line no-unused-vars -- used in JSX; this config has no eslint-plugin-react
import ConfirmDialog from '../ConfirmDialog'
// eslint-disable-next-line no-unused-vars -- used in JSX; this config has no eslint-plugin-react
import { Button } from '../ui'
import './eventhook.css'

// An event trigger in plain words, with the sender, the type and each match set
// as code: "When github sends pull_request where action=opened".
export function EventSentence({ trigger }) {
  const { t } = useTranslation('chats')
  const text = eventSentence(trigger, (key, vars) => t(`event.sentence.${key}`, vars))
  return (
    <span data-testid="event-sentence">
      {text.split('`').map((part, i) => (i % 2 === 1 ? <code key={i}>{part}</code> : part))}
    </span>
  )
}

// One line of text with its own Copy button. The text is real text: select it
// or copy it. A copy button names what it copies for a screen reader.
// eslint-disable-next-line no-unused-vars -- used in JSX; this config has no eslint-plugin-react
function Copyable({ text, block = false, label, copyLabel, testId, copyTestId }) {
  const { t } = useTranslation('chats')
  const [copied, setCopied] = useState(false)
  const timer = useRef(null)
  useEffect(() => () => clearTimeout(timer.current), [])
  const copy = async () => {
    if (!(await copyToClipboard(text))) return
    setCopied(true)
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setCopied(false), 2000)
  }
  return (
    <div className={`cx-hook__row${block ? ' cx-hook__row--block' : ''}`}>
      {block
        ? <pre className="cx-hook__text" tabIndex={0} aria-label={label} data-testid={testId}><code>{text}</code></pre>
        : <code className="cx-hook__text" data-testid={testId}>{text}</code>}
      <Button size="sm" icon={copied ? 'check' : 'copy'} aria-label={copyLabel} data-testid={copyTestId} onClick={copy}>
        {copied ? t('event.hook.copied') : t('event.hook.copy')}
      </Button>
    </div>
  )
}

// What the owner needs to point a sender at an event trigger: the hook URL, and
// right after the trigger is created or its secret rotated, the secret and a
// call that works. The secret comes from the parent, which keeps it in
// component state only: it is never stored, never put in an address and never
// logged, so after a reload only the URL and Rotate secret are left.
// `onSecret(value)` receives the secret of a rotate.
export default function EventHook({ trigger, secret = '', onSecret }) {
  const { t } = useTranslation('chats')
  const [asking, setAsking] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [said, setSaid] = useState('')
  const url = hookUrl(trigger.hook_path, window.location.origin, apiUrl('').replace(/\/+$/, ''))

  const rotate = async () => {
    setBusy(true)
    setError('')
    try {
      const res = await chatTriggersApi.rotateSecret(trigger.id)
      onSecret?.(res?.secret || '')
      setSaid(t('event.hook.rotated'))
      setAsking(false)
    } catch (e) {
      setError(t('event.hook.rotateFailed', { message: e.message }))
      setAsking(false)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="cx-hook" data-testid="event-hook">
      <p className="cx-hook__label">{t('event.hook.urlLabel')}</p>
      <Copyable text={url} label={t('event.hook.urlLabel')} copyLabel={t('event.hook.copyUrl')} testId="hook-url" copyTestId="hook-copy-url" />
      {secret ? (
        <div className="cx-hook__secret" data-testid="hook-secret-box">
          <p className="cx-hook__label">{t('event.hook.secretLabel')}</p>
          <Copyable text={secret} label={t('event.hook.secretLabel')} copyLabel={t('event.hook.copySecret')} testId="hook-secret" copyTestId="hook-copy-secret" />
          <p className="cx-hook__label">{t('event.hook.curlLabel')}</p>
          <Copyable block text={curlExample(url, secret, trigger)} label={t('event.hook.curlLabel')} copyLabel={t('event.hook.copyCurl')} testId="hook-curl" copyTestId="hook-copy-curl" />
          <p className="cx-hook__note" data-testid="hook-paste">{t('event.hook.paste')}</p>
        </div>
      ) : (
        <p className="cx-hook__note" data-testid="hook-no-secret">{t('event.hook.noSecret')}</p>
      )}
      <span className="cx-hook__acts">
        <Button size="sm" variant="secondary" icon="refresh" disabled={busy} onClick={() => setAsking(true)} data-testid="hook-rotate">{t('event.hook.rotate')}</Button>
      </span>
      {error && <p className="cx-hook__note cx-hook__note--err" role="alert">{error}</p>}
      <span className="sr-only" role="status" aria-live="polite">{said}</span>
      <ConfirmDialog
        open={asking}
        title={t('event.hook.rotateTitle', { name: trigger.name })}
        message={t('event.hook.rotateBody')}
        confirmLabel={t('event.hook.rotateConfirm')}
        danger
        pending={busy}
        onConfirm={rotate}
        onCancel={() => setAsking(false)}
      />
    </div>
  )
}
