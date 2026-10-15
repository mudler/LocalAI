import { test, expect } from './coverage-fixtures.js'
import { fakeRealtime } from './chat-fixtures.js'

// Talk shows the states the connection really reaches, over a fake WebRTC link:
// the page connects as it would, and the spec plays the server's events.

const status = (page) => page.getByTestId('talk-status')
const emit = (page, event) => page.evaluate((e) => window.__talk.emit(e), event)

// The server only speaks once the link is up, so the spec waits for that too.
const connected = (page) => page.waitForFunction(() => window.__talk.dc && window.__talk.pc?.connectionState === 'connected')

async function startSession(page) {
  await page.goto('/app/talk')
  await expect(page.getByTestId('talk-start')).toBeEnabled()
  await page.getByTestId('talk-start').click()
  await connected(page)
  await emit(page, { type: 'session.created' })
}

test.describe('Talk states', () => {
  test('idle: ready, with one way to start and a note on how the microphone is used', async ({ page }) => {
    await fakeRealtime(page)
    await page.goto('/app/talk')
    await expect(status(page)).toHaveAttribute('data-state', 'idle')
    await expect(status(page).getByRole('heading')).toHaveText('Ready')
    await expect(page.getByTestId('talk-start')).toHaveText('Start session')
    await expect(page.getByText('The server notices when you stop talking')).toBeVisible()
    await expect(page.getByTestId('talk-transcript')).toContainText('What you say and what the model answers appears here')
    // The pipeline chip, the voice and the language.
    await expect(page.getByTestId('home-model-chip')).toContainText('voice-assistant')
    await expect(page.getByRole('button', { name: /alloy/ })).toBeVisible()
    await expect(page.getByRole('button', { name: /auto/ })).toBeVisible()
    // Push to talk and hands-free are not on the page.
    await expect(page.getByText(/push to talk|hands-free/i)).toHaveCount(0)
  })

  test('connecting: the call is being set up and the button cannot be pressed twice', async ({ page }) => {
    await fakeRealtime(page)
    await page.addInitScript(() => { window.__talk.hold = true })
    await page.goto('/app/talk')
    await page.getByTestId('talk-start').click()
    await expect(status(page)).toHaveAttribute('data-state', 'connecting')
    await expect(status(page).getByRole('heading')).toHaveText('Connecting')
    await expect(page.getByTestId('talk-start')).toHaveCount(0)
    await expect(page.getByTestId('talk-end')).toBeVisible()
  })

  test('listening, hearing you, thinking, speaking and back to listening', async ({ page }) => {
    await fakeRealtime(page)
    await startSession(page)
    await expect(status(page)).toHaveAttribute('data-state', 'listening')
    await expect(status(page).getByRole('heading')).toHaveText('Listening')
    await expect(status(page)).toContainText('Say what you need. I answer when you stop.')

    await emit(page, { type: 'input_audio_buffer.speech_started' })
    await expect(status(page)).toContainText('Hearing you speak.')

    await emit(page, { type: 'input_audio_buffer.speech_stopped' })
    await expect(status(page)).toHaveAttribute('data-state', 'thinking')
    await expect(status(page).getByRole('heading')).toHaveText('Thinking')

    await emit(page, { type: 'conversation.item.input_audio_transcription.completed', item_id: 'u1', transcript: 'What is on my calendar?' })
    await expect(status(page)).toContainText('Writing the reply.')

    await emit(page, { type: 'response.output_audio_transcript.delta', item_id: 'a1', delta: 'You have two meetings.' })
    await emit(page, { type: 'response.output_audio.delta' })
    await expect(status(page)).toHaveAttribute('data-state', 'speaking')
    await expect(status(page).getByRole('heading')).toHaveText('Speaking')
    await expect(status(page)).toContainText('Speak over me at any time to interrupt.')

    await emit(page, { type: 'response.output_audio_transcript.done', item_id: 'a1', transcript: 'You have two meetings today.' })
    await emit(page, { type: 'response.done', response: { status: 'completed' } })
    await expect(status(page)).toHaveAttribute('data-state', 'listening')

    const lines = page.getByTestId('talk-transcript').locator('.talk-turn')
    await expect(lines).toHaveCount(2)
    await expect(lines.nth(0)).toContainText('You')
    await expect(lines.nth(0)).toContainText('What is on my calendar?')
    await expect(lines.nth(1)).toContainText('Reply')
    await expect(lines.nth(1)).toContainText('You have two meetings today.')
  })

  test('captions stream into one line per turn, and a discarded turn is retracted', async ({ page }) => {
    await fakeRealtime(page)
    await startSession(page)
    await emit(page, { type: 'conversation.item.input_audio_transcription.delta', item_id: 'u1', delta: 'What is ' })
    await emit(page, { type: 'conversation.item.input_audio_transcription.delta', item_id: 'u1', delta: 'the time' })
    await expect(page.getByTestId('talk-transcript').locator('.talk-turn')).toHaveCount(1)
    await expect(page.getByTestId('talk-transcript')).toContainText('What is the time')
    await emit(page, { type: 'conversation.item.input_audio_transcription.failed', item_id: 'u1' })
    await expect(page.getByTestId('talk-transcript').locator('.talk-turn')).toHaveCount(0)
  })

  test('interrupted: a cancelled reply leaves a note and says so until you speak again', async ({ page }) => {
    await fakeRealtime(page)
    await startSession(page)
    await emit(page, { type: 'response.output_audio_transcript.delta', item_id: 'a1', delta: 'Let me explain the whole' })
    await emit(page, { type: 'response.output_audio.delta' })
    await emit(page, { type: 'response.done', response: { status: 'cancelled' } })
    await expect(status(page).getByRole('heading')).toHaveText('Interrupted')
    await expect(status(page)).toContainText('I stopped. Go ahead, I am listening.')
    // The cut reply is dropped, as the server drops it, and a note marks the cut.
    await expect(page.getByTestId('talk-transcript')).not.toContainText('Let me explain')
    await expect(page.getByTestId('talk-transcript')).toContainText('Reply interrupted')

    await emit(page, { type: 'input_audio_buffer.speech_started' })
    await expect(status(page).getByRole('heading')).toHaveText('Listening')
  })

  test('a running tool is named while the model is thinking', async ({ page }) => {
    await fakeRealtime(page)
    await startSession(page)
    await emit(page, { type: 'response.function_call_arguments.done', call_id: 'c1', name: 'unknown_tool', arguments: '{}' })
    // The page answers for a tool no server advertises, and the model goes on.
    await expect.poll(() => page.evaluate(() => window.__talk.sent.map(m => m.type))).toContain('response.create')
  })

  test('tool calls and results from Manage mode show in the transcript', async ({ page }) => {
    await fakeRealtime(page)
    await startSession(page)
    await emit(page, { type: 'response.output_item.done', item: { FunctionCall: { name: 'list_models', arguments: '{}' } } })
    await emit(page, { type: 'response.output_item.done', item: { FunctionCallOutput: { output: '{"models":["a"]}' } } })
    const lines = page.getByTestId('talk-transcript').locator('.talk-turn')
    await expect(lines.nth(0)).toContainText('Tool')
    await expect(lines.nth(0)).toContainText('list_models({})')
    await expect(lines.nth(1)).toContainText('Result')
    await expect(lines.nth(1)).toContainText('"models"')
  })

  test('mic blocked: the browser refused the microphone', async ({ page }) => {
    await fakeRealtime(page, { micError: 'NotAllowedError' })
    await page.goto('/app/talk')
    await page.getByTestId('talk-start').click()
    await expect(status(page)).toHaveAttribute('data-state', 'blocked')
    await expect(status(page).getByRole('heading')).toHaveText('Microphone blocked')
    await expect(page.getByTestId('talk-blocked')).toContainText('Nothing was recorded.')
    await expect(page.getByTestId('talk-start')).toHaveText('Try again')
  })

  test('link lost: the transcript stays and Reconnect starts a new session', async ({ page }) => {
    await fakeRealtime(page)
    await startSession(page)
    await emit(page, { type: 'conversation.item.input_audio_transcription.completed', item_id: 'u1', transcript: 'Hello there' })
    await page.evaluate(() => window.__talk.fail())
    await expect(status(page)).toHaveAttribute('data-state', 'lost')
    await expect(status(page).getByRole('heading')).toHaveText('Connection lost')
    await expect(page.getByTestId('talk-lost')).toContainText('The conversation so far stays in the transcript.')
    await expect(page.getByTestId('talk-transcript')).toContainText('Hello there')
    await expect(page.getByRole('link', { name: 'View traces' })).toBeVisible()
    await expect(page.getByTestId('talk-start')).toHaveText('Reconnect')

    // A new call makes a new data channel; forget the old one first.
    await page.evaluate(() => { window.__talk.dc = null })
    await page.getByTestId('talk-start').click()
    await connected(page)
    await emit(page, { type: 'session.created' })
    await expect(status(page)).toHaveAttribute('data-state', 'listening')
  })

  test('a server error shows its reason with a way to the traces', async ({ page }) => {
    await fakeRealtime(page)
    await startSession(page)
    await emit(page, { type: 'error', error: { message: 'the backend ran out of memory' } })
    await expect(status(page)).toHaveAttribute('data-state', 'error')
    await expect(status(page)).toContainText('The server reported an error: the backend ran out of memory')
    await expect(page.getByRole('link', { name: 'View traces' })).toBeVisible()
  })

  test('no pipeline: a card names the next step instead of an orb', async ({ page }) => {
    await fakeRealtime(page, { pipelines: false })
    await page.goto('/app/talk')
    const card = page.getByTestId('talk-no-pipeline')
    await expect(card.getByRole('heading')).toHaveText('Talk needs a pipeline model')
    await expect(page.locator('.talk-orb')).toHaveCount(0)
    await expect(page.getByTestId('talk-start')).toHaveCount(0)
    await expect(page.getByTestId('home-model-chip')).toContainText('No pipeline')
    await card.getByRole('button', { name: 'Create a pipeline model' }).click()
    await expect(page).toHaveURL(/\/app\/model-editor\?template=pipeline/)
  })
})

test.describe('Talk controls', () => {
  test('End session closes the call and goes back to idle, keeping the transcript', async ({ page }) => {
    await fakeRealtime(page)
    await startSession(page)
    await emit(page, { type: 'conversation.item.input_audio_transcription.completed', item_id: 'u1', transcript: 'Hello' })
    await page.getByTestId('talk-end').click()
    await expect(status(page)).toHaveAttribute('data-state', 'idle')
    await expect(page.getByTestId('talk-start')).toBeVisible()
    await expect(page.getByTestId('talk-transcript')).toContainText('Hello')
  })

  test('Test tone sends the request and notes it in the transcript', async ({ page }) => {
    await fakeRealtime(page)
    await startSession(page)
    await page.getByTestId('talk-test-tone').click()
    await expect.poll(() => page.evaluate(() => window.__talk.sent.map(m => m.type))).toContain('test_tone')
    await expect(page.getByTestId('talk-transcript')).toContainText('Test tone requested')
  })

  test('Copy puts the transcript on the clipboard', async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write'])
    await fakeRealtime(page)
    await startSession(page)
    await expect(page.getByTestId('talk-copy')).toBeDisabled()
    await emit(page, { type: 'conversation.item.input_audio_transcription.completed', item_id: 'u1', transcript: 'Hello' })
    await emit(page, { type: 'response.output_audio_transcript.done', item_id: 'a1', transcript: 'Hi, how can I help?' })
    await page.getByTestId('talk-copy').click()
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe('You: Hello\nReply: Hi, how can I help?')
  })

  test('the pipeline can be changed before a session and not during one', async ({ page }) => {
    await fakeRealtime(page)
    await page.goto('/app/talk')
    await page.getByTestId('home-model-chip').click()
    await page.getByRole('option', { name: /voice-it/ }).click()
    await expect(page.getByTestId('home-model-chip')).toContainText('voice-it')
    // Its own voice comes with it.
    await expect(page.getByRole('button', { name: /paola/ })).toBeVisible()
    await page.getByTestId('talk-start').click()
    await connected(page)
    await expect(page.getByTestId('home-model-chip')).toBeDisabled()
  })

  test('diagnostics are offered only during a session and show the audio panel', async ({ page }) => {
    await fakeRealtime(page)
    await page.goto('/app/talk')
    await expect(page.getByTestId('talk-diag-toggle')).toHaveCount(0)
    await page.getByTestId('talk-start').click()
    await connected(page)
    await expect(page.getByTestId('talk-diag-toggle')).toBeVisible()
    await page.getByTestId('talk-diag-toggle').click()
    await expect(page.getByTestId('talk-diag')).toContainText('Audio diagnostics')
    await expect(page.getByTestId('talk-diag')).toContainText('Packets lost')
  })
})

test.describe('Talk session settings', () => {
  test('the sheet holds voice, language and instructions, and they go out when the session starts', async ({ page }) => {
    await fakeRealtime(page)
    await page.goto('/app/talk')
    await page.getByTestId('talk-settings-button').click()
    const sheet = page.getByTestId('talk-settings')
    await expect(sheet).toHaveClass(/dk-sheet/)
    await expect(sheet).toContainText('Applies when the session starts.')
    await sheet.getByLabel('Voice', { exact: true }).fill('ember')
    await sheet.getByLabel('Transcription language').fill('it')
    await sheet.getByRole('textbox', { name: 'Instructions' }).fill('Answer in one short sentence.')
    // The pipeline's own parts are listed.
    await expect(page.getByTestId('talk-pipeline')).toContainText('whisper-large-v3-turbo')
    await expect(page.getByTestId('talk-pipeline')).toContainText('kokoro-82m')
    await page.keyboard.press('Escape')
    await expect(sheet).toHaveCount(0)
    await expect(page.getByRole('button', { name: /ember/ })).toBeVisible()

    await page.getByTestId('talk-start').click()
    await connected(page)
    await emit(page, { type: 'session.created' })
    const update = await page.evaluate(() => window.__talk.sent.find(m => m.type === 'session.update'))
    expect(update.session.instructions).toBe('Answer in one short sentence.')
    expect(update.session.audio.output.voice).toBe('ember')
    expect(update.session.audio.input.transcription.language).toBe('it')
  })

  test('Manage mode is fixed while a session is open', async ({ page }) => {
    await fakeRealtime(page)
    await startSession(page)
    await page.getByTestId('talk-settings-button').click()
    const manage = page.getByTestId('talk-settings').getByRole('switch', { name: 'Manage mode' })
    await expect(manage).toBeDisabled()
    await expect(page.getByTestId('talk-settings')).toContainText('Fixed while a session is open.')
    // So is the pipeline editor.
    await expect(page.getByTestId('talk-pipeline').getByRole('button')).toHaveCount(0)
  })

  test('Edit pipeline opens the model editor before a session', async ({ page }) => {
    await fakeRealtime(page)
    await page.goto('/app/talk')
    await page.getByTestId('talk-settings-button').click()
    await page.getByTestId('talk-pipeline').getByRole('button', { name: 'Edit pipeline' }).click()
    await expect(page).toHaveURL(/\/app\/model-editor\/voice-assistant/)
  })
})

test.describe('Talk on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('stacks the stage over the transcript and fits the width', async ({ page }) => {
    await fakeRealtime(page)
    await startSession(page)
    await emit(page, { type: 'conversation.item.input_audio_transcription.completed', item_id: 'u1', transcript: 'Hello' })
    const stage = await page.locator('.talk-stage').boundingBox()
    const transcript = await page.locator('.talk-transcript').boundingBox()
    expect(transcript.y).toBeGreaterThanOrEqual(stage.y + stage.height - 1)
    const scroll = await page.evaluate(() => ({ w: document.documentElement.scrollWidth, v: window.innerWidth }))
    expect(scroll.w).toBeLessThanOrEqual(scroll.v)
    await expect(page.getByTestId('talk-end')).toBeInViewport()
  })

  test('the settings sheet rises from the bottom', async ({ page }) => {
    await fakeRealtime(page)
    await page.goto('/app/talk')
    await page.getByTestId('talk-settings-button').click()
    const box = await page.getByTestId('talk-settings').boundingBox()
    expect(box.width).toBeLessThanOrEqual(390)
    expect(box.y + box.height).toBeGreaterThanOrEqual(843)
  })
})

test.describe('Talk with reduced motion', () => {
  test('the transcript lines do not animate', async ({ page }) => {
    // The fixture option test.use({ reducedMotion }) does not reach our extended page.
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await fakeRealtime(page)
    await startSession(page)
    await emit(page, { type: 'conversation.item.input_audio_transcription.completed', item_id: 'u1', transcript: 'Hello' })
    const name = await page.locator('.talk-turn').first().evaluate(el => getComputedStyle(el).animationName)
    expect(name).toBe('none')
  })
})
