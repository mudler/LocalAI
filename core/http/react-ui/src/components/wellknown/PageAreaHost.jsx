/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import PageArea from './PageArea'
import { usePages } from '../../hooks/usePages'

// PageAreaHost is the page area of Home with what it reads: the pageArea of
// home.json and the switch of the administrator, which the pages list carries.
// Home draws it right below the composer, before the widgets.
//
//   wk  useWellKnown('home')
export default function PageAreaHost({ wk }) {
  const own = usePages()
  if (!wk.doc) return null
  return <PageArea value={wk.doc.data?.pageArea} enabled={own.pageAreaOn} wk={wk} pages={own.pages} />
}
