import * as React from 'react'
import Icon from '../components/Icon'
import i18n from '../i18n'
import { addonsApi } from '../utils/addonsClient'
import { Button, Spinner, EmptyState, Badge, Stack, Text } from './hostComponents'

// The real host services behind the SDK (see sdk.js for what an add-on gets).

function themeMode() {
  const t = document.documentElement.getAttribute('data-theme')
  return t === 'light' ? 'light' : 'dark'
}

function subscribeTheme(fn) {
  const observer = new MutationObserver(() => fn(themeMode()))
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
  return () => observer.disconnect()
}

function subscribeLanguage(fn) {
  const handler = (lng) => fn(String(lng || 'en'))
  i18n.on('languageChanged', handler)
  return () => i18n.off('languageChanged', handler)
}

// The gateway client. Both calls carry the add-on and the generation the SDK
// is bound to; the server denies a stale one whatever the page holds.
const gatewayApi = {
  dispatch(body, signal) {
    return addonsApi.dispatch({
      operationId: body.operationId,
      action: body.action,
      addonId: body.addonId,
      generation: body.generation,
      args: body.arguments,
    }, signal)
  },
  async propose(body, signal) {
    return addonsApi.call('/api/addons/proposals', { method: 'POST', body, signal })
  },
}

export const host = Object.freeze({
  React,
  components: { Button, Spinner, EmptyState, Badge, Stack, Text },
  Icon,
  theme: { mode: themeMode, subscribe: subscribeTheme },
  language: () => i18n.language || 'en',
  subscribeLanguage,
  api: gatewayApi,
})
