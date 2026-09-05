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

  test('strip renders both providers with correct percent/labels and tone colors', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')

    const strip = page.getByRole('button', { name: /^Provider usage:/ })
    await expect(strip).toBeVisible()
    // The outer button's accessible name summarizes every provider's numbers
    // and window (per-row aria-labels were removed as misleading — this is
    // now the one name AT actually announces).
    await expect(strip).toHaveAccessibleName('Provider usage: Claude 56% remaining, 5h window; Codex 0% remaining, weekly window')
    await expect(strip).toContainText('claude')
    await expect(strip).toContainText('56%')
    await expect(strip).toContainText('5h')
    await expect(strip).toContainText('codex')
    await expect(strip).toContainText('0%')
    await expect(strip).toContainText('wk')

    // ok tone (56% >= 35%) on claude, danger tone (0% < 15%) on codex — a
    // stable data-tone contract, not a Tailwind class-name assertion.
    const claudePercent = strip.locator('span', { hasText: '56%' })
    await expect(claudePercent).toHaveAttribute('data-tone', 'ok')
    const codexPercent = strip.locator('span', { hasText: '0%' })
    await expect(codexPercent).toHaveAttribute('data-tone', 'danger')
  })

  test('clicking the strip opens the modal with all windows including a model-scoped one', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')

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

    await page.keyboard.press('Escape')
    await expect(modal).toBeHidden()
  })

  test('refresh button fires POST /api/usage/refresh', async ({ page }) => {
    const state = await mockBaseAPI(page)
    await page.goto('/')

    await page.getByRole('button', { name: /^Provider usage:/ }).click()
    const [request] = await Promise.all([
      page.waitForRequest(req => req.url().includes('/api/usage/refresh') && req.method() === 'POST'),
      page.getByRole('button', { name: 'Refresh provider usage' }).click(),
    ])
    expect(request.method()).toBe('POST')
    expect(state.refreshCalls()).toBe(1)
  })

  test('unavailable state shows a single dim line on desktop', async ({ page }) => {
    await mockBaseAPI(page, {
      usage: { state: 'unavailable', message: 'Redline is not running on this host', updated_at: '', providers: [] },
    })
    await page.goto('/')

    await expect(page.getByText('usage · redline not running')).toBeVisible()
  })

  test('a provider error renders one red line on the strip and the message in the modal', async ({ page }) => {
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

    // Strip: the failing provider collapses to a single error line while the
    // healthy provider keeps rendering its meter.
    await expect(page.getByText('codex: no usage snapshot')).toBeVisible()
    await expect(page.getByText('56%')).toBeVisible()
    await expect(page.locator('[data-tone]')).toHaveCount(1)

    // Modal: the full error text is shown and refresh stays available.
    await page.getByRole('button', { name: /^Provider usage:/ }).click()
    const dialog = page.getByRole('dialog', { name: 'Provider usage details' })
    await expect(dialog.getByText('no usage snapshot')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Refresh provider usage' })).toBeEnabled()
  })

  test('the strip shows a loading line until the first usage payload arrives', async ({ page }) => {
    let release = () => {}
    const gate = new Promise(resolve => { release = resolve })
    await mockBaseAPI(page)
    // Re-route /api/usage to hang until we release it, so the loading state is
    // observable rather than a sub-frame flash.
    await page.route('**/api/usage', async route => {
      if (route.request().method() !== 'GET') return route.continue()
      await gate
      return route.fulfill({ json: usageFixture() })
    })
    await page.goto('/')

    await expect(page.getByText('usage · …')).toBeVisible()
    release()
    await expect(page.getByText('56%')).toBeVisible()
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

    // Two buttons now carry the summarizing name (sidebar strip + header pill);
    // the header one is the sibling of the facts strip.
    const pill = page.locator('[aria-label="Session facts"] ~ button[aria-label^="Provider usage:"]')
    await expect(pill).toBeVisible()
    await expect(pill).toContainText('claude')
    await expect(pill).toContainText('56%')
    await expect(pill).toContainText('codex')
    await expect(pill).toContainText('0%')
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

  test('strip is visible in the sessions view', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await expect(page.getByRole('button', { name: /^Provider usage:/ })).toBeVisible()
  })

  test('the unavailable line is hidden on mobile to save vertical space', async ({ page }) => {
    await mockBaseAPI(page, {
      usage: { state: 'unavailable', message: 'Redline is not running on this host', updated_at: '', providers: [] },
    })
    await page.goto('/')

    // Rendered in the DOM (desktop shares this markup) but hidden below lg.
    await expect(page.getByText('usage · redline not running')).toBeHidden()
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
