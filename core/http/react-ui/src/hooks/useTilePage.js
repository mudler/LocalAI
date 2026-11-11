import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { pagesApi, sharedPagesApi } from '../utils/api'
import { feedPaths, pageChanges } from '../utils/pages'
import { tileState } from '../utils/pageTiles'
import { useWorkspaceFeed } from './workspaceFeed'
import { refreshSharedPages } from './useSharedPages'

const errorOf = (e) => ({ status: e?.status || 0, type: e?.body?.error?.type || '', message: e?.message || '' })

// useTilePage reads the page of a tile (on Home or in a slot) and keeps it
// live: a change of its index.html or page.json reads it again, a change of
// another file of its folder goes to the frame as files.changed. A shared page
// runs the version the viewer allowed, and only once they allowed it on Home
// and in the slots (the embed consent of the trust sheet).
//
//   ref     {name, folder} of tileRef, or null
//   widget  false for the page area of Home and for the dock, which take a
//           page without a widget block
//
// It returns {read, state, run, files, frameRef, load, allow, onRefused,
// sheet}: frameRef goes to the PageFrame, allow(once) records the consent
// (embed unless once), and sheet holds the trust sheet's state.
export function useTilePage(ref, { widget = true } = {}) {
  const { t: tp } = useTranslation('pages')
  const [read, setRead] = useState({ loading: true })
  const [run, setRun] = useState(null) // shared: {html, page, hash, token}
  const [sheet, setSheet] = useState({ open: false, source: false, busy: false, error: '' })
  const frameRef = useRef(null)

  const load = useCallback(async ({ quiet = false } = {}) => {
    if (!ref) {
      setRead({ loading: false, error: { status: 400 } })
      return
    }
    if (!quiet) setRead((r) => ({ ...r, loading: true }))
    try {
      if (ref.folder) {
        const res = await sharedPagesApi.get(ref.folder, ref.name)
        setRead({ loading: false, page: res.page, html: res.html, consent: res.consent?.state, embed: res.consent?.embed === true, src: res })
      } else {
        const res = await pagesApi.get(ref.name)
        // A change of data files leaves the HTML as it was: keep the frame.
        setRead((prev) => ({ loading: false, page: res.page, html: prev.html === res.html ? prev.html : res.html }))
      }
    } catch (e) {
      if (!quiet) setRead({ loading: false, error: errorOf(e) })
    }
  }, [ref])

  useEffect(() => { setRun(null); load() }, [load])

  const state = tileState(read, { widget })
  // A shared page runs the version the viewer allowed, as read then.
  useEffect(() => {
    if (state === 'ready' && ref?.folder && !run) setRun({ html: read.html, page: read.page, hash: read.page.hash, token: '' })
  }, [state, ref, run, read])

  const onFeed = useCallback((ev) => {
    if (!ref || ref.folder) return
    const ch = pageChanges(feedPaths(ev), ref.name)
    if (!ch.all && !ch.index && !ch.meta && ch.rel.length === 0) return
    if (ch.all || ch.index || ch.meta) load({ quiet: true })
    if (ch.rel.length) frameRef.current?.emit('files.changed', { paths: ch.rel })
    else if (ch.all) frameRef.current?.emit('files.changed', { paths: [], resync: true })
  }, [ref, load])
  useWorkspaceFeed(ref && !ref.folder ? [`/pages/${ref.name}/**`] : [], onFeed)

  const files = useMemo(() => (ref?.folder && run ? {
    read: (path) => sharedPagesApi.read(ref.folder, ref.name, run, path),
    readImage: (path) => sharedPagesApi.readImage(ref.folder, ref.name, run, path),
    write: (path, content, base) => sharedPagesApi.write(ref.folder, ref.name, run, path, content, base),
    list: async (dir) => (await sharedPagesApi.files(ref.folder, ref.name, run, dir)).entries || [],
  } : null), [ref, run])

  const onRefused = useCallback(() => {
    setRun(null)
    load({ quiet: true })
  }, [load])

  // allow records the viewer's consent from the trust sheet: for Home and
  // the slots (a lasting consent), or for this visit only. A visit runs the
  // tile now but asks again next time.
  const allow = useCallback(async (once) => {
    const src = read.src
    if (!src) return false
    setSheet((s) => ({ ...s, busy: true, error: '' }))
    try {
      const res = await sharedPagesApi.allow(ref.folder, ref.name, src.page.hash, { once, embed: !once })
      setRun({ html: src.html, page: src.page, hash: res.hash, token: res.token || '' })
      setRead((r) => ({ ...r, consent: once ? r.consent : 'allowed', embed: once ? r.embed : true, once }))
      setSheet({ open: false, source: false, busy: false, error: '' })
      refreshSharedPages()
      return true
    } catch (e) {
      if (e?.status === 409) {
        await load({ quiet: true })
        setSheet((s) => ({ ...s, busy: false, error: tp('trust.changedMeanwhile') }))
      } else {
        setSheet((s) => ({ ...s, busy: false, error: e?.message || 'error' }))
      }
      return false
    }
  }, [read.src, ref, load, tp])

  const openSheet = useCallback(() => setSheet({ open: true, source: false, busy: false, error: '' }), [])
  const closeSheet = useCallback(() => setSheet({ open: false, source: false, busy: false, error: '' }), [])
  const showSource = useCallback((on) => setSheet((s) => ({ ...s, source: on })), [])

  // A visit allowed once runs although the tile state still asks.
  const runnable = (state === 'ready' || (read.once && run)) && (!ref?.folder || !!run)
  return { read, state, run, runnable, files, frameRef, load, allow, onRefused, sheet, openSheet, closeSheet, showSource }
}
