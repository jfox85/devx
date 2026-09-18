import { test, expect } from '@playwright/test'

// Regression coverage for the desktop keyboard proxy's error reporting.
//
// On desktop the terminal iframe is cross-origin (SPA on wails://, terminal on
// the private loopback origin), so WKWebView never hands keyboard focus to the
// frame and Terminal.svelte relays every keystroke over HTTP through a hidden
// parent-side textarea. When that relay was rejected the failure was swallowed
// by the promise queue, so typing stopped with nothing shown — indistinguishable
// from the terminal losing focus. These tests pin the reporting behaviour.

const sessionFixtures = [
  { name: 'alpha', display_name: 'Alpha', project_alias: 'alpha', branch: 'main', pinned: false, activity_at: '2026-09-04T12:00:00Z', last_opened_at: '2026-09-04T12:00:00Z', target_type: 'host', color: 'blue', status: { priority: 5, color: 'green', badges: [] }, stale: {}, ports: {}, routes: {} },
  { name: 'beta', display_name: 'Beta', project_alias: 'beta', branch: 'main', pinned: false, activity_at: '2026-09-04T11:00:00Z', last_opened_at: '2026-09-04T11:00:00Z', target_type: 'host', color: 'cyan', status: { priority: 5, color: 'green', badges: [] }, stale: {}, ports: {}, routes: {} },
]

async function mockTerminalAPI(page, { sendInputImpl } = {}) {
  const sent = []
  await page.addInitScript(() => localStorage.setItem('devx_authed', '1'))
  await page.route('**/api/sessions', route => {
    if (route.request().method() !== 'GET') return route.continue()
    return route.fulfill({ json: { sessions: sessionFixtures, stale_summary: { total: sessionFixtures.length, clean: 0, needs_review: 0, broken: 0 } } })
  })
  await page.route('**/api/asks/pending', route => route.fulfill({ json: { requests: [] } }))
  await page.route('**/api/settings', route => route.fulfill({
    json: { artifact_trigger_key: 'Ctrl+Space', default_session_target: 'host', usage_enabled: false },
  }))
  await page.route('**/api/usage', route => route.fulfill({ json: { state: 'disabled', message: '', updated_at: '', providers: [] } }))
  await page.route('**/api/events', route => route.abort())
  await page.route('**/api/windows*', route => route.fulfill({ json: { windows: [] } }))
  await page.route('**/api/active-pane*', route => route.fulfill({ json: { pane: null } }))
  await page.route('**/api/terminal/activity-receipt', route => route.fulfill({ json: { receipt: 'test-receipt' } }))
  await page.route('**/api/sessions/activity', route => route.fulfill({ status: 204 }))
  await page.route('**/api/refresh*', route => route.fulfill({ status: 200, json: {} }))
  await page.route('**/api/terminal/prewarm', route => route.fulfill({ json: { ready: false } }))
  await page.route(/^https?:\/\/[^/]+\/terminal\//, route => route.fulfill({ contentType: 'text/html', body: '<html><body></body></html>' }))

  await page.route('**/api/terminal/send-input', async route => {
    const body = route.request().postDataJSON()
    sent.push(body)
    if (sendInputImpl) return sendInputImpl(route, body)
    return route.fulfill({ status: 200, json: {} })
  })

  return { sent }
}

// Drive the hidden keyboard proxy directly: it is the element that receives
// typing on desktop when focus cannot enter the cross-origin terminal frame.
// It is visually hidden (1px, opacity-0), so wait for "attached", not "visible".
function proxyLocator(page) {
  return page.locator('textarea[aria-hidden="true"]')
}

async function typeViaProxy(page, text) {
  const proxy = proxyLocator(page)
  await proxy.evaluate((el, value) => {
    el.value = value
    el.dispatchEvent(new Event('input', { bubbles: true }))
  }, text)
}

async function openSessionAndTypeViaProxy(page, text) {
  await page.getByRole('button', { name: /^Alpha/ }).click()
  await proxyLocator(page).waitFor({ state: 'attached' })
  await typeViaProxy(page, text)
}

test.describe('desktop keyboard proxy error reporting', () => {
  test('a rate-limited keystroke relay surfaces an actionable toast instead of failing silently', async ({ page }) => {
    await mockTerminalAPI(page, {
      sendInputImpl: route => route.fulfill({ status: 429, json: { error: 'rate limit exceeded' } }),
    })
    await page.goto('/')
    await openSessionAndTypeViaProxy(page, 'hello')

    // The 429 must be reported, not swallowed. Before the fix the rejection was
    // unhandled and nothing appeared.
    await expect(page.getByText(/rate limited/i)).toBeVisible({ timeout: 5000 })
  })

  test('a non-429 relay failure reports the underlying error', async ({ page }) => {
    await mockTerminalAPI(page, {
      sendInputImpl: route => route.fulfill({ status: 500, json: { error: 'tmux unavailable' } }),
    })
    await page.goto('/')
    await openSessionAndTypeViaProxy(page, 'hello')

    await expect(page.getByText(/Terminal input failed/i)).toBeVisible({ timeout: 5000 })
  })

  test('the queue survives a failure so later keystrokes are still delivered', async ({ page }) => {
    let calls = 0
    const { sent } = await mockTerminalAPI(page, {
      sendInputImpl: route => {
        calls += 1
        // Fail only the first batch; the chain must keep running afterwards.
        if (calls === 1) return route.fulfill({ status: 429, json: { error: 'rate limit exceeded' } })
        return route.fulfill({ status: 200, json: {} })
      },
    })
    await page.goto('/')
    await openSessionAndTypeViaProxy(page, 'first')
    await expect(page.getByText(/rate limited/i)).toBeVisible({ timeout: 5000 })

    await typeViaProxy(page, 'second')

    // A dead queue would never issue this request.
    await expect.poll(() => sent.length, { timeout: 5000 }).toBeGreaterThan(1)
  })

  test('a successful relay shows no error toast', async ({ page }) => {
    const { sent } = await mockTerminalAPI(page)
    await page.goto('/')

    // typeViaProxy only buffers; the 75ms debounce then issues the request and
    // the queue callbacks run after it resolves. Asserting immediately would
    // pass before anything had a chance to fail, so wait for the relay to
    // actually complete first.
    const relayed = page.waitForResponse(r => r.url().includes('/api/terminal/send-input'))
    await openSessionAndTypeViaProxy(page, 'hello')
    await relayed
    await expect.poll(() => sent.length, { timeout: 5000 }).toBeGreaterThan(0)

    await expect(page.getByText(/rate limited/i)).toHaveCount(0)
    await expect(page.getByText(/Terminal input failed/i)).toHaveCount(0)
  })

})
