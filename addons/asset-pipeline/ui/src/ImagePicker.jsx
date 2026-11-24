// SPDX-License-Identifier: MIT
export function libraryImages(storage, origin) {
  try {
    const entries = JSON.parse(storage.getItem('localai_image_history') || '[]')
    if (!Array.isArray(entries)) return []
    return entries.flatMap(entry => (Array.isArray(entry?.results) ? entry.results : []).flatMap((image, index) => {
      try {
        if (typeof image?.url !== 'string' || !image.url.trim()) return []
        const url = new URL(image.url, origin)
        if (url.origin !== new URL(origin).origin || !['http:', 'https:'].includes(url.protocol)) return []
        return [{ id: `${entry.id}-${index}`, url: url.pathname + url.search, label: `${entry.prompt || 'Untitled image'} · ${index + 1}` }]
      } catch { return [] }
    }))
  } catch { return [] }
}

export function createImagePicker(sdk) {
  const { createElement: h, useState, useEffect } = sdk.React
  const { Button, SideSheet } = sdk.ui

  function Thumbnail({ item }) {
    const [url, setURL] = useState(''), [failed, setFailed] = useState(false)
    useEffect(() => {
      const controller = new AbortController()
      let objectURL
      sdk.core.fetch(item.url, { signal: controller.signal }).then(async response => {
        if (!response.ok) throw new Error('Image unavailable')
        const blob = await response.blob()
        if (!['image/png', 'image/jpeg', 'image/webp'].includes(blob.type)) throw new Error('Unsupported image')
        if (controller.signal.aborted) return
        objectURL = URL.createObjectURL(blob)
        setURL(objectURL)
      }).catch(() => { if (!controller.signal.aborted) setFailed(true) })
      return () => { controller.abort(); if (objectURL) URL.revokeObjectURL(objectURL) }
    }, [item.url])
    return url ? <img src={url} alt={item.label} /> : <span>{failed ? 'Preview unavailable' : 'Loading preview…'}</span>
  }

  return function ImagePicker({ onSelect, onClose }) {
    const [items] = useState(() => libraryImages(localStorage, location.href))
    const [query, setQuery] = useState(''), [selected, setSelected] = useState(null)
    const [page, setPage] = useState(0), [busy, setBusy] = useState(false), [error, setError] = useState('')
    const filtered = items.filter(item => item.label.toLowerCase().includes(query.toLowerCase()))
    async function choose() {
      setBusy(true); setError('')
      try {
        const response = await sdk.core.fetch(selected.url)
        if (!response.ok) throw new Error('This image is no longer available. Choose another image.')
        const blob = await response.blob()
        if (!['image/png', 'image/jpeg'].includes(blob.type)) throw new Error('Choose a PNG or JPEG image.')
        await onSelect(new File([blob], blob.type === 'image/jpeg' ? 'library.jpg' : 'library.png', { type: blob.type }))
        onClose()
      } catch (err) { setError(err.message) } finally { setBusy(false) }
    }
    return <SideSheet title="Choose an image" description="Images saved in this browser’s LocalAI library." wide onClose={busy ? undefined : onClose} footer={<Button variant="primary" disabled={!selected || busy} busy={busy} onClick={choose}>Use selected image</Button>}>
      <div className="ap-image-library">
        <label>Search images<input className="dk-input" disabled={busy} value={query} onChange={event => { setQuery(event.target.value); setPage(0); setSelected(null); setError('') }} /></label>
        {error && <p role="alert">{error}</p>}
        {!filtered.length && <p>{items.length ? 'No images match your search.' : 'No saved images. Generate an image in Studio or upload a file on the pipeline card.'}</p>}
        <div className="ap-image-grid" role="group" aria-label="Library images">{filtered.slice(page * 12, page * 12 + 12).map(item => <label key={item.id} className="ap-image-choice">
          <input className="dk-check" type="radio" name="pipeline-library-image" checked={selected?.id === item.id} onChange={() => { setSelected(item); setError('') }} disabled={busy} />
          <Thumbnail item={item} /><span>{item.label}</span>
        </label>)}</div>
        {filtered.length > 12 && <div className="ap-actions"><Button disabled={page === 0 || busy} onClick={() => setPage(page - 1)}>Previous</Button><span>Page {page + 1} of {Math.ceil(filtered.length / 12)}</span><Button disabled={(page + 1) * 12 >= filtered.length || busy} onClick={() => setPage(page + 1)}>Next</Button></div>}
      </div>
    </SideSheet>
  }
}
