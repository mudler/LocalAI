import { useState } from 'react'
import Badge from './components/Badge'
import './page.css'

// Uses the automatic JSX runtime, a host module name and a component that is
// not in src/: the build answers all three from compat/.
export function Page() {
  const [n] = useState(3)
  return <p className="fx">compat {n}<Badge /></p>
}
