import { useState } from 'react'
import { setSdk } from '../host-compat-fixture-runtime.js'
import './page.css'

export async function createAddon(sdk) {
  setSdk(sdk)
  const { Page } = await import('./Page.jsx')
  return { Component: Page, dispose() {} }
}

export const unused = useState
