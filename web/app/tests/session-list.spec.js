import { test, expect } from '@playwright/test'

const fixtures = [
  { name: 'older-alpha', display_name: 'Older alpha', project_alias: 'alpha', branch: 'main', pinned: false, activity_at: '2026-08-18T10:00:00Z', last_opened_at: '2026-08-18T10:00:00Z', target_type: 'host', color: 'blue', status: { priority: 5, color: 'green', badges: [] }, stale: {}, ports: {}, routes: {} },
  { name: 'newer-beta', display_name: 'Newer beta', project_alias: 'beta', branch: 'main', pinned: false, activity_at: '2026-08-18T12:00:00Z', last_opened_at: '2026-08-18T12:00:00Z', target_type: 'host', color: 'cyan', status: { priority: 5, color: 'green', badges: [] }, stale: {}, ports: {}, routes: { api: 'api.localhost' } },
]

async function mockSessionAPI(page) {
  let sessions = structuredClone(fixtures)
  let pinWrites = 0
  let failNextPin = false
  let failNextSessions = false
  await page.addInitScript(() => localStorage.setItem('devx_authed', '1'))
  await page.route('**/api/sessions', async route => {
    if (route.request().method() === 'GET') {
      if (failNextSessions) {
        failNextSessions = false
        await route.fulfill({ status: 500, json: { error: 'temporary load failure' } })
        return
      }
      await route.fulfill({ json: { sessions, stale_summary: { total: sessions.length, clean: 0, needs_review: 0, broken: 0 } } })
      return
    }
    await route.continue()
  })
  await page.route(/\/api\/sessions\/pin\?name=.+$/, async route => {
    const name = new URL(route.request().url()).searchParams.get('name')
    if (failNextPin) {
      failNextPin = false
      await route.fulfill({ status: 500, json: { error: 'pin failed' } })
      return
    }
    const pinned = route.request().method() === 'POST'
    sessions = sessions.map(item => item.name === name ? { ...item, pinned } : item)
    pinWrites++
    await route.fulfill({ status: 204 })
  })
  await page.route('**/api/asks/pending', route => route.fulfill({ json: { requests: [] } }))
  await page.route('**/api/events', route => route.abort())
  return {
    pinWrites: () => pinWrites,
    failNextPin: () => { failNextPin = true },
    failNextSessions: () => { failNextSessions = true },
  }
}

test('recent/projects preference and pinning remain deterministic', async ({ page }) => {
  const state = await mockSessionAPI(page)
  await page.goto('/')

  await expect(page.getByRole('group', { name: 'Session view' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Recent' })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('list', { name: 'Recent' })).toBeVisible()
  const rows = page.getByRole('listitem')
  await expect(rows.nth(0)).toContainText('Newer beta')
  await expect(rows.nth(1)).toContainText('Older alpha')
  await page.getByTitle('services').first().click()
  await expect(page.getByRole('listitem', { name: 'Services for Newer beta' })).toBeVisible()

  await page.getByRole('button', { name: 'Projects' }).click()
  await page.reload()
  await expect(page.getByRole('button', { name: 'Projects' })).toHaveAttribute('aria-pressed', 'true')

  await page.getByRole('button', { name: 'Pin Older alpha' }).click()
  await expect(page.getByText('● Pinned', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Unpin Older alpha' })).toBeFocused()
  await expect(page.getByRole('listitem')).toHaveCount(2)
  expect(state.pinWrites()).toBe(1)

  await page.keyboard.press('Control+p')
  const switcherRows = page.locator('[aria-label="session switcher results"] button')
  await expect(switcherRows.first()).toContainText('Older alpha')
  await page.keyboard.press('Escape')

  state.failNextPin()
  await page.getByRole('button', { name: 'Pin Newer beta' }).click()
  await expect(page.getByRole('button', { name: 'Pin Newer beta' })).toBeVisible()
  await expect(page.getByRole('alert')).toContainText('pin failed')

  const search = page.getByLabel('filter sessions')
  await search.fill('Older ')
  await search.press('Shift+P')
  await expect(search).toHaveValue('Older P')
  expect(state.pinWrites()).toBe(1)
})

test('failed initial load offers a working retry', async ({ page }) => {
  const state = await mockSessionAPI(page)
  state.failNextSessions()
  await page.goto('/')
  await expect(page.getByRole('alert')).toContainText('temporary load failure')
  await page.getByRole('button', { name: 'Retry' }).click()
  await expect(page.getByRole('listitem')).toHaveCount(2)
})

test('mobile rows preserve session names and 44px touch targets', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockSessionAPI(page)
  await page.goto('/')
  const viewButton = page.getByRole('button', { name: 'Recent' })
  const row = page.getByRole('listitem').filter({ hasText: 'Newer beta' })
  const pinButton = row.getByRole('button', { name: 'Pin Newer beta' })
  const nameButton = row.getByRole('button', { name: /^Newer beta/ })
  const projectChip = row.getByText('beta', { exact: true })
  const targetChip = row.getByTitle('Target: host')
  const colorButton = row.getByRole('button', { name: 'change color for newer-beta' })
  const [viewBox, pinBox, nameBox, projectBox] = await Promise.all([
    viewButton.boundingBox(),
    pinButton.boundingBox(),
    nameButton.boundingBox(),
    projectChip.boundingBox(),
  ])
  expect(viewBox.height).toBeGreaterThanOrEqual(44)
  expect(pinBox.height).toBeGreaterThanOrEqual(44)
  expect(pinBox.width).toBeGreaterThanOrEqual(44)
  expect(nameBox.width).toBeGreaterThanOrEqual(128)
  expect(nameBox.height).toBeGreaterThanOrEqual(44)
  expect(projectBox.y).toBeGreaterThan(nameBox.y)
  await expect(targetChip).toBeHidden()
  await expect(colorButton).toBeHidden()
})

test('desktop sidebar rows keep session names readable on two lines', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 })
  await mockSessionAPI(page)
  await page.goto('/')
  const row = page.getByRole('listitem').filter({ hasText: 'Newer beta' })
  const nameButton = row.getByRole('button', { name: /^Newer beta/ })
  await expect(nameButton).toBeVisible()
  const projectChip = row.getByText('beta', { exact: true })
  const targetChip = row.getByTitle('Target: host')
  const colorButton = row.getByRole('button', { name: 'change color for newer-beta' })
  const [nameBox, projectBox] = await Promise.all([
    nameButton.boundingBox(),
    projectChip.boundingBox(),
  ])
  // The desktop sidebar is ~288px wide; a readable name needs most of it.
  expect(nameBox.width).toBeGreaterThanOrEqual(128)
  // Metadata stays on a second line so it cannot squeeze the name.
  expect(projectBox.y).toBeGreaterThan(nameBox.y)
  // Desktop keeps the extra controls that mobile hides.
  await expect(targetChip).toBeVisible()
  await expect(colorButton).toBeVisible()
})

test('projects view keeps single-line rows without metadata lines', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 })
  await mockSessionAPI(page)
  await page.goto('/')
  await page.getByRole('button', { name: 'Projects' }).click()
  const row = page.getByRole('listitem').filter({ hasText: 'Newer beta' })
  const nameButton = row.getByRole('button', { name: /^Newer beta/ })
  await expect(nameButton).toBeVisible()
  const targetChip = row.getByTitle('Target: host')
  const [nameBox, targetBox] = await Promise.all([
    nameButton.boundingBox(),
    targetChip.boundingBox(),
  ])
  // Without project/activity metadata the target chip stays inline with the name.
  expect(targetBox.y).toBeLessThan(nameBox.y + nameBox.height)
  expect(targetBox.y + targetBox.height).toBeGreaterThan(nameBox.y)
})

test('desktop action buttons are hover-revealed uniformly for pinned and unpinned rows', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 })
  await mockSessionAPI(page)
  await page.goto('/')
  await page.getByRole('button', { name: 'Pin Newer beta' }).click()
  await expect(page.getByText('● Pinned', { exact: true })).toBeVisible()
  // Move focus/hover away from the freshly pinned row.
  await page.getByLabel('filter sessions').click()
  const pinnedRow = page.getByRole('listitem').filter({ hasText: 'Newer beta' })
  const unpinnedRow = page.getByRole('listitem').filter({ hasText: 'Older alpha' })
  const actionsIn = (row) => row.locator('div').filter({ has: page.getByRole('button', { name: /Pin|Unpin/ }) }).last()
  for (const row of [pinnedRow, unpinnedRow]) {
    await expect.poll(async () => actionsIn(row).evaluate(el => getComputedStyle(el).opacity)).toBe('0')
  }
  await unpinnedRow.hover()
  await expect.poll(async () => actionsIn(unpinnedRow).evaluate(el => getComputedStyle(el).opacity)).toBe('1')
})
