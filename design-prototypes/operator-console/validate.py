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
    box = reader.bounding_box()
    assert box == {"x": 0, "y": 0, "width": width, "height": height}, box
    expect(page.locator("#output-reader-heading")).to_be_focused()
    assert page.locator(".shell").evaluate("el => el.inert")
    expect(page.locator("#output-reader-body")).to_contain_text("operator output remains available")
    expect(page.locator("#output-open-tab")).to_be_visible()
    page.keyboard.press("Escape")
    expect(reader).to_be_hidden()
    expect(trigger).to_be_focused()
    assert not page.locator(".shell").evaluate("el => el.inert")


def run():
    with sync_playwright() as p:
        browser = p.chromium.launch()
        all_errors = []

        # Containment, responsive action hierarchy, compact/touch density.
        for width, height in VIEWPORTS:
            page, errors = new_page(browser, width, height, touch=width <= 600)
            assert_shell(page, width, height)
            assert page.locator(".brand").text_content() == "devx", (width, height)
            assert page.locator(".session").count() >= 25
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

        # Desktop fleet/search/keyboard, complete fixture selection, and full-screen output.
        page, errors = new_page(browser, 1440, 1000)
        assert page.locator(".session").first.bounding_box()["height"] == 32
        visible_rows = page.locator(".session").evaluate_all("els => els.filter(el => { const r=el.getBoundingClientRect(); return r.bottom>0 && r.top<innerHeight }).length")
        assert visible_rows >= 18, visible_rows
        expect(page.locator("#total-count")).to_have_text("25")
        page.locator("#session-search").fill("reconnect")
        expect(page.locator(".session:not([hidden])")).to_have_count(1)
        page.locator("#session-search").press("ArrowDown")
        expect(page.locator(".session[data-branch='jf-reconnect']")).to_be_focused()
        page.locator("#session-search").fill("")
        page.locator(".session[data-branch='jf-gatepost-proxy']").click()
        expect(page.locator("#session-title")).to_have_text("Gatepost proxy consolidation")
        expect(page.locator("#session-facts")).to_contain_text("gatepost target")
        expect(page.locator("#status-content")).to_contain_text("flagged for explicit operator review")
        expect(page.locator("#status-content")).to_contain_text("gatepost-proxy-api.localhost")
        expect(page.locator("#artifact-list")).to_contain_text("proxy-review.md")
        page.locator(".session[data-branch='jf-ui-refresh']").click()
        output_reader_check(page, 1440, 1000, page.get_by_role("button", name="Output", exact=True))

        # Desktop labels stay grouped; More is descriptive and keyboard-dismissable.
        page.locator("#more-actions").click()
        expect(page.locator("#actions-menu")).to_be_visible()
        expect(page.locator("#actions-menu")).to_contain_text("Attach image to terminal")
        page.keyboard.press("Escape")
        expect(page.locator("#more-actions")).to_be_focused()

        # ArtifactPane parity: sort, list, preview, JSX mode, selected actions, full-screen.
        page.get_by_role("button", name="Artifacts", exact=True).click()
        panel = page.locator("#artifact-panel")
        expect(panel).to_have_class("artifact-panel open")
        for label in ["Hide list", "Full Screen", "Upload", "New", "Refresh", "Close"]:
            expect(panel.get_by_role("button", name=label, exact=True)).to_be_visible()
        titles_newest = page.locator(".artifact-list-item b").all_inner_texts()
        page.locator("#artifact-sort").select_option("oldest")
        titles_oldest = page.locator(".artifact-list-item b").all_inner_texts()
        assert titles_newest == list(reversed(titles_oldest))
        page.locator("#artifact-sort").select_option("title")
        titles_title = page.locator(".artifact-list-item b").all_inner_texts()
        assert titles_title == sorted(titles_title)
        page.get_by_role("button", name="Hide list", exact=True).click()
        expect(page.locator("#artifact-list")).to_have_class("artifact-list-wrap collapsed")
        page.get_by_role("button", name="Show list", exact=True).click()
        page.locator(".artifact-list-item", has_text="FleetSummary.jsx").click()
        expect(page.locator("#artifact-preview")).to_contain_text("Fleet is ready")
        page.get_by_role("button", name="Code", exact=True).click()
        expect(page.locator("#artifact-preview")).to_contain_text("export default function FleetSummary")
        page.get_by_role("button", name="Preview", exact=True).click()
        page.get_by_role("button", name="Archive", exact=True).click()
        expect(page.locator(".artifact-list-item", has_text="FleetSummary.jsx")).to_contain_text("archived")
        page.get_by_role("button", name="Insert", exact=True).click()
        expect(page.locator("#composer")).to_have_class("composer open")
        expect(page.locator("#composer textarea")).to_have_value("[artifact:FleetSummary.jsx]")
        page.keyboard.press("Escape")
        page.get_by_role("button", name="Full Screen", exact=True).click()
        expect(panel).to_have_class("artifact-panel open fullscreen")
        assert panel.bounding_box() == {"x": 0, "y": 0, "width": 1440, "height": 1000}
        expect(page.get_by_role("button", name="Exit Full", exact=True)).to_be_visible()
        page.keyboard.press("Escape")
        assert "fullscreen" not in panel.get_attribute("class")
        page.get_by_role("button", name="New", exact=True).click()
        assert page.locator("#create-artifact").is_disabled()
        page.locator("#artifact-title").fill("density-notes.md")
        page.locator("#artifact-text").fill("Density validation notes")
        page.locator("#create-artifact").click()
        expect(page.locator("#artifact-list")).to_contain_text("density-notes.md")
        page.get_by_role("button", name="Refresh", exact=True).click()
        expect(page.locator(".toast").last).to_contain_text("Artifacts refreshed")
        page.get_by_role("button", name="Close", exact=True).click()
        expect(panel).not_to_have_class("artifact-panel open")

        # Quick switcher and session-scoped artifact replacement.
        page.keyboard.press("Control+p")
        page.locator("#quick-input").fill("Unread semantics")
        page.locator("#quick-dialog .switch-item:not([hidden])").click()
        expect(page.locator("#session-title")).to_have_text("Unread semantics audit")
        expect(page.locator("#artifact-list")).to_contain_text("audit-notes.md")
        assert page.locator("#artifact-list").get_by_text("operator-console.png").count() == 0

        # Status/artifact exclusivity at compact widths is covered separately below.
        page.locator("#toasts").evaluate("el => el.replaceChildren()")
        all_errors += errors
        page.screenshot(path=str(SHOT_DIR / "desktop-1440x1000.png"))
        page.close()

        # Tablet drawer inertness, one labeled Actions control, output, and panel exclusivity.
        page, errors = new_page(browser, 768, 1024)
        assert page.locator("#navigator").get_attribute("aria-hidden") == "true"
        assert page.locator("#navigator").evaluate("el => el.inert")
        page.locator("#open-nav").click()
        expect(page.locator("#session-search")).to_be_focused()
        assert page.locator("#navigator").get_attribute("role") == "dialog"
        page.keyboard.press("Escape")
        expect(page.locator("#open-nav")).to_be_focused()
        page.locator("#mobile-actions").click()
        expect(page.locator("#actions-menu")).to_contain_text("Output — full-screen terminal reader")
        expect(page.locator("#actions-menu")).to_contain_text("Artifacts — browse and preview")
        page.locator("#actions-menu [data-action='view']").click()
        expect(page.locator("#output-reader")).to_be_visible()
        assert page.locator("#output-reader").bounding_box() == {"x": 0, "y": 0, "width": 768, "height": 1024}
        page.keyboard.press("Escape")
        expect(page.locator("#mobile-actions")).to_be_focused()
        page.locator("#status-toggle").click()
        page.locator("#mobile-actions").click()
        page.locator("#actions-menu [data-action='artifacts']").click()
        assert not page.locator("#status-panel").evaluate("el => el.classList.contains('open')")
        assert page.locator("#artifact-panel").evaluate("el => el.classList.contains('open')")
        assert_shell(page, 768, 1024)
        all_errors += errors
        page.screenshot(path=str(SHOT_DIR / "tablet-768x1024.png"))
        page.close()

        # Mobile artifact action menu, preview/full-screen, output/focus, composer and soft keys.
        for width, height in [(390, 844), (320, 700)]:
            page, errors = new_page(browser, width, height, touch=True)
            assert page.locator(".session").first.bounding_box()["height"] == 44
            page.locator("#mobile-actions").click()
            expect(page.locator("#actions-menu [data-action='split']")).to_contain_text("current mode: terminal")
            page.locator("#actions-menu [data-action='view']").click()
            assert page.locator("#output-reader").bounding_box() == {"x": 0, "y": 0, "width": width, "height": height}
            expect(page.locator("#output-reader-heading")).to_be_focused()
            page.keyboard.press("Escape")
            expect(page.locator("#mobile-actions")).to_be_focused()
            page.locator("#mobile-artifacts").click()
            expect(page.locator("#artifact-heading")).to_be_focused()
            expect(page.locator("#artifact-menu-trigger")).to_be_visible()
            page.locator("#artifact-menu-trigger").click()
            for copy in ["Sort: newest", "Hide artifact list", "Full Screen preview", "Upload files", "New text artifact", "Refresh list", "Close artifacts"]:
                expect(page.locator("#artifact-actions-menu")).to_contain_text(copy)
            page.locator("#artifact-actions-menu [data-artifact-command='fullscreen']").click()
            assert page.locator("#artifact-panel").bounding_box() == {"x": 0, "y": 0, "width": width, "height": height}
            page.keyboard.press("Escape")
            page.keyboard.press("Escape")
            expect(page.locator("#mobile-artifacts")).to_be_focused()
            page.locator("#mobile-text").fill("mobile valid")
            assert page.locator("#mobile-send").is_enabled()
            page.locator("#keys-toggle").click()
            expect(page.locator("#softkeys")).to_have_class("softkeys open")
            page.locator("#mobile-send").click()
            expect(page.locator("#terminal-content")).to_contain_text("mobile valid")
            assert_shell(page, width, height)
            if width == 390:
                page.locator("#toasts").evaluate("el => el.replaceChildren()")
                page.screenshot(path=str(SHOT_DIR / "mobile-390x844.png"))
            all_errors += errors
            page.close()

        browser.close()
        assert not all_errors, all_errors
        print("PASS: 12 contained viewports; 25 complete fleet fixtures; compact/touch density; full-screen Output; labeled responsive actions; ArtifactPane sort/list/preview/JSX/full-screen/upload/new/refresh/close/insert/archive; focus and panel regressions; 0 console/page errors")


if __name__ == "__main__":
    run()
