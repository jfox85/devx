#!/usr/bin/env python3
"""Deterministic browser checks for the Operator Console v3 static prototype."""
from pathlib import Path
from playwright.sync_api import sync_playwright, expect

URL = "http://127.0.0.1:4181/"
VIEWPORTS = [
    (1440, 1000), (1280, 900), (1100, 800), (1024, 768),
    (900, 800), (768, 1024), (700, 800), (601, 800),
    (540, 760), (390, 844), (360, 780), (320, 700),
]
SHOT_DIR = Path(__file__).parent


def assert_shell(page, width, height):
    metrics = page.evaluate("""() => ({
      innerHeight, innerWidth,
      docHeight: document.documentElement.scrollHeight,
      docWidth: document.documentElement.scrollWidth,
      bodyHeight: document.body.scrollHeight,
      bodyWidth: document.body.scrollWidth,
      terminalOverflow: getComputedStyle(document.querySelector('.terminal')).overflowY,
      bodyRowHeight: document.querySelector('.body-row').getBoundingClientRect().height
    })""")
    assert metrics["docHeight"] == height, (width, height, metrics)
    assert metrics["bodyHeight"] == height, (width, height, metrics)
    assert metrics["docWidth"] == width, (width, height, metrics)
    assert metrics["bodyWidth"] == width, (width, height, metrics)
    assert metrics["terminalOverflow"] == "auto", (width, height, metrics)
    assert metrics["bodyRowHeight"] > 0, (width, height, metrics)


def new_page(browser, width, height, touch=False):
    page = browser.new_page(viewport={"width": width, "height": height}, has_touch=touch)
    errors = []
    page.on("console", lambda msg: errors.append(f"console:{msg.type}:{msg.text}") if msg.type == "error" else None)
    page.on("pageerror", lambda exc: errors.append(f"pageerror:{exc}"))
    page.goto(URL)
    expect(page.locator("#session-title")).to_have_text("Operator console refresh")
    return page, errors


def output_reader_check(page, width, height, trigger):
    trigger.click()
    reader = page.locator("#output-reader")
    expect(reader).to_be_visible()
    assert reader.bounding_box() == {"x": 0, "y": 0, "width": width, "height": height}
    expect(page.locator("#output-reader-heading")).to_be_focused()
    assert page.locator(".shell").evaluate("el => el.inert")
    body = page.locator("#output-reader-body")
    expect(body).to_contain_text("operator output remains available")
    scroll = body.evaluate("el => ({scrollHeight:el.scrollHeight, clientHeight:el.clientHeight})")
    assert scroll["scrollHeight"] > scroll["clientHeight"], scroll

    # The heading is the reverse boundary, and Close wraps forward to it.
    page.keyboard.press("Shift+Tab")
    expect(page.locator("#output-reader-body")).to_be_focused()
    page.keyboard.press("Tab")
    expect(page.locator("#output-reader-heading")).to_be_focused()

    # Blob navigation must create a real popup containing the transcript.
    with page.expect_popup() as popup_info:
        page.locator("#output-open-tab").click()
    popup = popup_info.value
    popup.wait_for_load_state()
    assert popup.url.startswith("blob:"), popup.url
    expect(popup.locator("body")).to_contain_text("devx status --session jf-ui-refresh")
    popup.close()

    page.keyboard.press("Escape")
    expect(reader).to_be_hidden()
    expect(trigger).to_be_focused()
    assert not page.locator(".shell").evaluate("el => el.inert")


def assert_fleet_contract(page):
    expect(page.locator("#total-count")).to_have_text("25")
    rows = page.locator(".session")
    assert rows.count() == 25
    semantics = rows.evaluate_all("""els => els.map(el => ({
      text: el.innerText, label: el.getAttribute('aria-label'), status: el.dataset.status,
      state: el.querySelector('.session-state')?.getAttribute('aria-label'),
      artifacts: el.querySelector('.badge.blue')?.getAttribute('aria-label')
    }))""")
    assert all(item["status"] in item["state"] for item in semantics), semantics
    assert all("artifacts" in item["artifacts"] for item in semantics), semantics
    assert all(item["status"] in item["label"] and "artifacts" in item["label"] for item in semantics), semantics

    # Production grouping: projects by name, sessions by status priority then title.
    projects = page.locator(".project").evaluate_all("els => els.map(el => el.dataset.project)")
    assert projects == sorted(projects), projects
    priority = {"repair": 0, "attention": 1, "dirty": 2, "active": 3, "idle": 4}
    groups = page.locator(".project").evaluate_all("""els => els.map(el => [...el.querySelectorAll('.session')].map(row => ({status:row.dataset.status,title:row.dataset.title})))""")
    for group in groups:
        expected = sorted(group, key=lambda item: (priority[item["status"]], item["title"]))
        assert group == expected, (group, expected)


def compact_artifact_fullscreen_check(page, width, height):
    if width <= 600:
        page.locator("#mobile-artifacts").click()
    else:
        page.locator("#mobile-actions").click()
        page.locator("#actions-menu [data-action='artifacts']").click()

    panel = page.locator("#artifact-panel")
    trigger = page.locator("#artifact-menu-trigger")
    menu = page.locator("#artifact-actions-menu")
    expect(panel).to_have_class("artifact-panel open")
    expect(trigger).to_have_attribute("aria-controls", "artifact-actions-menu")
    expect(menu).to_have_attribute("aria-labelledby", "artifact-menu-trigger")

    trigger.click()
    expect(trigger).to_have_attribute("aria-expanded", "true")
    expect(menu).to_be_visible()
    expect(menu.locator("[role='menuitem']").first).to_be_focused()
    menu.locator("[data-artifact-command='fullscreen']").click()

    expect(panel).to_have_attribute("role", "dialog")
    expect(panel).to_have_attribute("aria-modal", "true")
    assert panel.bounding_box() == {"x": 0, "y": 0, "width": width, "height": height}
    assert page.locator(".shell").evaluate("el => el.inert")
    for selector in ["#navigator", ".topbar", ".facts", ".windowbar", ".terminal-pane", "#status-panel", ".mobile-bottom"]:
        assert page.locator(selector).evaluate("el => el.inert"), (width, height, selector)
    assert menu.evaluate("menu => menu.parentElement === document.querySelector('#artifact-panel')")
    assert menu.evaluate("menu => menu.closest('[role=dialog][aria-modal=true]') === document.querySelector('#artifact-panel')")
    assert not menu.evaluate("menu => menu.parentElement === document.body")
    expect(page.locator("#artifact-heading")).to_be_focused()

    # The compact menu participates in the fullscreen dialog's focus scope in both directions.
    trigger.click()
    expect(trigger).to_have_attribute("aria-expanded", "true")
    expect(menu).to_be_visible()
    expect(menu.locator("[role='menuitem']").first).to_be_focused()
    for key in (["Tab"] * 24) + (["Shift+Tab"] * 24):
        page.keyboard.press(key)
        assert page.locator(":focus").evaluate("el => !!el.closest('#artifact-panel')"), (width, height, key)

    # Escape closes only the topmost menu first, updates its trigger, then exits fullscreen.
    page.keyboard.press("Escape")
    expect(menu).to_be_hidden()
    expect(trigger).to_have_attribute("aria-expanded", "false")
    expect(trigger).to_be_focused()
    expect(panel).to_have_attribute("role", "dialog")
    assert page.locator(".shell").evaluate("el => el.inert")
    page.keyboard.press("Escape")
    expect(panel).not_to_have_attribute("role", "dialog")
    expect(trigger).to_be_focused()
    assert not page.locator(".shell").evaluate("el => el.inert")
    assert menu.evaluate("menu => menu.parentElement === document.querySelector('#artifact-panel')")


def run():
    with sync_playwright() as p:
        browser = p.chromium.launch()
        all_errors = []

        # Twelve-viewport containment, density, action hierarchy, and semantic rows.
        for width, height in VIEWPORTS:
            page, errors = new_page(browser, width, height, touch=width <= 600)
            assert_shell(page, width, height)
            assert page.locator(".brand").text_content() == "devx"
            assert_fleet_contract(page)
            row_height = page.locator(".session").first.bounding_box()["height"]
            assert row_height == (44 if width <= 600 else 32), (width, row_height)
            if width >= 1024:
                for label in ["Output", "Artifacts", "Split: terminal", "Compose", "More"]:
                    expect(page.get_by_role("button", name=label, exact=True)).to_be_visible()
                expect(page.locator("#mobile-actions")).to_be_hidden()
            else:
                expect(page.locator("#mobile-actions")).to_be_visible()
                expect(page.locator(".terminal-actions .action").first).to_be_hidden()
            all_errors += errors
            page.close()

        # Explicit compact fullscreen ownership, focus, inertness, and Escape ordering regressions.
        for width, height in [(768, 1024), (390, 844), (320, 700)]:
            page, errors = new_page(browser, width, height, touch=width <= 600)
            compact_artifact_fullscreen_check(page, width, height)
            assert_shell(page, width, height)
            all_errors += errors
            page.close()

        # Desktop: repeated ordered fleet navigation, truthfulness, and Output popup/focus/scroll.
        page, errors = new_page(browser, 1440, 1000)
        assert page.locator(".session").first.bounding_box()["height"] == 32
        visible_rows = page.locator(".session").evaluate_all("els => els.filter(el => { const r=el.getBoundingClientRect(); return r.bottom>0 && r.top<innerHeight }).length")
        assert visible_rows >= 18, visible_rows
        search = page.locator("#session-search")
        search.focus()
        ordered = page.locator(".session").evaluate_all("els => els.filter(el => el.offsetParent !== null).map(el => el.dataset.branch)")
        for expected in ordered[:4]:
            page.keyboard.press("ArrowDown")
            expect(page.locator(f".session[data-branch='{expected}']")).to_be_focused()
        page.keyboard.press("ArrowUp")
        expect(page.locator(f".session[data-branch='{ordered[2]}']")).to_be_focused()
        page.keyboard.press("Enter")
        expect(page.locator("#branch-label")).to_have_text(ordered[2])
        search.fill("reconnect")
        search.press("ArrowDown")
        expect(page.locator(".session[data-branch='jf-reconnect']")).to_be_focused()
        page.keyboard.press("Enter")
        expect(page.locator("#session-title")).to_have_text("Terminal reconnect handling")
        search.fill("")
        page.locator(".session[data-branch='jf-ui-refresh']").click()
        expect(page.locator("#session-facts")).to_contain_text("clean worktree")
        output_reader_check(page, 1440, 1000, page.get_by_role("button", name="Output", exact=True))

        # Quick switcher has repeated arrow navigation and Enter selection.
        page.keyboard.press("Control+p")
        expect(page.locator("#quick-input")).to_be_focused()
        page.keyboard.press("ArrowDown")
        page.keyboard.press("ArrowDown")
        selected_branch = page.locator("#quick-dialog .switch-item.selected").get_attribute("data-branch")
        page.keyboard.press("Enter")
        expect(page.locator("#branch-label")).to_have_text(selected_branch)
        page.locator(".session[data-branch='jf-ui-refresh']").click()

        # Artifact parity: folders, formats, metadata, intake, paths, confirmation, and isolation.
        page.get_by_role("button", name="Artifacts", exact=True).click()
        panel = page.locator("#artifact-panel")
        expect(panel).to_have_class("artifact-panel open")
        expect(page.locator(".artifact-folder")).to_contain_text([".artifacts"])
        assert page.locator(".artifact-folder").count() >= 4
        newest_first = page.locator(".artifact-list-item b").first.inner_text()
        page.locator("#artifact-sort").select_option("oldest")
        assert page.locator(".artifact-list-item b").first.inner_text() != newest_first
        page.locator("#artifact-sort").select_option("title")

        for title, expected in [
            ("walkthrough.mp4", "Video player state"),
            ("fixtures.zip", "No inline preview"),
        ]:
            page.locator(".artifact-list-item", has_text=title).click()
            expect(page.locator("#artifact-preview")).to_contain_text(expected)
        page.locator(".artifact-list-item", has_text="report.html").click()
        expect(page.locator("#artifact-preview iframe[title^='HTML preview']")).to_be_visible()
        page.locator(".artifact-list-item", has_text="brief.pdf").click()
        expect(page.locator("#artifact-preview iframe[title^='PDF preview']")).to_be_visible()
        page.locator(".artifact-list-item", has_text="FleetSummary.jsx").click()
        expect(page.locator("#artifact-preview")).to_contain_text("25 complete session fixtures")
        page.get_by_role("button", name="Code", exact=True).click()
        expect(page.locator("#artifact-preview")).to_contain_text("export default function FleetSummary")
        page.get_by_role("button", name="Preview", exact=True).click()

        # Insert uses the production path syntax.
        page.get_by_role("button", name="Insert", exact=True).click()
        expect(page.locator("#composer textarea")).to_have_value(".artifacts/components/FleetSummary.jsx")
        page.keyboard.press("Escape")

        # Remove is non-destructive until titled confirmation is accepted.
        page.get_by_role("button", name="Remove", exact=True).click()
        expect(page.locator("#generic-title")).to_have_text("Remove artifact?")
        expect(page.locator("#generic-dialog")).to_contain_text("requires confirmation")
        page.locator("#generic-dialog [data-close-dialog]").last.click()
        expect(page.locator(".artifact-list-item", has_text="FleetSummary.jsx")).to_have_count(1)
        page.get_by_role("button", name="Remove", exact=True).click()
        page.locator("#confirm-remove-artifact").click()
        expect(page.locator(".artifact-list-item", has_text="FleetSummary.jsx")).to_have_count(0)

        # Text paste and file drop intake occur in ArtifactPane.
        page.locator("#artifact-panel").evaluate("""panel => { const dt=new DataTransfer(); dt.setData('text/plain','# pasted review'); panel.dispatchEvent(new ClipboardEvent('paste',{clipboardData:dt,bubbles:true})); }""")
        expect(page.locator(".artifact-list-item", has_text="pasted-note-")).to_have_count(1)
        page.locator("#artifact-panel").evaluate("""panel => { const dt=new DataTransfer(); dt.items.add(new File(['drop body'],'dropped.txt',{type:'text/plain'})); panel.dispatchEvent(new DragEvent('drop',{dataTransfer:dt,bubbles:true})); }""")
        expect(page.locator(".artifact-list-item", has_text="dropped.txt")).to_have_count(1)

        # New artifact exposes format, retention and tags; Edit exposes summary/tags/retention.
        page.get_by_role("button", name="New", exact=True).click()
        expect(page.locator("#artifact-format")).to_be_visible()
        expect(page.locator("#artifact-retention")).to_be_visible()
        expect(page.locator("#artifact-tags")).to_be_visible()
        page.locator("#artifact-title").fill("density-notes.html")
        page.locator("#artifact-format").select_option("html")
        page.locator("#artifact-retention").select_option("30 days")
        page.locator("#artifact-tags").fill("density, ui")
        page.locator("#artifact-text").fill("<h1>Density notes</h1>")
        page.locator("#create-artifact").click()
        page.get_by_role("button", name="Edit", exact=True).click()
        for selector in ["#edit-artifact-summary", "#edit-artifact-tags", "#edit-artifact-retention"]:
            expect(page.locator(selector)).to_be_visible()
        page.locator("#edit-artifact-summary").fill("Viewport density evidence")
        page.locator("#edit-artifact-tags").fill("density, evidence")
        page.locator("#edit-artifact-retention").select_option("7 days")
        page.locator("#save-artifact").click()
        expect(page.locator("#artifact-preview-head")).to_contain_text("Viewport density evidence")
        expect(page.locator("#artifact-preview-head")).to_contain_text("#density #evidence")
        expect(page.locator("#artifact-preview-head")).to_contain_text("7 days")

        # Fullscreen is modal-like, excludes the shell, traps both directions, and restores focus.
        fullscreen_trigger = page.get_by_role("button", name="Full Screen", exact=True)
        fullscreen_trigger.click()
        expect(panel).to_have_attribute("role", "dialog")
        expect(panel).to_have_attribute("aria-modal", "true")
        assert panel.bounding_box() == {"x": 0, "y": 0, "width": 1440, "height": 1000}
        assert page.locator(".shell").evaluate("el => el.inert")
        for selector in ["#navigator", ".topbar", ".facts", ".windowbar", ".terminal-pane", "#status-panel", ".mobile-bottom"]:
            assert page.locator(selector).evaluate("el => el.inert"), selector
        expect(page.locator("#artifact-heading")).to_be_focused()
        page.keyboard.press("Shift+Tab")
        expect(page.get_by_role("button", name="Remove", exact=True)).to_be_focused()
        page.keyboard.press("Tab")
        expect(page.locator("#artifact-heading")).to_be_focused()
        for _ in range(20):
            page.keyboard.press("Tab")
            assert page.locator(":focus").evaluate("el => !!el.closest('#artifact-panel')")
        page.keyboard.press("Escape")
        expect(fullscreen_trigger).to_be_focused()
        expect(panel).not_to_have_attribute("role", "dialog")
        assert not page.locator(".shell").evaluate("el => el.inert")
        page.get_by_role("button", name="Close", exact=True).click()

        page.locator("#toasts").evaluate("el => el.replaceChildren()")
        assert_shell(page, 1440, 1000)
        all_errors += errors
        page.close()

        # Tablet: compact Split truthfully cycles all four modes and returns to terminal.
        page, errors = new_page(browser, 768, 1024)
        expected_modes = [
            ("vertical", "pane-wrap", True),
            ("horizontal", "pane-wrap horizontal", True),
            ("artifacts", "pane-wrap artifacts-only", True),
            ("terminal", "pane-wrap", False),
        ]
        for mode, class_name, panel_open in expected_modes:
            page.locator("#mobile-actions").click()
            page.locator("#actions-menu [data-action='split']").click()
            expect(page.locator("#actions-menu [data-action='split']")).to_have_text(f"Split — current mode: {mode}")
            expect(page.locator("#pane-wrap")).to_have_class(class_name)
            assert page.locator("#artifact-panel").evaluate("el => el.classList.contains('open')") == panel_open
        assert_shell(page, 768, 1024)
        all_errors += errors
        page.close()

        # Mobile Artifact/Status destinations remove dead terminal chrome and retain distinct actions.
        for width, height in [(390, 844), (320, 700)]:
            page, errors = new_page(browser, width, height, touch=True)
            page.locator("#mobile-artifacts").click()
            expect(page.locator("#artifact-heading")).to_be_focused()
            expect(page.locator("#artifact-menu-trigger")).to_have_text("Artifact actions")
            expect(page.locator("#artifact-menu-trigger")).to_have_attribute("aria-label", "Open artifact actions")
            expect(page.locator(".windowbar")).to_be_hidden()
            expect(page.locator(".mobile-composer")).to_be_hidden()
            expect(page.locator("#composer")).to_be_hidden()
            page.locator("#artifact-menu-trigger").click()
            expect(page.locator("#artifact-actions-menu")).to_contain_text("Full Screen preview")
            page.keyboard.press("Escape")
            page.locator("#mobile-status").click()
            expect(page.locator("#status-heading")).to_be_focused()
            expect(page.locator(".windowbar")).to_be_hidden()
            expect(page.locator(".mobile-composer")).to_be_hidden()
            expect(page.locator("#composer")).to_be_hidden()
            page.locator("#mobile-terminal").click()
            expect(page.locator(".windowbar")).to_be_visible()
            expect(page.locator(".mobile-composer")).to_be_visible()
            assert_shell(page, width, height)
            all_errors += errors
            page.close()

        # Every committed proof starts from a new page and the initial eight-artifact fixture.
        for width, height, filename, destination in [
            (1440, 1000, "desktop-1440x1000.png", "terminal"),
            (768, 1024, "tablet-768x1024.png", "terminal"),
            (390, 844, "mobile-390x844.png", "artifacts"),
        ]:
            page, errors = new_page(browser, width, height, touch=width <= 600)
            expect(page.locator("#session-facts")).to_contain_text("8 artifacts")
            if destination == "artifacts":
                page.locator("#mobile-artifacts").click()
                expect(page.locator(".artifact-list-item")).to_have_count(8)
            assert_shell(page, width, height)
            page.screenshot(path=str(SHOT_DIR / filename))
            all_errors += errors
            page.close()

        browser.close()
        assert not all_errors, all_errors
        print("PASS: 12 viewports; no overflow/errors; semantic status+artifact counts; production fleet order; repeated fleet/quick navigation; Blob popup; Output scroll/reverse trap; modal Artifact fullscreen isolation/traps; production artifact path+confirmation; folder/preview/intake/metadata parity; compact split cycle; mobile chrome hiding")


if __name__ == "__main__":
    run()
