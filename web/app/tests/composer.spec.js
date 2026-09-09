import { test, expect } from '@playwright/test'

const sessionFixtures = [
  { name: 'alpha', display_name: 'Alpha', project_alias: 'alpha', branch: 'main', pinned: false, activity_at: '2026-09-04T12:00:00Z', last_opened_at: '2026-09-04T12:00:00Z', target_type: 'host', color: 'blue', status: { priority: 5, color: 'green', badges: [] }, stale: {}, ports: {}, routes: {} },
  { name: 'beta', display_name: 'Beta', project_alias: 'beta', branch: 'main', pinned: false, activity_at: '2026-09-04T11:00:00Z', last_opened_at: '2026-09-04T11:00:00Z', target_type: 'host', color: 'cyan', status: { priority: 5, color: 'green', badges: [] }, stale: {}, ports: {}, routes: {} },
]

async function mockBaseAPI(page, { sendImpl } = {}) {
  const sentRequests = []
  await page.addInitScript(() => localStorage.setItem('devx_authed', '1'))
  await page.route('**/api/sessions', route => {
    if (route.request().method() !== 'GET') return route.continue()
    return route.fulfill({ json: { sessions: sessionFixtures, stale_summary: { total: 2, clean: 0, needs_review: 0, broken: 0 } } })
  })
  await page.route('**/api/asks/pending', route => route.fulfill({ json: { requests: [] } }))
  await page.route('**/api/settings', route => route.fulfill({
    json: { artifact_trigger_key: 'Ctrl+Space', default_session_target: 'host', usage_enabled: false },
  }))
  await page.route('**/api/usage', route => route.fulfill({ json: { state: 'disabled', message: '', updated_at: '', providers: [] } }))
  await page.route('**/api/events', route => route.abort())

  // Terminal-view plumbing needed to reach the composer without a real backend.
  await page.route('**/api/windows*', route => route.fulfill({ json: { windows: [] } }))
  await page.route('**/api/active-pane*', route => route.fulfill({ json: { pane: null } }))
  await page.route('**/api/terminal/activity-receipt', route => route.fulfill({ json: { receipt: 'test-receipt' } }))
  await page.route('**/api/sessions/activity', route => route.fulfill({ status: 204 }))
  await page.route('**/api/refresh*', route => route.fulfill({ status: 200, json: {} }))
  await page.route('**/api/terminal/prewarm', route => route.fulfill({ json: { ready: false } }))
  await page.route(/^https?:\/\/[^/]+\/terminal\//, route => route.fulfill({ contentType: 'text/html', body: '<html><body></body></html>' }))

  await page.route('**/api/terminal/send-input', async route => {
    const body = route.request().postDataJSON()
    sentRequests.push(body)
    if (sendImpl) return sendImpl(route, body)
    return route.fulfill({ status: 200, json: {} })
  })

  return { sentRequests }
}

async function openSession(page, name = 'Alpha') {
  await page.getByRole('button', { name: new RegExp('^' + name) }).click()
  await page.getByLabel('terminal input composer').waitFor()
}

test.describe('composer draft persistence — mobile (390px)', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('unsent text in the mobile composer survives a reload', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await openSession(page)

    const composer = page.getByLabel('terminal input composer')
    await composer.fill('unsent draft text')
    // Debounced persistence: wait past the 400ms window before reloading.
    await page.waitForTimeout(500)

    await page.reload()
    await openSession(page)
    await expect(page.getByLabel('terminal input composer')).toHaveValue('unsent draft text')
  })

  test('drafts in two different sessions persist and restore independently', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')

    await openSession(page, 'Alpha')
    await page.getByLabel('terminal input composer').fill('alpha draft')
    await page.waitForTimeout(500)

    // Go back to the session list and open Beta instead.
    await page.goto('/')
    await openSession(page, 'Beta')
    await page.getByLabel('terminal input composer').fill('beta draft')
    await page.waitForTimeout(500)

    await page.reload()
    await openSession(page, 'Beta')
    await expect(page.getByLabel('terminal input composer')).toHaveValue('beta draft')

    await page.goto('/')
    await openSession(page, 'Alpha')
    await expect(page.getByLabel('terminal input composer')).toHaveValue('alpha draft')
  })

  test('sending a prompt clears the composer and fires the POST body; the history button was already visible before sending', async ({ page }) => {
    const { sentRequests } = await mockBaseAPI(page)
    await page.goto('/')
    await openSession(page)

    // The ⏱ button is always visible in the mobile docked composer (not
    // gated on history.length > 0) so privacy prefs stay reachable even with
    // an empty/disabled history.
    await expect(page.getByTitle('prompt history')).toBeVisible()

    const composer = page.getByLabel('terminal input composer')
    await composer.fill('hello terminal')
    await page.getByTitle('send to terminal').click()

    await expect(composer).toHaveValue('')
    expect(sentRequests).toHaveLength(1)
    expect(sentRequests[0]).toMatchObject({ session: 'alpha', text: 'hello terminal', submit: true })

    await expect(page.getByTitle('prompt history')).toBeVisible()
  })

  test('opening the history sheet blurs the textarea first so the on-screen keyboard dismisses before the sheet opens', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await openSession(page)

    const composer = page.getByLabel('terminal input composer')
    await composer.click()
    await expect(composer).toBeFocused()

    await page.getByTitle('prompt history').click()
    await expect(page.getByRole('dialog', { name: /^Prompt history for/ })).toBeVisible()
    await expect(composer).not.toBeFocused()
  })

  test('tapping ⏱ shows the sent prompt; tapping it restores into the composer; a second recall appends a newline', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await openSession(page)

    const composer = page.getByLabel('terminal input composer')
    await composer.fill('first sent prompt')
    await page.getByTitle('send to terminal').click()
    await expect(composer).toHaveValue('')

    await page.getByTitle('prompt history').click()
    const sheet = page.getByRole('dialog', { name: /^Prompt history for/ })
    await expect(sheet).toBeVisible()
    await expect(sheet).toContainText('first sent prompt')

    await sheet.getByText('first sent prompt').click()
    await expect(sheet).toBeHidden()
    await expect(composer).toHaveValue('first sent prompt')

    // Recall into a non-empty composer appends on a new line rather than
    // replacing what's being typed.
    await page.getByTitle('prompt history').click()
    await page.getByRole('dialog', { name: /^Prompt history for/ }).getByText('first sent prompt').click()
    await expect(composer).toHaveValue('first sent prompt\nfirst sent prompt')
  })

  test('a send that is still in flight when the user switches sessions is credited to the original session', async ({ page }) => {
    // Hold the send-input response until the test releases it, so we can
    // switch sessions while the promise is unresolved.
    let releaseSend
    const sendGate = new Promise(resolve => { releaseSend = resolve })
    await mockBaseAPI(page, {
      sendImpl: async (route) => { await sendGate; return route.fulfill({ status: 200, json: {} }) },
    })
    await page.goto('/')

    // Seed a draft in Beta so we can prove it is not clobbered by Alpha's send.
    await openSession(page, 'Beta')
    await page.getByLabel('terminal input composer').fill('beta draft stays')
    await page.waitForTimeout(500)
    await page.goto('/')

    await openSession(page, 'Alpha')
    const composer = page.getByLabel('terminal input composer')
    await composer.fill('sent from alpha')
    await page.getByTitle('send to terminal').click()

    // Mid-flight: jump to Beta via the quick switcher (desktop sidebar / Ctrl+P
    // keep PromptComposer mounted, so sessionName changes under the await).
    await page.keyboard.press('Control+p')
    await page.getByLabel('session switcher search').fill('beta')
    await page.getByLabel('session switcher results').getByRole('button', { name: /Beta/ }).click()
    await expect(composer).toHaveValue('beta draft stays')

    releaseSend()
    await page.waitForTimeout(300)

    // Beta's draft is intact and Beta has no history from Alpha's send.
    await expect(composer).toHaveValue('beta draft stays')
    await page.getByTitle('prompt history').click()
    const betaSheet = page.getByRole('dialog', { name: 'Prompt history for beta' })
    await expect(betaSheet).toContainText('no prompt history yet')
    await betaSheet.getByLabel('Close').click()

    // Alpha's history has the prompt, its draft is cleared, and no stale
    // "may already have been sent" warning remains.
    await page.goto('/')
    await openSession(page, 'Alpha')
    await expect(composer).toHaveValue('')
    await expect(page.getByText(/may already have been sent/)).toHaveCount(0)
    await page.getByTitle('prompt history').click()
    await expect(page.getByRole('dialog', { name: 'Prompt history for alpha' })).toContainText('sent from alpha')
  })

  test('typing the next prompt while a send is in flight preserves that new draft when the send resolves', async ({ page }) => {
    let releaseSend
    const sendGate = new Promise(resolve => { releaseSend = resolve })
    await mockBaseAPI(page, {
      sendImpl: async (route) => { await sendGate; return route.fulfill({ status: 200, json: {} }) },
    })
    await page.goto('/')
    await openSession(page, 'Alpha')

    const composer = page.getByLabel('terminal input composer')
    await composer.fill('first prompt being sent')
    await page.getByTitle('send to terminal').click()
    await composer.fill('next prompt typed while waiting')
    await page.waitForTimeout(500) // let the new draft persist before send resolves

    releaseSend()
    await page.waitForTimeout(300)
    await expect(composer).toHaveValue('next prompt typed while waiting')

    await page.reload()
    await openSession(page, 'Alpha')
    await expect(composer).toHaveValue('next prompt typed while waiting')
    await page.getByTitle('prompt history').click()
    await expect(page.getByRole('dialog', { name: 'Prompt history for alpha' })).toContainText('first prompt being sent')
  })

  test('a failed send keeps the text, shows an error, and does not record history', async ({ page }) => {
    await mockBaseAPI(page, {
      sendImpl: (route) => route.fulfill({ status: 500, json: { error: 'send failed' } }),
    })
    await page.goto('/')
    await openSession(page)

    const composer = page.getByLabel('terminal input composer')
    await composer.fill('will not land')
    await page.getByTitle('send to terminal').click()

    await expect(composer).toHaveValue('will not land')
    await expect(page.locator('text=/failed/i')).toBeVisible()
    // The ⏱ button is always visible (privacy prefs must stay reachable),
    // but the sheet it opens shows the empty state since no send landed.
    await page.getByTitle('prompt history').click()
    await expect(page.getByRole('dialog', { name: /^Prompt history for/ })).toContainText('no prompt history yet')
  })

  test('a draft restored with sending:true shows the may-already-be-sent warning; dismiss hides it and persists false', async ({ page }) => {
    await mockBaseAPI(page)
    // Seed localStorage once, before the app's own scripts run, but only for
    // the very first navigation — addInitScript re-runs on every subsequent
    // navigation (including our own reload below), which would otherwise
    // re-seed sending:true and mask a real persistence bug in the dismiss path.
    await page.addInitScript(() => {
      if (window.__seededSending) return
      window.__seededSending = true
      localStorage.setItem('devx_composer_v1:alpha', JSON.stringify({ draft: 'maybe already sent', sending: true }))
    })
    await page.goto('/')
    await openSession(page)

    const composer = page.getByLabel('terminal input composer')
    await expect(composer).toHaveValue('maybe already sent')
    const warning = page.getByText('this may already have been sent — check the terminal before resending')
    await expect(warning).toBeVisible()

    await page.getByLabel('dismiss may-already-have-been-sent warning').click()
    await expect(warning).toBeHidden()
    // window.__seededSending does not survive a real navigation, so assert
    // directly against localStorage instead of relying on a second reload.
    const raw = await page.evaluate(() => localStorage.getItem('devx_composer_v1:alpha'))
    expect(JSON.parse(raw).sending).toBeFalsy()
  })

  test('pagehide flushes an in-flight debounced draft immediately, without waiting for the 400ms debounce', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await openSession(page)

    const composer = page.getByLabel('terminal input composer')
    await composer.fill('typed just before backgrounding')
    // No waitForTimeout: dispatch pagehide immediately, well inside the 400ms
    // debounce window, to prove the flush — not the debounce — persisted it.
    await page.evaluate(() => window.dispatchEvent(new Event('pagehide')))

    const raw = await page.evaluate(() => localStorage.getItem('devx_composer_v1:alpha'))
    expect(JSON.parse(raw).draft).toBe('typed just before backgrounding')
  })

  test('clear history requires two taps: the first arms confirmation without clearing, the second clears and keeps the sheet open', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await openSession(page)

    const composer = page.getByLabel('terminal input composer')
    await composer.fill('a prompt to clear later')
    await page.getByTitle('send to terminal').click()
    await expect(page.getByTitle('prompt history')).toBeVisible()

    await page.getByTitle('prompt history').click()
    const sheet = page.getByRole('dialog', { name: /^Prompt history for/ })
    await expect(sheet).toContainText('a prompt to clear later')

    // First tap arms the two-step confirmation; history must survive it.
    const clearButton = sheet.getByRole('button', { name: '[clear]', exact: true })
    await clearButton.click()
    await expect(sheet.getByText('[confirm clear?]')).toBeVisible()
    await expect(sheet).toContainText('a prompt to clear later')

    // Second tap actually clears; the sheet stays open showing the empty state.
    await sheet.getByText('[confirm clear?]').click()
    await expect(sheet).toContainText('no prompt history yet')
    await expect(sheet).toBeVisible()

    // ⏱ remains visible after closing — always-visible, not gated on history.
    await sheet.getByLabel('Close').click()
    await expect(sheet).toBeHidden()
    await expect(page.getByTitle('prompt history')).toBeVisible()
  })

  test('the clear confirmation disarms after 5s so a stray later tap does not clear', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await openSession(page)

    const composer = page.getByLabel('terminal input composer')
    await composer.fill('a prompt that should survive')
    await page.getByTitle('send to terminal').click()

    await page.getByTitle('prompt history').click()
    const sheet = page.getByRole('dialog', { name: /^Prompt history for/ })
    const clearButton = sheet.getByRole('button', { name: '[clear]', exact: true })
    await clearButton.click()
    await expect(sheet.getByText('[confirm clear?]')).toBeVisible()

    await page.waitForTimeout(5200)
    await expect(sheet.getByText('[clear]')).toBeVisible()
    await expect(sheet).toContainText('a prompt that should survive')
  })

  test('closing the history sheet via Escape disarms an in-progress clear confirmation', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await openSession(page)

    const composer = page.getByLabel('terminal input composer')
    await composer.fill('escape disarm check')
    await page.getByTitle('send to terminal').click()

    await page.getByTitle('prompt history').click()
    const sheet = page.getByRole('dialog', { name: /^Prompt history for/ })
    await sheet.getByRole('button', { name: '[clear]', exact: true }).click()
    await expect(sheet.getByText('[confirm clear?]')).toBeVisible()

    await page.keyboard.press('Escape')
    await expect(sheet).toBeHidden()

    await page.getByTitle('prompt history').click()
    await expect(page.getByRole('dialog', { name: /^Prompt history for/ }).getByText('[clear]')).toBeVisible()
  })

  test('the history trigger button regains focus after an ordinary sheet close', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await openSession(page)

    const composer = page.getByLabel('terminal input composer')
    await composer.fill('focus return check')
    await page.getByTitle('send to terminal').click()

    const historyButton = page.getByTitle('prompt history')
    await historyButton.click()
    const sheet = page.getByRole('dialog', { name: /^Prompt history for/ })
    await sheet.getByLabel('Close').click()
    await expect(sheet).toBeHidden()
    await expect(historyButton).toBeFocused()
  })

  test('the ⏱ history button matches the existing composer action buttons\' touch-target size and stays reachable', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await openSession(page)

    const composer = page.getByLabel('terminal input composer')
    await composer.fill('touch target check')
    await page.getByTitle('send to terminal').click()

    // ⏱ is a new addition to a row of pre-existing action buttons (¶, ↵,
    // ⌨) whose touch-target sizing predates this feature; parity with those
    // (not an absolute 44px, which they don't meet either) is the correct bar
    // here so this test doesn't silently mask a real regression in ⏱'s own
    // sizing without demanding an unrelated resize of pre-existing controls.
    const historyBox = await page.getByTitle('prompt history').boundingBox()
    const pasteBox = await page.getByTitle('paste into terminal without submitting').boundingBox()
    expect(historyBox.height).toBeCloseTo(pasteBox.height, 0)
    expect(historyBox.width).toBeGreaterThanOrEqual(pasteBox.width - 2)
  })

  test('the local storage footer renders both toggles, default checked, with clear explanatory copy and stable aria-labels', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await openSession(page)

    await page.getByTitle('prompt history').click()
    const sheet = page.getByRole('dialog', { name: /^Prompt history for/ })
    await expect(sheet).toContainText('local storage')
    await expect(sheet).toContainText('saved only in this browser')

    const draftToggle = sheet.getByLabel('save drafts on this device')
    const historyToggle = sheet.getByLabel('keep sent prompt history')
    await expect(draftToggle).toBeChecked()
    await expect(historyToggle).toBeChecked()
  })

  test('turning history off immediately clears history and keeps the sheet open; turning it back on re-enables future recording', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await openSession(page)

    const composer = page.getByLabel('terminal input composer')
    await composer.fill('a prompt before disabling history')
    await page.getByTitle('send to terminal').click()

    await page.getByTitle('prompt history').click()
    const sheet = page.getByRole('dialog', { name: /^Prompt history for/ })
    await expect(sheet).toContainText('a prompt before disabling history')

    await sheet.getByLabel('keep sent prompt history').uncheck()
    await expect(sheet).toContainText('no prompt history yet')
    await expect(sheet).toBeVisible()

    await sheet.getByLabel('keep sent prompt history').check()
    await sheet.getByLabel('Close').click()

    await composer.fill('a prompt after re-enabling history')
    await page.getByTitle('send to terminal').click()
    await page.getByTitle('prompt history').click()
    await expect(page.getByRole('dialog', { name: /^Prompt history for/ })).toContainText('a prompt after re-enabling history')
  })

  test('turning drafts off purges the persisted draft but does not clear text currently in the textarea', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await openSession(page)

    const composer = page.getByLabel('terminal input composer')
    await composer.fill('typed text that must survive turning drafts off')
    await page.waitForTimeout(500)
    let raw = await page.evaluate(() => localStorage.getItem('devx_composer_v1:alpha'))
    expect(JSON.parse(raw).draft).toBe('typed text that must survive turning drafts off')

    await page.getByTitle('prompt history').click()
    const sheet = page.getByRole('dialog', { name: /^Prompt history for/ })
    await sheet.getByLabel('save drafts on this device').uncheck()

    // Persisted draft is purged...
    raw = await page.evaluate(() => localStorage.getItem('devx_composer_v1:alpha'))
    expect(raw === null || !JSON.parse(raw).draft).toBeTruthy()
    // ...but the in-memory textarea content is untouched.
    await expect(composer).toHaveValue('typed text that must survive turning drafts off')
  })

  test('composer stays usable and sending still works when localStorage is unavailable', async ({ page }) => {
    const { sentRequests } = await mockBaseAPI(page)
    // Simulate private-mode/quota-blocked storage: every setItem throws.
    // devx_authed itself is set via document.cookie-independent localStorage
    // in the app's login flow, so seed it directly before the override lands.
    await page.addInitScript(() => {
      localStorage.setItem('devx_authed', '1')
      Storage.prototype.setItem = function () { throw new Error('QuotaExceededError') }
    })
    await page.goto('/')
    await openSession(page)

    const composer = page.getByLabel('terminal input composer')
    await composer.fill('works without storage')
    await page.getByTitle('send to terminal').click()

    await expect(composer).toHaveValue('')
    expect(sentRequests).toHaveLength(1)
    expect(sentRequests[0]).toMatchObject({ text: 'works without storage' })
  })
})

test.describe('composer draft persistence — desktop overlay (1280px)', () => {
  test.use({ viewport: { width: 1280, height: 800 } })

  async function openOverlay(page, name = 'Alpha') {
    await page.getByRole('button', { name: new RegExp('^' + name) }).click()
    await page.keyboard.press('Control+k')
    await page.getByLabel('terminal input composer').and(page.locator(':visible')).waitFor()
  }

  test('draft persists across reload on the desktop overlay, but no history button is shown (v1 deferred)', async ({ page }) => {
    await mockBaseAPI(page)
    await page.goto('/')
    await openOverlay(page)

    const composer = page.getByLabel('terminal input composer').and(page.locator(':visible'))
    await composer.fill('desktop overlay draft')
    // Wait for the observable debounce result rather than sleeping 500ms for a
    // 400ms timer; loaded CI workers can delay timers beyond that margin.
    await expect.poll(() => page.evaluate(() => {
      const stored = localStorage.getItem('devx_composer_v1:alpha')
      return stored ? JSON.parse(stored).draft : ''
    })).toBe('desktop overlay draft')

    await page.reload()
    await openOverlay(page)
    await expect(page.getByLabel('terminal input composer').and(page.locator(':visible'))).toHaveValue('desktop overlay draft')

    // History UI is mobile (docked) only in v1; the desktop overlay never
    // shows a ⏱ button even after a send. The docked composer (which now
    // always renders ⏱) is still present at this viewport width behind an
    // `lg:hidden` wrapper, so assert non-visibility rather than absence from
    // the DOM.
    await page.getByRole('button', { name: /^send ⌘↵/ }).click()
    await expect(page.getByTitle('prompt history')).not.toBeVisible()
  })
})
