import { createContext } from './ctx.jsx'
import { createSpeech } from './Speech.jsx'
import { createApi } from './api.js'
import { createVoiceLibrary } from './VoiceLibrary.jsx'
import { createVoiceProfileCreate } from './VoiceProfileCreate.jsx'
import { createVoiceHub } from './VoiceHub.jsx'
import { createVoiceRecognition } from './VoiceRecognition.jsx'
import * as identity from '../../../internal/identity-ui/src/identity.js'

// The second argument is for tests: the host passes the SDK only.
export function createAddon(sdk, options = {}) {
  const ctx = createContext(sdk)
  const { h } = ctx
  const api = createApi(ctx)
  // The library and the profile form are for everyone who may use speech:
  // each user keeps their own saved voices and an administrator manages all
  // (the server decides what each person sees). A viewer without the speech
  // feature gets this notice and the page below is never rendered, so it makes
  // no call and shows no profile data.
  const needsSpeech = (Page) => function SpeechPage(props) {
    if (!ctx.viewer.isAdmin() && !ctx.viewer.hasFeature('audio_speech')) {
      return (
        <main className="page vc-off" data-testid="voice-no-speech">
          <ctx.ui.PageHeader title={ctx.t('voice.noSpeech.title')} supporting={ctx.t('voice.noSpeech.text')} />
          {props.tabs}
        </main>
      )
    }
    return <Page {...props} />
  }
  const sections = {
    recognition: createVoiceRecognition(ctx, api, identity).VoiceRecognition,
    library: needsSpeech(createVoiceLibrary(ctx, api).VoiceLibrary),
    create: needsSpeech(createVoiceProfileCreate(ctx, api).VoiceProfileCreate),
    speech: createSpeech(ctx).Speech,
    ...options.pages,
  }
  const { Hub, redirectFor } = createVoiceHub(ctx, api, identity, sections)
  // One page, "voice", holds every section as a tab. The older page names stay
  // declared in the manifest so a saved link still opens, and each one is
  // sent to its tab (see redirectFor).
  const pages = { voice: Hub }
  for (const legacy of ['recognition', 'library', 'create', 'speech']) {
    pages[legacy] = function LegacyPage({ path }) {
      const { useEffect } = ctx.React
      const target = redirectFor(legacy, path || '', sdk.navigation.current().search)
      useEffect(() => { sdk.navigation.navigate(target, { replace: true }) }, [target])
      return null
    }
  }

  function Component({ page, path }) {
    const Page = pages[page]
    return Page ? <Page path={path || ''} /> : null
  }
  return { Component, components: {}, chatActions: [], dispose() {} }
}

// For tests: the context of an SDK, built the way createAddon builds it.
export const __ctxFor = (sdk) => createContext(sdk)
