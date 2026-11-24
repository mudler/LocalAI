import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import { Button, Callout, Tag } from '../../ui'
import { useCommandPreview } from '../../../hooks/useLlamaCppRuntime'
import { llamaCppApi } from '../../../utils/api'

// Copies text, with the old execCommand path for a page served over plain HTTP
// on another host, where navigator.clipboard is missing.
async function copyText(text) {
  if (navigator.clipboard?.writeText) {
    try { await navigator.clipboard.writeText(text); return true } catch { /* fall back */ }
  }
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.className = 'rt-offscreen'
  document.body.appendChild(area)
  area.select()
  let ok = false
  try { ok = document.execCommand('copy') } catch { ok = false }
  area.remove()
  return ok
}

// The llama-server command the model runs as, asked from the server a moment
// after the last edit, with a copy button.
export function CommandPreview({ config }) {
  const { t } = useTranslation('modelEditor')
  const preview = useCommandPreview(config)
  const [copied, setCopied] = useState(null)
  const timer = useRef(null)
  useEffect(() => () => clearTimeout(timer.current), [])

  const copy = async () => {
    const ok = await copyText(preview.command)
    setCopied(ok ? 'ok' : 'failed')
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setCopied(null), 2000)
  }

  return (
    <div className="rt-cmd" data-testid="rt-preview">
      <div className="rt-cmd__head">
        <span className="dk-eyebrow" id="rt-preview-label">{t('runtime.preview.title')}</span>
        {preview.status === 'loading' && preview.command && <span className="text-meta" data-testid="rt-preview-updating">{t('runtime.preview.updating')}</span>}
        <Button
          size="sm"
          variant="ghost"
          icon={copied === 'ok' ? 'check' : 'copy'}
          className="rt-cmd__copy"
          disabled={!preview.command}
          onClick={copy}
          data-testid="rt-preview-copy"
        >
          {copied === 'ok' ? t('runtime.preview.copied') : copied === 'failed' ? t('runtime.preview.copyFailed') : t('runtime.preview.copy')}
        </Button>
      </div>
      {preview.command
        ? <pre className="rt-code rt-code--block" tabIndex={0} aria-labelledby="rt-preview-label" data-testid="rt-preview-command">{preview.command}</pre>
        : preview.status !== 'error' && <span className="dk-skeleton dk-skeleton--line" role="status" aria-label={t('runtime.preview.loading')} />}
      {preview.status === 'error' && (
        <Callout tone="warn" title={t('runtime.preview.failed')} testId="rt-preview-error">{preview.error}</Callout>
      )}
      <p className="dk-hint">{t('runtime.preview.note')}</p>
    </div>
  )
}

// A llama-server command pasted in, sent to the server to be read, and written
// into the form. Whatever the server could not map is listed with its reason
// and one click to keep it as a raw option, so nothing is dropped silently.
//
//   onApply(parsed)       writes the answer and returns { count, modelFile }
//   onKeep(unmapped)      adds a raw option entry for an unmapped flag
//   onDrop(flag)          removes the raw entry the server kept for a flag
//   onUseModelFile(file)  sets parameters.model
//
// The server keeps a flag with no field as a raw entry already, and lists it
// as unmapped so the person sees it; a flag LocalAI sets itself (--port, ...)
// is listed as ignored; a flag whose value does not fit is offered to keep.
export function PasteCommand({ onApply, onKeep, onDrop, onUseModelFile, currentModelFile }) {
  const { t } = useTranslation('modelEditor')
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState(null)
  const [error, setError] = useState(null)
  const [kept, setKept] = useState(new Set())

  const fill = async () => {
    const command = text.trim()
    if (!command) return
    setBusy(true)
    setError(null)
    try {
      const parsed = await llamaCppApi.parseCommand(command)
      const applied = onApply(parsed)
      const options = Array.isArray(parsed?.options) ? parsed.options.map(String) : []
      const unmapped = (Array.isArray(parsed?.unmapped) ? parsed.unmapped : []).map(u => ({
        ...u,
        ignored: /^ignored\b/i.test(String(u.reason || '')),
        // The server kept it: an options entry with the flag as its name.
        kept: options.some(o => o === u.flag || o.startsWith(`${u.flag}:`)),
      }))
      setKept(new Set(unmapped.map((u, i) => (u.kept ? i : -1)).filter(i => i >= 0)))
      setResult({
        count: applied.count,
        modelFile: applied.modelFile,
        unmapped,
        warnings: Array.isArray(parsed?.warnings) ? parsed.warnings : [],
      })
    } catch (err) {
      setResult(null)
      setError(err?.message || String(err))
    } finally {
      setBusy(false)
    }
  }

  const keep = (i) => {
    onKeep(result.unmapped[i])
    setKept(prev => new Set(prev).add(i))
  }
  const drop = (i) => {
    onDrop(result.unmapped[i].flag)
    setKept(prev => { const next = new Set(prev); next.delete(i); return next })
  }
  const keepable = (u, i) => !u.ignored && !kept.has(i)
  const keepAll = () => {
    const next = new Set(kept)
    result.unmapped.forEach((u, i) => { if (keepable(u, i)) { onKeep(u); next.add(i) } })
    setKept(next)
  }
  const unmappedLeft = result ? result.unmapped.filter(keepable).length : 0
  const showModelFile = result?.modelFile && result.modelFile !== currentModelFile

  return (
    <div className="rt-paste" data-testid="rt-paste">
      <div className="dk-field">
        <label className="dk-label" htmlFor="rt-paste-text">{t('runtime.paste.title')}</label>
        <textarea
          id="rt-paste-text"
          className="dk-textarea dk-input--mono rt-paste__text"
          value={text}
          rows={4}
          spellCheck={false}
          placeholder="llama-server -m model.gguf --ctx-size 8192 -ngl 99 -fa on"
          aria-describedby="rt-paste-hint"
          onChange={e => setText(e.target.value)}
          data-testid="rt-paste-text"
        />
        <p className="dk-hint" id="rt-paste-hint">{t('runtime.paste.hint')}</p>
      </div>
      <div>
        <Button icon="terminal" busy={busy} disabled={busy || !text.trim()} onClick={fill} data-testid="rt-paste-fill">
          {t('runtime.paste.fill')}
        </Button>
      </div>

      {error && <Callout tone="error" role="alert" title={t('runtime.paste.failed')} testId="rt-paste-error">{error}</Callout>}

      {result && (
        <div className="rt-paste__result" data-testid="rt-paste-result">
          <Callout tone="ok" title={t('runtime.paste.filled', { count: result.count })} testId="rt-paste-filled">
            {result.unmapped.length === 0 ? t('runtime.paste.allMapped') : null}
          </Callout>
          {showModelFile && (
            <Callout
              tone="info"
              title={t('runtime.paste.modelFile', { file: result.modelFile })}
              testId="rt-paste-model-file"
              actions={<Button size="sm" onClick={() => { onUseModelFile(result.modelFile); setResult(r => ({ ...r, modelFile: undefined })) }} data-testid="rt-paste-use-model">{t('runtime.paste.useModelFile')}</Button>}
            >
              {t('runtime.paste.modelFileKept', { file: currentModelFile || '-' })}
            </Callout>
          )}
          {result.warnings.length > 0 && (
            <Callout tone="warn" title={t('runtime.paste.warnings', { count: result.warnings.length })} testId="rt-paste-warnings">
              <ul className="rt-list">{result.warnings.map((w, i) => <li key={i}>{w}</li>)}</ul>
            </Callout>
          )}
          {result.unmapped.length > 0 && (
            <Callout
              tone="warn"
              title={t('runtime.paste.unmapped', { count: result.unmapped.length })}
              testId="rt-paste-unmapped"
              actions={unmappedLeft > 1 ? <Button size="sm" onClick={keepAll} data-testid="rt-paste-keep-all">{t('runtime.paste.keepAll')}</Button> : null}
            >
              {t('runtime.paste.unmappedNote')}
              <ul className="rt-unmapped">
                {result.unmapped.map((u, i) => (
                  <li key={i} className="rt-unmapped__row" data-testid={`rt-unmapped-${i}`}>
                    <div className="rt-unmapped__text">
                      <code className="rt-flag">{[u.flag, u.value].filter(v => v !== undefined && v !== null && v !== '').join(' ')}</code>
                      {u.reason && <span className="text-meta">{u.reason}</span>}
                    </div>
                    {u.ignored
                      ? <Tag icon="ban" testId={`rt-unmapped-ignored-${i}`}>{t('runtime.paste.ignored')}</Tag>
                      : kept.has(i)
                        ? (
                          <span className="rt-unmapped__actions">
                            <Tag tone="ok" icon="check">{t('runtime.paste.kept')}</Tag>
                            <Button size="sm" variant="ghost" onClick={() => drop(i)} data-testid={`rt-unmapped-drop-${i}`}>{t('runtime.paste.drop')}</Button>
                          </span>
                        )
                        : <Button size="sm" onClick={() => keep(i)} data-testid={`rt-unmapped-keep-${i}`}>{t('runtime.paste.keep')}</Button>}
                  </li>
                ))}
              </ul>
            </Callout>
          )}
        </div>
      )}
    </div>
  )
}
