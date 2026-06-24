import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { copyToClipboard } from '../utils/clipboard'
import { apiUrl } from '../utils/basePath'

// The request the form just built.
//
// LocalAI is an API-first product and Studio is the best place in the app to
// teach its own endpoints: the form stops being a black box, and a result worth
// keeping can be reproduced from a shell without reverse-engineering which
// fields the page sent.
//
// Rendered only once there is something to show. A panel describing a request
// nobody has made yet is a tutorial, not a record.
export default function RequestPanel({ endpoint, body, method = 'POST' }) {
  const { t } = useTranslation('media')
  const [copied, setCopied] = useState(false)

  if (!endpoint || !body) return null

  const json = JSON.stringify(body, null, 2)

  const curl = [
    `curl -X ${method} ${window.location.origin}${apiUrl(endpoint)} \\`,
    `  -H 'Content-Type: application/json' \\`,
    `  -d '${JSON.stringify(body)}'`,
  ].join('\n')

  const onCopy = async () => {
    const ok = await copyToClipboard(curl)
    if (!ok) return
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <section className="request-panel">
      <div className="request-panel__head">
        <span className="request-panel__title">{t('request.heading')}</span>
        <button type="button" className="request-panel__copy" onClick={onCopy}>
          {copied ? t('request.copied') : t('request.copyCurl')}
        </button>
      </div>
      <pre className="request-panel__code">
        <code>
          <span className="request-panel__method">{method}</span>{' '}
          <span className="request-panel__endpoint">{endpoint}</span>
          {'\n'}
          {json}
        </code>
      </pre>
    </section>
  )
}
