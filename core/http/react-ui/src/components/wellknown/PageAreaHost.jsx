/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import PageArea from './PageArea'
import { usePages } from '../../hooks/usePages'

// PageAreaHost is the page area of Home with what it reads: the pageArea of
// home.json and the switch of the administrator, which the pages list carries.
// Home draws it right below the composer, before the widgets.
//
// While the area is off the server leaves pageArea out of the file it serves
// and names it in withheld: the area then shows the note that it is off.
//
//   wk  useWellKnown('home')
export default function PageAreaHost({ wk }) {
  const own = usePages()
  if (!wk.doc) return null
  const withheld = Array.isArray(wk.doc.withheld) && wk.doc.withheld.includes('pageArea')
  if (withheld) return <PageArea value={WITHHELD} enabled={false} wk={wk} pages={own.pages} />
  return <PageArea value={wk.doc.data?.pageArea} enabled={own.pageAreaOn} wk={wk} pages={own.pages} />
}

// A value for an area the server withheld: the area only shows that it is off.
const WITHHELD = { page: 'withheld' }
