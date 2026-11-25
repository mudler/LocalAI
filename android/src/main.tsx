// SPDX-License-Identifier: MIT
import { createRoot } from 'react-dom/client'
import { CORE_METHODS } from '@launcher/shared/apiGroups'
import type { Bridge } from '@launcher/renderer/bridge'
import { App } from '@launcher/renderer/App'
import '@launcher/renderer/styles'
import './mobile.css'

// Placeholder until the client bridge exists: every core method fails loudly.
const unwired = Object.fromEntries(
  CORE_METHODS.map((name) => [
    name,
    () => {
      throw new Error(`not wired: ${name}`)
    },
  ]),
) as unknown as Bridge

createRoot(document.getElementById('root')!).render(<App bridge={unwired} />)
