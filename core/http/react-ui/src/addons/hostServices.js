import * as React from 'react'
import { createPortal } from 'react-dom'
import Icon from '../components/Icon'
import i18n from '../i18n'
import { addonsApi } from '../utils/addonsClient'
import { resourceRequest, dispatchArgs } from './resourceCalls'
import {
  Button, Spinner, EmptyState, Badge, Stack, Text,
  PageHeader, ConfirmDialog, ErrorWithTraceLink, ModelSelector, MediaInput, WaveformPlayer,
  DistributionBars, EmbeddingInspector, TabSwitch,
} from './hostComponents'
import LoadingSpinner from '../components/LoadingSpinner'
import Modal from '../components/Modal'
import Lightbox from '../components/Lightbox'
import HomeModelPicker from '../components/home/HomeModelPicker'
import { useMediaCapture, audioBufferToWavBlob } from '../hooks/useMediaCapture'
import { useModels } from '../hooks/useModels'
import { useResources } from '../hooks/useResources'
import { useVoiceProfiles } from '../hooks/useVoiceProfiles'
import { useOperations } from '../hooks/useOperations'
import { useMCPClient } from '../hooks/useMCPClient'
import useObjectUrl from '../hooks/useObjectUrl'
import { useDelayedAction } from '../hooks/useDelayedAction'
import { basePath } from '../utils/basePath'
import ArtifactCard from './ArtifactCard'

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
    return addonsApi.dispatch(dispatchArgs(body), signal)
  },
  dispatchFile(body, signal) {
    return addonsApi.dispatchFile(dispatchArgs(body), signal)
  },
  async propose(body, signal) {
    return addonsApi.call('/api/addons/proposals', { method: 'POST', body, signal })
  },
  // A resource management call. The route, the method and the own-session
  // confirmation header come from resourceRequest, never from the add-on.
  async resource(call, signal) {
    const r = resourceRequest(call)
    return addonsApi.call(r.path, { method: r.method, body: r.body, headers: r.headers, signal })
  },
}

export const host = Object.freeze({
  React,
  components: {
    Button, Spinner, EmptyState, Badge, Stack, Text, ArtifactCard,
    PageHeader, ConfirmDialog, ErrorWithTraceLink, ModelSelector, MediaInput, WaveformPlayer,
    DistributionBars, EmbeddingInspector, TabSwitch,
    LoadingSpinner, Modal, Lightbox, HomeModelPicker,
  },
  hooks: {
    useMediaCapture, useObjectUrl, useDelayedAction, audioBufferToWavBlob,
    useModels, useResources, useVoiceProfiles, useOperations, useMCPClient,
  },
  createPortal,
  basePath,
  Icon,
  theme: { mode: themeMode, subscribe: subscribeTheme },
  language: () => i18n.language || 'en',
  subscribeLanguage,
  api: gatewayApi,
})
