import { test, expect } from '@playwright/test'

const fixtures = [
  { name: 'older-alpha', display_name: 'Older alpha', project_alias: 'alpha', branch: 'main', pinned: false, activity_at: '2026-08-18T10:00:00Z', last_opened_at: '2026-08-18T10:00:00Z', target_type: 'host', color: 'blue', status: { priority: 5, color: 'green', badges: [] }, stale: {}, ports: {}, routes: {} },
  { name: 'newer-beta', display_name: 'Newer beta', project_alias: 'beta', branch: 'main', pinned: false, activity_at: '2026-08-18T12:00:00Z', last_opened_at: '2026-08-18T12:00:00Z', target_type: 'host', color: 'cyan', status: { priority: 5, color: 'green', badges: [] }, stale: {}, ports: {}, routes: {} },
]

async function mockSessionAPI(page) {
  let sessions = structuredClone(fixtures)
  let pinWrites = 0
  await page.addInitScript(() => localStorage.setItem('devx_authed', '1'))
  await page.route('**/api/sessions', async route => {
    if (route.request().method() === 'GET') {
      await route.fulfill({ json: { sessions, stale_summary: { total: sessions.length, clean: 0, needs_review: 0, broken: 0 } } })
      return
    }
    await route.continue()
  })
  await page.route('**/api/sessions/pin?name=*', async route => {
    const name = new URL(route.request().url()).searchParams.get('name')
    const pinned = route.request().method() === 'POST'
    sessions = sessions.map(item => item.name === name ? { ...item, pinned } : item)
    pinWrites++
    await route.fulfill({ status: 204 })
  })
  await page.route('**/api/events', route => route.abort())
  return { pinWrites: () => pinWrites }
}

test('recent/projects preference and pinning remain deterministic', async ({ page }) => {
  const state = await mockSessionAPI(page)
  await page.goto('/')

  await expect(page.getByRole('button', { name: 'Recent' })).toHaveAttribute('aria-pressed', 'true')
  const rows = page.getByRole('listitem')
  await expect(rows.nth(0)).toContainText('Newer beta')
  await expect(rows.nth(1)).toContainText('Older alpha')

  await page.getByRole('button', { name: 'Projects' }).click()
  await page.reload()
  await expect(page.getByRole('button', { name: 'Projects' })).toHaveAttribute('aria-pressed', 'true')

  await page.getByRole('button', { name: 'Pin Older alpha' }).click()
  await expect(page.getByText('● Pinned', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Unpin Older alpha' })).toBeFocused()
  expect(state.pinWrites()).toBe(1)

  const search = page.getByLabel('filter sessions')
  await search.fill('Older ')
  await search.press('Shift+P')
  await expect(search).toHaveValue('Older P')
  expect(state.pinWrites()).toBe(1)
})

test('mobile view and pin controls meet 44px touch targets', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockSessionAPI(page)
  await page.goto('/')
  const viewButton = page.getByRole('button', { name: 'Recent' })
  const pinButton = page.getByRole('button', { name: 'Pin Newer beta' })
  const [viewBox, pinBox] = await Promise.all([viewButton.boundingBox(), pinButton.boundingBox()])
  expect(viewBox.height).toBeGreaterThanOrEqual(44)
  expect(pinBox.height).toBeGreaterThanOrEqual(44)
  expect(pinBox.width).toBeGreaterThanOrEqual(44)
})
