import { test, expect } from '@playwright/test'

const sessionFixtures = [
  { name: 'alpha', display_name: 'Alpha', project_alias: 'alpha', branch: 'main', pinned: false, activity_at: '2026-09-04T12:00:00Z', last_opened_at: '2026-09-04T12:00:00Z', target_type: 'host', color: 'blue', status: { priority: 5, color: 'green', badges: [] }, stale: {}, ports: {}, routes: {} },
]

async function mockBaseAPI(page) {
  await page.addInitScript(() => localStorage.setItem('devx_authed', '1'))
  await page.route('**/api/sessions', route => {
    if (route.request().method() !== 'GET') return route.continue()
    return route.fulfill({ json: { sessions: sessionFixtures, stale_summary: { total: 1, clean: 0, needs_review: 0, broken: 0 } } })
  })
  await page.route('**/api/asks/pending', route => route.fulfill({ json: { requests: [] } }))
  await page.route('**/api/settings', route => route.fulfill({ json: { artifact_trigger_key: 'Ctrl+Space', default_session_target: 'host', usage_enabled: false } }))
  await page.route('**/api/usage', route => route.fulfill({ json: { state: 'disabled', message: '', updated_at: '', providers: [] } }))
  await page.route('**/api/events', route => route.abort())
  await page.route('**/api/windows*', route => route.fulfill({ json: { windows: [] } }))
  await page.route('**/api/active-pane*', route => route.fulfill({ json: { pane: null } }))
  await page.route('**/api/terminal/activity-receipt', route => route.fulfill({ json: { receipt: 'test-receipt' } }))
  await page.route('**/api/sessions/activity', route => route.fulfill({ status: 204 }))
  await page.route('**/api/refresh*', route => route.fulfill({ status: 200, json: {} }))
  await page.route('**/api/terminal/prewarm', route => route.fulfill({ json: { ready: false } }))
  await page.route(/^https?:\/\/[^/]+\/terminal\//, route => route.fulfill({ contentType: 'text/html', body: '<html><body></body></html>' }))
  await page.route('**/api/terminal/send-input', async route => route.fulfill({ status: 200, json: {} }))
}

test.use({ viewport: { width: 1280, height: 800 } })

test('desktop: no way at all to reach the prefs toggles (v1 gap)', async ({ page }) => {
  await mockBaseAPI(page)
  await page.goto('/')
  await page.getByRole('button', { name: /^Alpha/ }).click()
  await page.getByLabel('terminal input composer').first().waitFor()
  await page.keyboard.press('Control+k')
  await page.waitForTimeout(300)
  const historyBtn = page.getByTitle('prompt history')
  const count = await historyBtn.count()
  console.log('history buttons in DOM:', count);
  for (let i = 0; i < count; i++) {
    console.log(i, 'visible:', await historyBtn.nth(i).isVisible());
  }
})
