import { modelOf } from './lib/model.js'

// The preview of an Office file on the Files page: the text, tables and slide
// outline the read tool returns. The host draws it in its own frame, below the
// file's name and above its download link.
export function createPreview(sdk, t) {
  const { React } = sdk
  const h = React.createElement
  // eslint-disable-next-line no-unused-vars
  const { useEffect, useState } = React

  function OfficePreviewView({ model }) {
    if (model.error !== '') {
      return (
        <p className="office-preview__note" data-testid="office-preview-error" role="note">
          {model.error === 'unsupported' || model.error === 'unreadable' ? t(`office.preview.${model.error}`) : model.error}
        </p>
      )
    }
    return (
      <div className="office-preview" data-testid="office-preview">
        {model.blocks.map((b, i) => {
          if (b.type === 'heading') return <h3 key={i} className="office-preview__heading">{b.text}</h3>
          if (b.type === 'paragraph') return <p key={i} className="office-preview__para">{b.text}</p>
          if (b.type === 'slide') {
            return (
              <section key={i} className="office-preview__slide">
                <h4>{b.index ? `${b.index}. ` : ''}{b.title}</h4>
                <ul>{b.lines.map((l, j) => <li key={j}>{l}</li>)}</ul>
              </section>
            )
          }
          return (
            <div key={i} className="office-preview__tablewrap">
              <table className="office-preview__table">
                <tbody>{b.rows.map((r, j) => <tr key={j}>{r.map((c, k) => <td key={k}>{c}</td>)}</tr>)}</tbody>
              </table>
            </div>
          )
        })}
        {model.truncated && <p className="office-preview__note">{t('office.preview.truncated')}</p>}
      </div>
    )
  }

  // path, name and extension come from the host; the add-on asks its own read
  // tool for the text. A failure shows a note and leaves the host's view.
  function OfficePreview({ path, extension }) {
    const [model, setModel] = useState(null)
    useEffect(() => {
      const ctrl = new AbortController()
      setModel(null)
      sdk.api.read({ arguments: { tool: `read-${extension}`, path } }, { signal: ctrl.signal })
        .then((payload) => { if (!ctrl.signal.aborted) setModel(modelOf(extension, payload)) })
        .catch(() => { if (!ctrl.signal.aborted) setModel(modelOf(extension, null)) })
      return () => ctrl.abort()
    }, [path, extension])
    if (model === null) return <p className="office-preview__note" data-testid="office-preview-loading" aria-busy="true">{t('office.preview.loading')}</p>
    return <OfficePreviewView model={model} />
  }

  return { OfficePreview, OfficePreviewView }
}
