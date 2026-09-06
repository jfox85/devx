import { test, expect } from '@playwright/test'

const sessionFixtures = [
  { name: 'alpha', display_name: 'Alpha', project_alias: 'alpha', branch: 'main', pinned: false, activity_at: '2026-09-04T12:00:00Z', last_opened_at: '2026-09-04T12:00:00Z', target_type: 'host', color: 'blue', status: { priority: 5, color: 'green', badges: [] }, stale: {}, ports: {}, routes: {} },
]

function usageFixture(overrides = {}) {
  return {
    state: 'ok',
    message: '',
    updated_at: '2026-09-04T16:31:53Z',
    providers: [
      {
        id: 'claude-main',
        provider: 'claude',
        label: 'Claude',
        state: 'ok',
        error: '',
        source: 'openusage',
        observed_at: '2026-09-04T16:31:23Z',
        primary: { key: 'session', label: '5h', remaining: 0.56, resets_at: '2026-09-04T20:50:00Z', period_seconds: 18000 },
        windows: [
          { key: 'session', label: '5-hour window', scope: 'account', remaining: 0.56, resets_at: '2026-09-04T20:50:00Z', period_seconds: 18000 },
          { key: 'weekly', label: 'Weekly', scope: 'account', remaining: 0.15, resets_at: '2026-09-04T16:59:59Z', period_seconds: 604800 },
          { key: 'model:fable:weekly', label: 'Fable', scope: 'model', remaining: 0.45, resets_at: '2026-09-07T16:59:59Z', period_seconds: 604800, reset_inferred: true },
        ],
      },
      {
        id: 'codex-main',
        provider: 'codex',
        label: 'Codex',
        state: 'ok',
        error: '',
        source: 'native',
        observed_at: '2026-09-04T16:31:23Z',
        primary: { key: 'weekly', label: 'wk', remaining: 0, resets_at: '2026-09-07T03:03:02Z', period_seconds: 604800 },
        windows: [
          { key: 'weekly', label: 'Weekly', scope: 'account', remaining: 0, resets_at: '2026-09-07T03:03:02Z', period_seconds: 604800 },
          { key: 'model:spark:short', label: 'Spark', scope: 'model', remaining: 0.55, resets_at: '2026-09-04T19:36:00Z', period_seconds: 18000 },
        ],
      },
    ],
    ...overrides,
  }
}

async function mockBaseAPI(page, { usageEnabled = true, usage = usageFixture() } = {}) {
  let currentUsage = usage
  let refreshCalls = 0
  await page.addInitScript(() => localStorage.setItem('devx_authed', '1'))
  await page.route('**/api/sessions', route => {
    if (route.request().method() !== 'GET') return route.continue()
    return route.fulfill({ json: { sessions: sessionFixtures, stale_summary: { total: 1, clean: 0, needs_review: 0, broken: 0 } } })
  })
  await page.route('**/api/asks/pending', route => route.fulfill({ json: { requests: [] } }))
  await page.route('**/api/settings', route => route.fulfill({
    json: { artifact_trigger_key: 'Ctrl+Space', default_session_target: 'host', usage_enabled: usageEnabled },
  }))
  await page.route('**/api/usage', route => {
    if (route.request().method() !== 'GET') return route.continue()
    return route.fulfill({ json: currentUsage })
  })
  await page.route('**/api/usage/refresh', async route => {
    refreshCalls++
    await route.fulfill({ json: currentUsage })
  })
  await page.route('**/api/events', route => route.abort())

  // Terminal-view plumbing needed to reach the mobile ☰ menu without a real
  // backend: the iframe target and the calls Terminal.svelte fires on open.
  await page.route('**/api/windows*', route => route.fulfill({ json: { windows: [] } }))
  await page.route('**/api/active-pane*', route => route.fulfill({ json: { pane: null } }))
  await page.route('**/api/terminal/activity-receipt', route => route.fulfill({ json: { receipt: 'test-receipt' } }))
  await page.route('**/api/sessions/activity', route => route.fulfill({ status: 204 }))
  await page.route('**/api/refresh*', route => route.fulfill({ status: 200, json: {} }))
  await page.route('**/api/terminal/prewarm', route => route.fulfill({ json: { ready: false } }))
  // Matches the ttyd iframe target only (leading slash), never the SPA's own
  // /src/lib/terminal/*.svelte module imports.
  await page.route(/^https?:\/\/[^/]+\/terminal\//, route => route.fulfill({ contentType: 'text/html', body: '<html><body></body></html>' }))

  return {
    refreshCalls: () => refreshCalls,
    setUsage: (next) => { currentUsage = next },
  }
}

test.describe('provider usage widget — desktop (1280px)', () => {
  test.use({ viewport: { width: 1280, height: 720 } })

  // Usage lives in the session header, which only exists once a session is
  // open, so every case here opens one first.
  async function openSession(page) {
    await page.getByRole('button', { name: /^Alpha/ }).click()
    await page.locator('[aria-label="Session facts"]').waitFor()
  }

  test('the modal lists all windows including model-scoped pools', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await openSession(page)

    await page.getByRole('button', { name: /^Provider usage:/ }).click()
    const modal = page.getByRole('dialog', { name: 'Provider usage details' })
    await expect(modal).toBeVisible()

    const claudeSection = page.getByRole('region', { name: 'Claude usage' })
    await expect(claudeSection).toContainText('5-hour window')
    await expect(claudeSection).toContainText('Weekly')
    await expect(claudeSection).toContainText('Fable') // model-scoped window
    await expect(claudeSection).toContainText('reset inferred')

    const codexSection = page.getByRole('region', { name: 'Codex usage' })
    await expect(codexSection).toContainText('Spark') // model-scoped window

    // Escape is handled on the dialog, which only sees events bubbling from its
    // own descendants, so dispatch the key from inside it. Targeting the dialog
    // rather than page.keyboard avoids depending on OS window focus, which is
    // not guaranteed under Playwright's parallel workers.
    await modal.press('Escape')
    await expect(modal).toBeHidden()
  })

  test('refresh button fires POST /api/usage/refresh', async ({ page }) => {
    const state = await mockBaseAPI(page)
    await page.goto('/')
    await openSession(page)

    await page.getByRole('button', { name: /^Provider usage:/ }).click()
    const [request] = await Promise.all([
      page.waitForRequest(req => req.url().includes('/api/usage/refresh') && req.method() === 'POST'),
      page.getByRole('button', { name: 'Refresh provider usage' }).click(),
    ])
    expect(request.method()).toBe('POST')
    expect(state.refreshCalls()).toBe(1)
  })

  test('a provider error is reported in the header and detailed in the modal', async ({ page }) => {
    const usage = usageFixture()
    usage.providers[1] = {
      ...usage.providers[1],
      state: 'error',
      error: 'no usage snapshot',
      primary: null,
      windows: [],
    }
    await mockBaseAPI(page, { usage })
    await page.goto('/')
    await openSession(page)

    // The row has no space for an error string, so the failing provider is
    // dropped from the meters and the modal carries the message.
    const pill = page.getByRole('button', { name: /^Provider usage:/ })
    await expect(pill).toContainText('claude')
    await expect(pill).toContainText('56%')
    await expect(pill).not.toContainText('codex')

    await pill.click()
    const dialog = page.getByRole('dialog', { name: 'Provider usage details' })
    await expect(dialog.getByText('no usage snapshot')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Refresh provider usage' })).toBeEnabled()
  })

  test('the u hotkey opens the modal from the session list', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await page.getByRole('button', { name: /^Alpha/ }).waitFor()

    await page.keyboard.press('u')
    await expect(page.getByRole('dialog', { name: 'Provider usage details' })).toBeVisible()
  })

  test('the session list no longer carries a usage strip', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await page.getByRole('button', { name: /^Alpha/ }).waitFor()

    // With no session open there is no header, so nothing renders usage; the
    // sidebar space belongs to the session list.
    await expect(page.getByRole('button', { name: /^Provider usage:/ })).toHaveCount(0)
    await expect(page.getByText('usage · redline not running')).toHaveCount(0)
  })
})

test.describe('provider usage summary in the session header — desktop (1440px)', () => {
  test.use({ viewport: { width: 1440, height: 900 } })

  // The header only exists once a session is open, so each case opens one.
  async function openSession(page) {
    await page.getByRole('button', { name: /^Alpha/ }).click()
    await page.locator('[aria-label="Session facts"]').waitFor()
  }

  test('the header pill shows each provider between the facts and the Status toggle', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await openSession(page)

    // The pill is the sibling of the facts strip, i.e. the open space between
    // the session facts and the Status toggle.
    const pill = page.locator('[aria-label="Session facts"] ~ button[aria-label^="Provider usage:"]')
    await expect(pill).toBeVisible()
    await expect(pill).toHaveAccessibleName('Provider usage: Claude 56% remaining, 5h window; Codex 0% remaining, weekly window')
    await expect(pill).toContainText('claude')
    await expect(pill).toContainText('56%')
    await expect(pill).toContainText('5h')
    await expect(pill).toContainText('codex')
    await expect(pill).toContainText('0%')
    await expect(pill).toContainText('wk')

    // ok tone (56% >= 35%) on claude, danger tone (0% < 15%) on codex — a
    // stable data-tone contract, not a Tailwind class-name assertion.
    await expect(pill.locator('[data-tone="ok"]')).toHaveCount(1)
    await expect(pill.locator('[data-tone="danger"]')).toHaveCount(1)
  })

  test('clicking the header pill opens the detail modal', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await openSession(page)

    await page.locator('[aria-label="Session facts"] ~ button[aria-label^="Provider usage:"]').click()
    await expect(page.getByRole('dialog', { name: 'Provider usage details' })).toBeVisible()
  })

  test('the header pill degrades to a muted marker when Redline is unavailable', async ({ page }) => {
    await mockBaseAPI(page, {
      usage: { state: 'unavailable', message: 'Redline is not running on this host', updated_at: '', providers: [] },
    })
    await page.goto('/')
    await openSession(page)

    const pill = page.locator('[aria-label="Session facts"] ~ button[aria-label^="Provider usage"]')
    await expect(pill).toContainText('n/a')
  })

  test('the header pill is not mounted when usage is disabled', async ({ page }) => {
    await mockBaseAPI(page, { usageEnabled: false })
    await page.goto('/')
    await openSession(page)

    await expect(page.locator('[aria-label="Session facts"] ~ button[aria-label^="Provider usage"]')).toHaveCount(0)
  })
})

test.describe('provider usage widget — mobile (390px)', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('the sessions view is free of usage chrome', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await page.getByRole('button', { name: /^Alpha/ }).waitFor()

    // v2: usage reaches mobile through the actions menu only, so the list keeps
    // its vertical space. The session header (and its pill) is desktop-only.
    await expect(page.getByRole('button', { name: /^Provider usage:/ })).toHaveCount(0)
  })

  test('mobile menu → Provider usage opens the modal from the terminal view', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')

    await page.getByRole('button', { name: /^Alpha/ }).click()
    const menuButton = page.getByTitle('terminal and artifact actions')
    await expect(menuButton).toBeVisible()

    await menuButton.click()
    await page.getByRole('menuitem', { name: 'Provider usage' }).click()

    await expect(page.getByRole('dialog', { name: 'Provider usage details' })).toBeVisible()
  })
})
