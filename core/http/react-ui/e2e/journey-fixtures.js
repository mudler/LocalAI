// Helpers of the journey specs that run against the real test server: the
// mock model is offered to the model picker, and every console error, page
// error and failed request of a journey is collected so a spec can assert the
// journey was quiet.

export async function offerMockModel(page) {
  await page.route('**/api/models/capabilities', route =>
    route.fulfill({ json: { data: [{ id: 'mock-model', capabilities: ['FLAG_CHAT'] }] } }))
}

// Requests the browser cancels on its own when the page navigates away.
const BENIGN = /net::ERR_ABORTED|net::ERR_NETWORK_CHANGED/

// Watches a page. problems() lists what a quiet journey would not have.
export function watchProblems(page, { ignore = [] } = {}) {
  const found = []
  const skip = (text) => ignore.some(re => re.test(text))
  page.on('console', (m) => {
    if (m.type() === 'error' && !skip(m.text())) found.push(`console: ${m.text()}`)
  })
  page.on('pageerror', (e) => found.push(`pageerror: ${e.message}`))
  page.on('requestfailed', (r) => {
    const why = r.failure()?.errorText || ''
    if (!BENIGN.test(why) && !skip(r.url())) found.push(`request failed: ${r.method()} ${new URL(r.url()).pathname} ${why}`)
  })
  page.on('response', (r) => {
    const path = new URL(r.url()).pathname
    if (r.status() >= 400 && !skip(path) && !skip(`${r.status()} ${path}`)) found.push(`http ${r.status()}: ${r.request().method()} ${path}`)
  })
  return { problems: () => [...found] }
}

export const composer = (page) => page.getByRole('textbox', { name: 'Message' })
export const transcript = (page) => page.getByRole('region', { name: 'Conversation' })

export async function startChat(page, text) {
  await offerMockModel(page)
  await page.goto('/app/chats')
  await composer(page).fill(text)
  await page.getByRole('button', { name: 'Send' }).click()
  await page.waitForURL(/\/app\/chats\/[0-9a-f]+$/)
  return page.url().split('/').pop()
}

export function b64(obj) {
  return Buffer.from(JSON.stringify(obj)).toString('base64url')
}
