import { test, expect } from './coverage-fixtures.js'
import { box, emit, menu, openSlashChat } from './slash-fixtures.js'

// The slash commands of the chat composer with the API stubbed and the event
// stream faked: the skills and the saved agents in the menu with argument
// completion, a skill sent as a marker with its chip, /compact and /clear with
// their progress, markers and meter, and the /help and /about cards.

const open = openSlashChat

test.describe('Chat slash commands', () => {
  test('the menu lists the skills with their source and the new commands', async ({ page }) => {
    await open(page)
    await box(page).fill('/')
    await expect(menu(page)).toBeVisible()
    for (const id of ['model', 'new', 'find', 'compact', 'clear', 'help', 'about', 'skill', 'agent', 'skill-review', 'skill-pdf', 'skill-slides', 'skill-new']) {
      await expect(page.getByTestId(`home-slash-${id}`)).toHaveCount(1)
    }
    await expect(page.locator('#home-slash-group-skills')).toHaveText('Skills and agents')
    await expect(page.getByTestId('home-slash-skill-pdf')).toContainText('built-in')
    await expect(page.getByTestId('home-slash-skill-slides')).toContainText('add-on')
    await expect(page.getByTestId('home-slash-skill-review')).toContainText('yours')
    // A skill named like a command is reached with /skill, and says so.
    await expect(page.getByTestId('home-slash-skill-new')).toContainText('/skill new')
    await expect(page.getByTestId('home-slash-skill-new')).toContainText('only with /skill')

    await box(page).fill('/sl')
    await expect(page.getByTestId('home-slash-skill-slides')).toBeVisible()
    await expect(page.getByTestId('home-slash-compact')).toHaveCount(0)
    await expect(box(page)).toHaveAttribute('aria-expanded', 'true')
    await expect(page.getByRole('option', { selected: true })).toHaveCount(1)
  })

  test('choosing a skill and sending carries the marker and shows the chip', async ({ page }) => {
    const api = await open(page)
    await box(page).fill('/rev')
    await page.getByTestId('home-slash-skill-review').click()
    await expect(box(page)).toHaveValue('/review ')
    await box(page).pressSequentially('look at the diff')
    await box(page).press('Enter')
    await expect.poll(() => api.sent.length).toBe(1)
    expect(api.sent[0]).toEqual({ text: 'look at the diff', skill: 'review' })
    const chip = page.getByTestId('message-invoked')
    await expect(chip).toHaveText('Skill: review')
    await expect(page.getByTestId('chat-message').last()).toContainText('look at the diff')
    await expect(page.getByTestId('chat-message').last()).not.toContainText('/review')
  })

  test('arguments complete with the keyboard: /skill and /agent', async ({ page }) => {
    const api = await open(page)
    await box(page).fill('/skill p')
    await expect(page.getByTestId('home-slash-arg-skill-pdf')).toBeVisible()
    await expect(page.getByTestId('home-slash-arg-skill-review')).toHaveCount(0)
    await box(page).press('Tab')
    await expect(box(page)).toHaveValue('/skill pdf ')
    await expect(menu(page)).toHaveCount(0)

    await box(page).fill('/age')
    await box(page).press('Tab')
    await expect(box(page)).toHaveValue('/agent ')
    await expect(page.getByTestId('home-slash-arg-agent-rev1')).toContainText('Checks drafts for tone')
    await box(page).pressSequentially('res')
    await expect(page.getByTestId('home-slash-arg-agent-rev1')).toHaveCount(0)
    await box(page).press('Enter')
    await expect(box(page)).toHaveValue('/agent Researcher ')
    await box(page).pressSequentially('find three sources')
    await box(page).press('Enter')
    await expect.poll(() => api.sent.length).toBe(1)
    expect(api.sent[0]).toEqual({ text: 'find three sources', agent: 'rev2' })
    await expect(page.getByTestId('message-invoked')).toHaveText('Agent: Researcher')
  })

  test('an agent command offers to turn saved agents on when they are off', async ({ page }) => {
    const api = await open(page, { agentsOn: false })
    await box(page).fill('/agent ')
    const enable = page.getByTestId('home-slash-arg-enable-agents')
    await expect(enable).toContainText('Turn on saved agents for this chat')
    await enable.click()
    await expect.poll(() => api.builtins.length).toBe(1)
    expect(api.builtins[0].on).toContain('saved_agents')
    await expect(page.getByTestId('home-slash-arg-agent-rev1')).toBeVisible()
  })

  test('/model suggests the models and picks one', async ({ page }) => {
    await open(page)
    await box(page).fill('/model gem')
    await expect(page.getByTestId('home-slash-arg-model-gemma-3-12b')).toBeVisible()
    await expect(page.getByTestId('home-slash-arg-model-qwen3-8b')).toHaveCount(0)
  })

  test('/compact shows its progress, then the marker, and the meter drops', async ({ page }) => {
    const api = await open(page)
    await emit(page, 'usage', { kind: 'usage', usage: { used: 9000, limit: 32768, completion: 10 } })
    await expect(page.getByTestId('chat-context-meter')).toContainText('27%')
    await box(page).fill('/compact')
    await box(page).press('Enter')
    await expect(page.getByTestId('chat-state')).toHaveAttribute('data-state', 'compacting')
    await expect(page.getByTestId('chat-state')).toContainText('Compacting the context')
    await expect.poll(() => typeof api.compactRelease).toBe('function')
    await page.evaluate(() => {})
    api.compactRelease()
    const marker = page.getByTestId('chat-marker-compacted')
    await expect(marker).toBeVisible()
    await expect(marker).toContainText('Context compacted')
    await expect(page.getByTestId('chat-state')).toHaveAttribute('data-state', 'ready')
    await expect(page.getByTestId('chat-context-meter')).toContainText('9%')
  })

  test('/clear draws the divider and the meter drops to nothing', async ({ page }) => {
    await open(page)
    await emit(page, 'usage', { kind: 'usage', usage: { used: 9000, limit: 32768, completion: 10 } })
    await box(page).fill('/cle')
    await box(page).press('Enter')
    const divider = page.getByTestId('chat-marker-cleared')
    await expect(divider).toBeVisible()
    await expect(divider).toContainText('Context cleared')
    await expect(divider).toHaveAttribute('role', 'separator')
    await expect(page.getByTestId('chat-context-meter')).toContainText('0%')
    // Nothing is left for the model: the context commands leave the menu.
    await box(page).fill('/c')
    await expect(page.getByTestId('home-slash-clear')).toHaveCount(0)
    await expect(page.getByTestId('home-slash-compact')).toHaveCount(0)
    await expect(page.getByTestId('home-slash-chats')).toBeVisible()
  })

  test('/help and /about draw a card that closes', async ({ page }) => {
    await open(page)
    await box(page).fill('/help')
    await box(page).press('Enter')
    const help = page.getByTestId('chat-help-card')
    await expect(help).toBeVisible()
    await expect(help).toContainText('/compact')
    await expect(help).toContainText('/agent')
    await expect(help).toContainText('4 skills')
    await help.getByTestId('chat-cmdcard-close').click()
    await expect(help).toHaveCount(0)

    await box(page).fill('/about')
    await box(page).press('Enter')
    const about = page.getByTestId('chat-about-card')
    await expect(about).toContainText('qwen3-8b')
    await expect(about).toContainText('4 skills')
    await expect(about).not.toContainText('c1')
  })

  test('a typed skill command that does not exist says so and sends nothing', async ({ page }) => {
    const api = await open(page)
    await box(page).fill('/skill nope do it')
    await box(page).press('Escape')
    await box(page).press('Enter')
    await expect(page.getByText('This chat has no skill named "nope".')).toBeVisible()
    expect(api.sent).toHaveLength(0)
  })
})
