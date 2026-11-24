import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { chatConnectorsApi } from '../utils/api'
import { addonsApi, addonsCatalog } from '../utils/addonsClient'
import { localizeEntry, localizeRecord } from '../components/addons/addonState'
import { whereItems } from '../utils/agentWizard'
import { useAuth } from '../context/AuthContext'

// The apps the "Where it works" step offers, read from the server: connector
// kinds of running add-ons, the add-ons that give tools (toolAddons, from
// useAgentTools) and, for an administrator, the installable add-ons of the
// catalog. Every source is best effort: one that fails leaves its part out.
// Also returns the icon path each installed add-on declares, by add-on ID.
export function useWhereItems(toolAddons) {
  const { i18n } = useTranslation()
  const { isAdmin } = useAuth()
  const [kinds, setKinds] = useState([])
  const [catalog, setCatalog] = useState([])
  const [icons, setIcons] = useState({})
  const [ready, setReady] = useState(false)

  useEffect(() => {
    let live = true
    const ctl = new AbortController()
    const kindsP = chatConnectorsApi.kinds().then((r) => (Array.isArray(r?.kinds) ? r.kinds : [])).catch(() => [])
    const appsP = addonsApi.apps.list(ctl.signal).catch(() => [])
    const catalogP = isAdmin ? addonsCatalog.search({ text: '', page: 1 }, ctl.signal).then((r) => r.entries).catch(() => []) : Promise.resolve([])
    Promise.all([kindsP, appsP, catalogP]).then(([k, apps, entries]) => {
      if (!live) return
      const installed = new Set([...apps.map((a) => a.id), ...k.map((x) => x.addon)])
      setKinds(k)
      setIcons(Object.fromEntries(apps.filter((a) => a.icon).map((a) => [a.id, a.icon])))
      setCatalog(entries.filter((e) => e.kind === 'addon' && !installed.has(e.id)))
      setReady(true)
    })
    return () => { live = false; ctl.abort() }
  }, [isAdmin])

  const groups = useMemo(() => {
    const g = whereItems({
      kinds,
      toolAddons: (toolAddons || []).map((a) => localizeRecord(a, i18n.language)),
      catalog: catalog.map((e) => localizeEntry(e, i18n.language)),
    })
    return g
  }, [kinds, toolAddons, catalog, i18n.language])
  return { groups, icons, ready, kinds }
}
