import { Link } from 'react-router-dom'
import KitNotice from '../../../packages/ui-kit/src/Notice.jsx'

// The kit's Notice (packages/ui-kit) with the app's router link, for a fix
// or a second action with `to`. Props: see the kit file.
export default function Notice(props) {
  return <KitNotice linkComponent={Link} {...props} />
}
