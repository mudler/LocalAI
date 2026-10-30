/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import Skeleton from '../Skeleton'
import LoadError from '../ui/LoadError'
import AddonSlot from '../../addons/AddonSlot'
import { useSlotContributions } from '../../addons/useAddonRegistry'
import { filesApi } from '../../utils/api'
import { extensionOf, fileIcon, fileSize } from '../../utils/fileInfo'
import { renderMarkdown } from '../../utils/markdown'
import { PAGE_CSP } from '../../utils/pagesBridge'
import { withHead } from '../../utils/chatApps'
import { SERVER_VIEWS, TEXT_VIEWS, languageOf, viewOf } from '../../utils/previewPane'
import hljs from '../../utils/hljs'
import './preview.css'

// The most of a text file the preview reads. A longer file shows its start.
export const TEXT_LIMIT = 1024 * 1024

// The view of one item of the preview pane. item is a file of the user's
// ({ type: 'file', path, name, size, mime }) or a code block of a reply
// ({ type: 'block', name, lang, code, view }). It draws only the content:
// the caller gives the name and the actions.
//
// An add-on that previews the file's extension (the files.preview slot)
// replaces the built-in view. Every view has a skeleton while it loads and an
// error with Try again: the pane is never blank.

// What a file view needs from the server: its text, or the server's reading of
// it. { status: loading | ready | error, data, error, retry }.
function useItemData(item, view) {
  const [state, setState] = useState({ status: 'loading', data: null, error: null })
  const [tick, setTick] = useState(0)
  const path = item.type === 'file' ? item.path : ''
  const size = item.size || 0
  const needsText = item.type === 'file' && TEXT_VIEWS.has(view)
  const needsServer = item.type === 'file' && SERVER_VIEWS.has(view)
  useEffect(() => {
    if (!needsText && !needsServer) {
      setState({ status: 'ready', data: null, error: null })
      return undefined
    }
    // A read that ends after another file was chosen must not show.
    let current = true
    setState({ status: 'loading', data: null, error: null })
    const load = needsServer ? filesApi.preview(path) : filesApi.readText(path, TEXT_LIMIT)
    load
      .then((data) => { if (current) setState({ status: 'ready', data, error: null }) })
      .catch((error) => { if (current) setState({ status: 'error', data: null, error }) })
    return () => { current = false }
  }, [path, size, needsText, needsServer, tick])
  const retry = useCallback(() => setTick(n => n + 1), [])
  return { ...state, retry }
}

export default function PreviewBody({ item }) {
  const { t } = useTranslation('workspace')
  const view = item.type === 'block' ? item.view : viewOf(item.name, item.mime)
  const ext = item.type === 'file' ? extensionOf(item.name) : ''
  // An add-on that handles the extension draws the preview instead.
  const addon = useSlotContributions('files.preview', item.type === 'file' && !!ext, ext || undefined)
  const data = useItemData(item, addon.length > 0 ? 'none' : view)
  if (item.type === 'file' && ext && addon.length > 0) {
    return (
      <div className="pv pv--addon" data-testid="preview-addon">
        <AddonSlot slot="files.preview" context={{ path: item.path, name: item.name, extension: ext }} />
      </div>
    )
  }
  if (data.status === 'loading') return <PreviewSkeleton view={view} label={t('preview.loading', { name: item.name })} />
  if (data.status === 'error') {
    const status = data.error?.status
    // A file the server will not read is not an error to retry: it is a
    // download.
    if (status === 413 || status === 415) return <UnknownCard item={item} t={t} note={t('preview.tooLarge')} />
    return (
      <LoadError
        title={t('preview.failedTitle', { name: item.name })}
        error={data.error}
        onRetry={data.retry}
        className="pv__error"
        testId="preview-error"
      />
    )
  }
  const text = item.type === 'block' ? item.code : (typeof data.data === 'string' ? data.data : '')
  const cut = item.type === 'file' && TEXT_VIEWS.has(view) && (item.size || 0) > TEXT_LIMIT
  const body = (() => {
    switch (view) {
      case 'table': return <TableView data={data.data} item={item} t={t} />
      case 'document': return <DocumentView data={data.data} t={t} />
      case 'slides': return <SlidesView data={data.data} t={t} />
      case 'pdf': return <PdfView item={item} t={t} />
      case 'html': return <HtmlView html={text} name={item.name} t={t} />
      case 'image': return <ImageView item={item} t={t} />
      case 'audio': return <audio className="pv__audio" controls src={filesApi.contentUrl(item.path, true)} data-testid="preview-audio" />
      case 'video': return <video className="pv__video" controls src={filesApi.contentUrl(item.path, true)} data-testid="preview-video" />
      case 'markdown': return <MarkdownView text={text} />
      case 'json': return <JsonView text={text} t={t} />
      case 'code': return <CodeView text={text} language={item.type === 'block' ? item.lang : languageOf(item.name)} />
      case 'text': return <pre className="pv__text" data-testid="preview-text">{text}</pre>
      default: return <UnknownCard item={item} t={t} />
    }
  })()
  return (
    <div className={`pv pv--${view}`} data-testid="preview-body" data-view={view}>
      {body}
      {cut && <p className="pv__note">{t('preview.textCut', { size: fileSize(TEXT_LIMIT) })}</p>}
    </div>
  )
}

function PreviewSkeleton({ view, label }) {
  return (
    <div className="pv pv--loading" role="status" aria-label={label} aria-busy="true" data-testid="preview-loading">
      {view === 'table'
        ? <Skeleton variant="block" className="pv__sk-table" />
        : (
          <>
            <Skeleton variant="title" />
            <Skeleton variant="line" count={4} />
            <Skeleton variant="line" className="skeleton--w60" />
          </>
        )}
    </div>
  )
}

// A file with no preview: what it is and a Download.
export function UnknownCard({ item, t, note }) {
  const ext = extensionOf(item.name)
  return (
    <div className="pv-card" data-testid="preview-unknown">
      <Icon name={fileIcon(item.name, item.mime || '')} className="pv-card__icon" />
      <p className="pv-card__name">{item.name}</p>
      <p className="pv-card__meta">
        {ext ? t('preview.typeFile', { ext: ext.toUpperCase() }) : t('preview.typeUnknown')}
        {item.size > 0 && <> · {fileSize(item.size)}</>}
      </p>
      <p className="pv-card__note">{note || t('preview.none')}</p>
      {item.type === 'file' && (
        <a className="btn btn-secondary btn-sm" href={filesApi.contentUrl(item.path)} download={item.name}>
          <Icon name="download" /> {t('download')}
        </a>
      )}
    </div>
  )
}

const columnName = (i) => {
  let s = ''
  for (let n = i + 1; n > 0; n = Math.floor((n - 1) / 26)) s = String.fromCharCode(65 + ((n - 1) % 26)) + s
  return s
}

// A spreadsheet: a tab per sheet, the first row as a sticky header, row
// numbers, and what was left out.
function TableView({ data, item, t }) {
  const sheets = data?.sheets || []
  const [at, setAt] = useState(0)
  const sheet = sheets[Math.min(at, sheets.length - 1)]
  const onKey = (e) => {
    if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return
    e.preventDefault()
    const next = (at + (e.key === 'ArrowRight' ? 1 : -1) + sheets.length) % sheets.length
    setAt(next)
    e.currentTarget.querySelectorAll('[role="tab"]')[next]?.focus()
  }
  if (!sheet) return <p className="pv__note">{t('preview.sheetEmpty')}</p>
  const width = Math.max(0, ...sheet.rows.map(r => r.length))
  const cols = Array.from({ length: width }, (_, i) => i)
  const [head, ...rest] = sheet.rows
  const more = sheet.total_rows > sheet.rows.length || sheet.total_cols > width
  return (
    <div className="pv-table" data-testid="preview-table">
      {sheets.length > 1 && (
        <div className="pv-tabs" role="tablist" aria-label={t('preview.sheets')} onKeyDown={onKey}>
          {sheets.map((s, i) => (
            <button
              key={`${i}-${s.name}`} type="button" role="tab" className="pv-tab"
              id={`pv-tab-${i}`} aria-controls="pv-sheet" aria-selected={i === at} tabIndex={i === at ? 0 : -1}
              onClick={() => setAt(i)} data-testid="preview-sheet-tab"
            >
              {s.name}
            </button>
          ))}
        </div>
      )}
      <div
        className="pv-table__scroll" id="pv-sheet" tabIndex={0}
        role={sheets.length > 1 ? 'tabpanel' : 'region'}
        aria-labelledby={sheets.length > 1 ? `pv-tab-${at}` : undefined}
        aria-label={sheets.length > 1 ? undefined : t('preview.sheetLabel', { name: sheet.name })}
      >
        {sheet.rows.length === 0 ? <p className="pv__note">{t('preview.sheetEmpty')}</p> : (
          <table className="pv-grid">
            <thead>
              <tr>
                <th scope="col" className="pv-grid__n" aria-label={t('preview.row')}>1</th>
                {cols.map(c => <th key={c} scope="col" title={columnName(c)}>{head[c] ?? ''}</th>)}
              </tr>
            </thead>
            <tbody>
              {rest.map((r, i) => (
                <tr key={i}>
                  <th scope="row" className="pv-grid__n">{i + 2}</th>
                  {cols.map(c => <td key={c}>{r[c] ?? ''}</td>)}
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {more && (
        <p className="pv__note" data-testid="preview-table-more">
          {t('preview.showingRows', { rows: sheet.rows.length, total: sheet.total_rows })}
          {sheet.total_cols > width && ` ${t('preview.showingCols', { cols: width, total: sheet.total_cols })}`}{' '}
          {item.type === 'file' && <a href={filesApi.contentUrl(item.path)} download={item.name}>{t('preview.downloadRest')}</a>}
        </p>
      )}
    </div>
  )
}

const TextNote = ({ t, truncated }) => (
  <p className="pv__note pv__note--top" data-testid="preview-text-note">
    <Icon name="info" /> {t('preview.textOnly')}{truncated ? ` ${t('preview.truncated')}` : ''}
  </p>
)

// A Word document as readable text.
function DocumentView({ data, t }) {
  const blocks = data?.blocks || []
  return (
    <div className="pv-doc" data-testid="preview-document">
      <TextNote t={t} truncated={data?.truncated} />
      {blocks.length === 0 && <p className="pv__note">{t('preview.noText')}</p>}
      {blocks.map((b, i) => {
        if (b.type === 'heading') {
          const H = b.level <= 1 ? 'h3' : b.level === 2 ? 'h4' : 'h5'
          return <H key={i} className={`pv-doc__h pv-doc__h${Math.min(b.level || 1, 3)}`}>{b.text}</H>
        }
        if (b.type === 'item') return <p key={i} className="pv-doc__item" data-level={Math.min(b.level || 0, 4)}>{b.text}</p>
        return <p key={i} className="pv-doc__p">{b.text}</p>
      })}
    </div>
  )
}

// A presentation as its slides' text.
function SlidesView({ data, t }) {
  const slides = data?.slides || []
  return (
    <div className="pv-slides" data-testid="preview-slides">
      <TextNote t={t} truncated={data?.truncated} />
      {slides.length === 0 && <p className="pv__note">{t('preview.noText')}</p>}
      {slides.map(s => (
        <section key={s.n} className="pv-slide" aria-label={t('preview.slide', { n: s.n })}>
          <p className="pv-slide__n">{t('preview.slide', { n: s.n })}</p>
          {s.title && <h3 className="pv-slide__title">{s.title}</h3>}
          {(s.lines || []).map((l, i) => <p key={i} className="pv-doc__item" data-level={Math.min(l.level || 0, 4)}>{l.text}</p>)}
          {s.notes && (
            <details className="pv-slide__notes">
              <summary>{t('preview.notes')}</summary>
              <p>{s.notes}</p>
            </details>
          )}
        </section>
      ))}
    </div>
  )
}

// A PDF in the browser's own viewer. The content address answers with a
// sandboxing policy of its own, which the viewer works under; a browser that
// cannot show it still has the line below and Download.
function PdfView({ item, t }) {
  return (
    <div className="pv-pdf" data-testid="preview-pdf">
      <iframe className="pv-frame" src={filesApi.contentUrl(item.path, true)} title={t('preview.pdfTitle', { name: item.name })} referrerPolicy="no-referrer" />
      <p className="pv__note">
        {t('preview.pdfFallback')}{' '}
        <a href={filesApi.contentUrl(item.path)} download={item.name}>{t('download')}</a>
      </p>
    </div>
  )
}

// HTML in the sandbox of the pages: scripts run in an opaque origin under a
// policy that reaches no network. Unlike a page it gets no bridge: a preview
// cannot read or write files or send messages.
function HtmlView({ html, name, t }) {
  const srcDoc = useMemo(() => withHead(html, `<meta http-equiv="Content-Security-Policy" content="${PAGE_CSP}">`), [html])
  return (
    <iframe
      className="pv-frame pv-frame--html" sandbox="allow-scripts" referrerPolicy="no-referrer"
      srcDoc={srcDoc} title={t('preview.htmlTitle', { name })} data-testid="preview-html"
    />
  )
}

// An image, fitted to the pane; a click shows it at full size.
function ImageView({ item, t }) {
  const [zoom, setZoom] = useState(false)
  const src = useMemo(() => {
    if (item.type === 'file') return filesApi.contentUrl(item.path, true)
    // A block's SVG as an image: an <img> never runs the SVG's script.
    return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(item.code || '')}`
  }, [item.type, item.path, item.code])
  const [broken, setBroken] = useState(false)
  useEffect(() => { setBroken(false); setZoom(false) }, [src])
  if (broken) return <UnknownCard item={item} t={t} note={t('preview.imageFailed')} />
  return (
    <div className={`pv-image${zoom ? ' pv-image--zoom' : ''}`} data-testid="preview-image">
      <button type="button" className="pv-image__btn" onClick={() => setZoom(z => !z)} aria-pressed={zoom} aria-label={zoom ? t('preview.zoomOut') : t('preview.zoomIn')}>
        <img src={src} alt={item.name} onError={() => setBroken(true)} />
      </button>
    </div>
  )
}

function MarkdownView({ text }) {
  const html = useMemo(() => renderMarkdown(text), [text])
  return <div className="pv-md cx-prose" data-testid="preview-markdown" dangerouslySetInnerHTML={{ __html: html }} />
}

function CodeView({ text, language }) {
  const html = useMemo(() => {
    try {
      if (language && hljs.getLanguage(language)) return hljs.highlight(text, { language }).value
      return hljs.highlightAuto(text).value
    } catch {
      return null
    }
  }, [text, language])
  return (
    <pre className="pv__code" data-testid="preview-code">
      {html === null ? <code>{text}</code> : <code className="hljs" dangerouslySetInnerHTML={{ __html: html }} />}
    </pre>
  )
}

// JSON, pretty printed, with objects and arrays that fold. Text that is not
// JSON shows as code.
function JsonView({ text, t }) {
  const parsed = useMemo(() => {
    try { return { ok: true, value: JSON.parse(text) } } catch { return { ok: false } }
  }, [text])
  if (!parsed.ok) return <CodeView text={text} language="json" />
  return (
    <div className="pv-json" data-testid="preview-json">
      <JsonNode value={parsed.value} depth={0} t={t} />
    </div>
  )
}

function JsonNode({ value, name, depth, t }) {
  const label = name !== undefined ? <span className="pv-json__key">{JSON.stringify(name)}: </span> : null
  if (value === null || typeof value !== 'object') {
    const kind = value === null ? 'null' : typeof value
    return <div className="pv-json__row">{label}<span className={`pv-json__v pv-json__v--${kind}`}>{JSON.stringify(value)}</span></div>
  }
  const entries = Array.isArray(value) ? value.map((v, i) => [i, v]) : Object.entries(value)
  const [open, close] = Array.isArray(value) ? ['[', ']'] : ['{', '}']
  if (entries.length === 0) return <div className="pv-json__row">{label}{open}{close}</div>
  return (
    <details className="pv-json__node" open={depth < 2}>
      <summary>{label}{open}<span className="pv-json__count">{t('preview.items', { count: entries.length })}</span></summary>
      <div className="pv-json__kids">
        {entries.map(([k, v]) => <JsonNode key={k} name={Array.isArray(value) ? undefined : k} value={v} depth={depth + 1} t={t} />)}
      </div>
      <div className="pv-json__row">{close}</div>
    </details>
  )
}
