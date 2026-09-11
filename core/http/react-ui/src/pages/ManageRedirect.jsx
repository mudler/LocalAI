import { Navigate, useLocation } from 'react-router-dom'

export default function ManageRedirect() {
  const { search } = useLocation()
  const legacy = new URLSearchParams(search)
  const backends = legacy.get('tab') === 'backends'
  const next = new URLSearchParams({ view: 'installed' })

  const selection = legacy.get('sel')
  const query = legacy.get(backends ? 'bq' : 'mq')
  const state = legacy.get(backends ? 'bf' : 'mf')

  if (selection) next.set(backends ? 'backend' : 'model', selection)
  if (query) next.set('q', query)
  if (state) next.set('state', state)
  if (backends && legacy.get('bv') === '1') next.set('show_all', '1')
  if (backends && legacy.get('bd') === '1') next.set('development', '1')

  const path = backends ? '/app/backends' : '/app/models'
  return <Navigate to={`${path}?${next.toString()}`} replace />
}
