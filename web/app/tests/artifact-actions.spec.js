import { test, expect } from '@playwright/test'

// 1x1 PNG
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64')

const session = { name: 'alpha', display_name: 'Alpha', project_alias: 'alpha', branch: 'main', pinned: false, activity_at: '2026-09-04T12:00:00Z', last_opened_at: '2026-09-04T12:00:00Z', target_type: 'host', color: 'blue', status: { priority: 5, color: 'green', badges: [] }, stale: {}, ports: {}, routes: {}, artifact_count: 3 }

const beta = { ...session, name: 'beta', display_name: 'Beta', project_alias: 'beta', artifact_count: 0 }

const artifacts = [
  { id: 'shot-1', title: 'Login screen', type: 'screenshot', file: 'screenshots/login.png', url: '/sessions/alpha/artifacts/screenshots/login.png', path: '.artifacts/screenshots/login.png', created: '2026-09-04T12:03:00Z', agent: 'pi' },
  { id: 'zip-1', title: 'Bundle', type: 'other', file: 'bundle.zip', url: '/sessions/alpha/artifacts/bundle.zip', path: '.artifacts/bundle.zip', created: '2026-09-04T12:02:00Z', agent: 'pi' },
  { id: 'shot-2', title: 'Dashboard', type: 'screenshot', file: 'screenshots/dash.png', url: '/sessions/alpha/artifacts/screenshots/dash.png', path: '.artifacts/screenshots/dash.png', created: '2026-09-04T12:01:00Z', agent: 'pi' },
]

async function mockAPI(page, sessions = [session]) {
  await page.addInitScript(() => {
    localStorage.setItem('devx_authed', '1')
    window.__opened = []
    window.open = (url) => { window.__opened.push(url); return null }
  })
  await page.route('**/api/sessions', route => route.request().method() === 'GET'
    ? route.fulfill({ json: { sessions, stale_summary: { total: sessions.length, clean: 0, needs_review: 0, broken: 0 } } })
    : route.continue())
  await page.route('**/api/asks/pending', route => route.fulfill({ json: { requests: [] } }))
  await page.route('**/api/settings', route => route.fulfill({ json: { artifact_trigger_key: 'Ctrl+Space', default_session_target: 'host', usage_enabled: false } }))
  await page.route('**/api/usage', route => route.fulfill({ json: { state: 'disabled', message: '', updated_at: '', providers: [] } }))
  await page.route('**/api/events', route => route.abort())
  await page.route('**/api/windows*', route => route.fulfill({ json: { windows: [] } }))
  await page.route('**/api/active-pane*', route => route.fulfill({ json: { pane: null } }))
  await page.route('**/api/terminal/activity-receipt', route => route.fulfill({ json: { receipt: 'r' } }))
  await page.route('**/api/sessions/activity', route => route.fulfill({ status: 204 }))
  await page.route('**/api/refresh*', route => route.fulfill({ status: 200, json: {} }))
  await page.route('**/api/terminal/prewarm', route => route.fulfill({ json: { ready: false } }))
  await page.route(/^https?:\/\/[^/]+\/terminal\//, route => route.fulfill({ contentType: 'text/html', body: '<html><body></body></html>' }))
  await page.route('**/api/artifacts?*', route => route.fulfill({ json: { session: 'alpha', artifacts } }))
  await page.route('**/api/artifacts/seen*', route => route.fulfill({ status: 204 }))
  await page.route('**/api/artifacts/focus*', route => route.fulfill({ status: 204 }))
  await page.route('**/sessions/alpha/artifacts/**', route => {
    const url = new URL(route.request().url())
    if (url.searchParams.get('download') === '1') {
      return route.fulfill({ body: PNG, headers: { 'Content-Type': 'image/png', 'Content-Disposition': 'attachment; filename="login.png"' } })
    }
    return route.fulfill({ body: PNG, contentType: 'image/png' })
  })
}

async function openArtifacts(page) {
  await page.goto('/')
  await page.getByRole('button', { name: /^Alpha/ }).click()
  await page.getByTitle('Terminal — alpha').waitFor({ state: 'attached' })
  await page.evaluate(() => window.dispatchEvent(new CustomEvent('devx:toggleArtifacts')))
  await expect(page.getByLabel('artifacts panel')).toBeVisible()
}

for (const [label, viewport] of [['desktop', { width: 1280, height: 800 }], ['mobile', { width: 390, height: 844 }]]) {
  test.describe(`artifact actions — ${label}`, () => {
    test.use({ viewport })

    test('view fullscreen, step through images, close with Escape and Back', async ({ page }) => {
      await mockAPI(page)
      await openArtifacts(page)
      const pane = page.getByLabel('artifacts panel')
      await pane.getByRole('button', { name: '[view]' }).click()

      const viewer = page.getByRole('dialog', { name: 'artifact viewer' })
      await expect(viewer).toBeVisible()
      await expect(viewer.getByTestId('artifact-viewer-title')).toHaveText('Login screen')
      const box = await viewer.boundingBox()
      expect(box.width).toBe(viewport.width)
      expect(box.height).toBe(viewport.height)

      // Next skips the non-viewable zip.
      await viewer.getByRole('button', { name: 'next artifact' }).click()
      await expect(viewer.getByTestId('artifact-viewer-title')).toHaveText('Dashboard')
      await page.keyboard.press('ArrowRight')
      await expect(viewer.getByTestId('artifact-viewer-title')).toHaveText('Login screen')

      await page.keyboard.press('Escape')
      await expect(viewer).toBeHidden()
      await expect(pane).toBeVisible()

      // Browser/Android Back closes only the viewer, not the artifact pane.
      await pane.getByRole('button', { name: /view Login screen fullscreen/ }).click()
      await expect(viewer).toBeVisible()
      await page.goBack()
      await expect(viewer).toBeHidden()
      await expect(pane).toBeVisible()
    })

    test('switching sessions with the viewer open leaves no stale history entry', async ({ page }) => {
      await mockAPI(page, [session, beta])
      await openArtifacts(page)
      const pane = page.getByLabel('artifacts panel')
      await pane.getByRole('button', { name: '[view]' }).click()
      const viewer = page.getByRole('dialog', { name: 'artifact viewer' })
      await expect(viewer).toBeVisible()
      const depthWithViewer = await page.evaluate(() => history.length)

      // The quick switcher (Cmd/Ctrl+P) works over the viewer.
      await page.keyboard.press('Control+p')
      await page.getByLabel('session switcher search').fill('beta')
      await page.getByLabel('session switcher search').press('Enter')
      await expect(page.getByLabel('session switcher search')).toHaveCount(0)
      await expect(page.getByTitle('Terminal — beta')).toBeAttached()
      await expect(viewer).toBeHidden()

      // The viewer's entry was popped on the switch: the current history entry
      // is no longer a devx modal, so the next Back is a real navigation and
      // is not silently consumed.
      await expect.poll(() => page.evaluate(() => history.state?.devxModal ?? null)).toBeNull()
      expect(await page.evaluate(() => history.length)).toBeLessThanOrEqual(depthWithViewer + 1)
    })

    test('open uses a new tab and download requests an attachment', async ({ page }) => {
      await mockAPI(page)
      await openArtifacts(page)
      const pane = page.getByLabel('artifacts panel')

      await pane.getByRole('button', { name: '[open]' }).click()
      await expect.poll(() => page.evaluate(() => window.__opened)).toEqual(['/sessions/alpha/artifacts/screenshots/login.png'])

      const downloadEvent = page.waitForEvent('download')
      await pane.getByRole('button', { name: '[download]' }).click()
      const download = await downloadEvent
      expect(download.suggestedFilename()).toBe('login.png')
      const dlURL = new URL(download.url())
      expect(dlURL.pathname + dlURL.search).toBe('/sessions/alpha/artifacts/screenshots/login.png?download=1')

      // Same actions are available inside the fullscreen viewer.
      await pane.getByRole('button', { name: '[view]' }).click()
      const viewer = page.getByRole('dialog', { name: 'artifact viewer' })
      const second = page.waitForEvent('download')
      await viewer.getByRole('button', { name: 'Download' }).click()
      await second
      await viewer.getByRole('button', { name: 'Open' }).click()
      await expect.poll(() => page.evaluate(() => window.__opened.length)).toBe(2)
    })

    test('desktop app routes open/download through the native host', async ({ page }) => {
      await mockAPI(page)
      await page.addInitScript(() => {
        window.__DEVX_DESKTOP = { terminalBase: '', terminalToken: '' }
        window.__hostCalls = []
        window.go = { main: { Host: {
          OpenArtifact: async (s, id) => { window.__hostCalls.push(['open', s, id]) },
          DownloadArtifact: async (s, id) => { window.__hostCalls.push(['download', s, id]); return '/Users/me/Downloads/login.png' },
        } } }
      })
      await openArtifacts(page)
      const pane = page.getByLabel('artifacts panel')
      await pane.getByRole('button', { name: '[open]' }).click()
      await pane.getByRole('button', { name: '[download]' }).click()
      await expect(pane.getByRole('status')).toContainText('Saved to /Users/me/Downloads/login.png')
      expect(await page.evaluate(() => window.__hostCalls)).toEqual([['open', 'alpha', 'shot-1'], ['download', 'alpha', 'shot-1']])
      expect(await page.evaluate(() => window.__opened)).toEqual([])
    })
  })
}
