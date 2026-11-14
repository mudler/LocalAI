/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../../context/AuthContext'
import { useModels } from '../../hooks/useModels'
import { CAP_CHAT } from '../../utils/capabilities'
import { apiUrl } from '../../utils/basePath'
import { copyToClipboard } from '../../utils/clipboard'
import { AGENT_IDS, AGENTS, DOCS_URL, PLACEHOLDER_CONTEXT, PLACEHOLDER_MODEL, authMode, buildSnippet } from '../../utils/connectSnippets'
import Callout from '../ui/Callout'
import Segmented from '../ui/Segmented'
import Button from '../ui/Button'

function Block({ text, label, copyLabel, doneLabel, onCopied, testId }) {
  const [copied, setCopied] = useState(false)
  const timer = useRef(null)
  useEffect(() => () => clearTimeout(timer.current), [])
  const copy = async () => {
    const ok = await copyToClipboard(text)
    onCopied?.(ok)
    if (!ok) return
    setCopied(true)
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setCopied(false), 2000)
  }
  return (
    <div className="ak-cmd" data-testid={testId}>
      <pre className="ak-cmd__text" tabIndex={0} aria-label={label}><code>{text}</code></pre>
      <Button size="sm" icon={copied ? 'check' : 'copy'} className="ak-cmd__copy" onClick={copy}>
        {copied ? doneLabel : copyLabel}
      </Button>
    </div>
  )
}

// A guide under the key list: pick a coding agent, get its config for this
// server, with the address, the model and (right after creating one) the key
// already filled in. It only prints text; nothing here writes to the server.
export default function AgentConnect({ apiKey, addToast }) {
  const { t } = useTranslation('auth')
  const { authEnabled, staticApiKeyRequired } = useAuth()
  const { models } = useModels(CAP_CHAT)
  const [agent, setAgent] = useState(AGENT_IDS[0])
  const [pickedModel, setPickedModel] = useState('')

  const ids = useMemo(() => models.map(m => m.id).filter(Boolean), [models])
  const model = pickedModel && ids.includes(pickedModel) ? pickedModel : (ids[0] || '')
  const mode = authMode({ authEnabled, staticApiKeyRequired })
  // Absolute base for this instance, honouring any sub-path mount.
  const baseUrl = new URL(apiUrl('/'), window.location.origin).href.replace(/\/$/, '')

  const snippet = buildSnippet(agent, { baseUrl, key: mode === 'user' || mode === 'static' ? apiKey : '', model, mode })
  const meta = AGENTS[agent]
  const options = AGENT_IDS.map(id => ({ id, label: AGENTS[id].name }))
  const hasKey = Boolean(apiKey) && mode === 'user'
  const copied = (ok) => addToast?.(ok ? t('account.connect.copiedToast') : t('account.apiKeys.copyFailed'), ok ? 'success' : 'error', ok ? 2000 : undefined)

  return (
    <section className="ak-connect" aria-labelledby="ak-connect-title" data-testid="agent-connect">
      <div className="ak-connect__head">
        <h3 id="ak-connect-title" className="ak-connect__title">{t('account.connect.title')}</h3>
        <p className="ak-intro">{t('account.connect.intro')}</p>
      </div>

      <Callout tone="info" testId="agent-connect-model-note">{t('account.connect.modelNote')}</Callout>

      <Segmented
        label={t('account.connect.pick')} options={options} value={agent} onChange={setAgent}
        className="ak-connect__pick" testId="agent-connect-pick" itemTestId="agent-connect-"
      />

      <div className="ak-connect__fields">
        <div className="dk-field">
          <label className="dk-label" htmlFor="ak-connect-model">{t('account.connect.model')}</label>
          <div className="dk-select-wrap">
            <select id="ak-connect-model" className="dk-select" value={model} disabled={ids.length === 0} onChange={(e) => setPickedModel(e.target.value)}>
              {ids.length === 0 && <option value="">{PLACEHOLDER_MODEL}</option>}
              {ids.map(id => <option key={id} value={id}>{id}</option>)}
            </select>
          </div>
        </div>
      </div>

      <p className="ak-connect__line" data-testid="agent-connect-endpoint">
        {t('account.connect.endpoint', { agent: meta.name })} <code className="dk-mono">{meta.endpoint}</code>
      </p>
      <p className="ak-connect__line" data-testid="agent-connect-file">
        {agent === 'claude-code'
          ? <>{t('account.connect.fileEnv')} <code className="dk-mono">{meta.file}</code></>
          : <>{t('account.connect.file')} <code className="dk-mono">{meta.file}</code></>}
      </p>

      {snippet.blocks.map(b => (
        <Block
          key={`${agent}-${b.id}`} text={b.text} testId={`agent-connect-snippet-${b.id}`}
          label={t('account.connect.snippetLabel', { agent: meta.name })}
          copyLabel={t('account.connect.copy')} doneLabel={t('account.connect.copied')} onCopied={copied}
        />
      ))}

      <ul className="ak-connect__notes">
        <li data-testid="agent-connect-auth">
          {mode === 'none' && t('account.connect.auth.none')}
          {mode === 'static' && t('account.connect.auth.static')}
          {mode === 'user' && (hasKey ? t('account.connect.auth.userKey') : t('account.connect.auth.userPlaceholder'))}
        </li>
        <li>{t('account.connect.contextNote', { size: PLACEHOLDER_CONTEXT })}</li>
        <li data-testid="agent-connect-caveat">{t(`account.connect.caveat.${agent}`)}</li>
      </ul>

      <a className="ak-connect__docs" href={DOCS_URL} target="_blank" rel="noopener noreferrer" data-testid="agent-connect-docs">
        {t('account.connect.docs')}
      </a>
    </section>
  )
}
