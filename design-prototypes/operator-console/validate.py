#!/usr/bin/env python3
"""Deterministic browser checks for the Operator Console v2 static prototype."""
from playwright.sync_api import sync_playwright, expect

URL = "http://127.0.0.1:4181/"
VIEWPORTS = [(1440,1000),(1024,768),(900,800),(768,1024),(700,800),(601,800),(390,844),(320,700)]


def assert_shell(page, width, height):
    metrics = page.evaluate("""() => ({
      innerHeight, innerWidth,
      docHeight: document.documentElement.scrollHeight,
      docWidth: document.documentElement.scrollWidth,
      bodyHeight: document.body.scrollHeight,
      terminalScroll: document.querySelector('.terminal').scrollHeight >= document.querySelector('.terminal').clientHeight,
      terminalOverflow: getComputedStyle(document.querySelector('.terminal')).overflowY,
      bodyRowHeight: document.querySelector('.body-row').getBoundingClientRect().height
    })""")
    assert metrics["docHeight"] == height, (width, height, metrics)
    assert metrics["bodyHeight"] == height, (width, height, metrics)
    assert metrics["docWidth"] == width, (width, height, metrics)
    assert metrics["terminalOverflow"] == "auto", (width, height, metrics)
    assert metrics["bodyRowHeight"] > 0, (width, height, metrics)


def new_page(browser, width, height):
    page = browser.new_page(viewport={"width": width, "height": height})
    errors = []
    page.on("console", lambda msg: errors.append(f"console:{msg.type}:{msg.text}") if msg.type == "error" else None)
    page.on("pageerror", lambda exc: errors.append(f"pageerror:{exc}"))
    page.goto(URL)
    expect(page.locator("#session-title")).to_have_text("Operator console refresh")
    return page, errors


def run():
    with sync_playwright() as p:
        browser = p.chromium.launch()
        all_errors = []
        for width, height in VIEWPORTS:
            page, errors = new_page(browser, width, height)
            assert_shell(page, width, height)
            page.locator("#status-toggle").click()
            expect(page.locator("#status-panel")).to_have_class("status-panel open")
            assert_shell(page, width, height)
            if width <= 600:
                page.locator("#mobile-artifacts").click()
            else:
                page.locator("[data-action='artifacts']").first.click()
            assert_shell(page, width, height)
            if width <= 1180:
                assert not page.locator("#status-panel").evaluate("el => el.classList.contains('open')")
                assert page.locator("#artifact-panel").evaluate("el => el.classList.contains('open')")
            all_errors += errors
            page.close()

        # Complete fixture switching and quick switcher.
        page, errors = new_page(browser, 1440, 1000)
        page.locator(".session[data-branch='jf-gatepost-proxy']").click()
        expect(page.locator("#session-title")).to_have_text("Gatepost proxy consolidation")
        expect(page.locator("#session-facts")).to_contain_text("tmux stopped")
        expect(page.locator("#session-facts")).to_contain_text("gatepost target")
        expect(page.locator("#session-facts")).to_contain_text("2 routes")
        expect(page.locator("#artifact-list")).to_contain_text("proxy-review.md")
        page.locator("#status-toggle").click()
        expect(page.locator("#status-content")).to_contain_text("Enabled for this session")
        expect(page.locator("#status-content")).to_contain_text("gatepost-proxy-api.localhost")
        page.keyboard.press("Control+p")
        page.locator("#quick-input").fill("Unread")
        page.locator("#quick-dialog .switch-item:not([hidden])").click()
        expect(page.locator("#session-title")).to_have_text("Unread semantics audit")
        expect(page.locator("#branch-label")).to_have_text("jf-unread-audit")
        expect(page.locator("#session-facts")).to_contain_text("clean worktree")
        expect(page.locator("#terminal-content")).to_contain_text("jf-unread-audit")
        assert page.locator("#quick-dialog [data-branch='jf-unread-audit']").get_attribute("aria-current") == "true"
        assert page.locator("#quick-dialog [data-branch='jf-unread-audit']").evaluate("el => el.classList.contains('selected')")
        assert not page.locator("#quick-dialog [data-branch='jf-ui-refresh']").evaluate("el => el.classList.contains('selected')")

        # Validated composer and action transitions.
        page.keyboard.press("Control+k")
        send = page.locator("#composer button[type='submit']")
        assert send.is_disabled()
        page.locator("#composer textarea").fill("long ✓ input <safe> " + "x" * 1200)
        assert send.is_enabled()
        send.click()
        expect(page.locator("#terminal-content")).to_contain_text("long ✓ input <safe>")
        assert_shell(page, 1440, 1000)

        page.locator("[data-action='new-artifact']").first.click()
        create = page.locator("#create-artifact")
        assert create.is_disabled()
        page.locator("#artifact-text").fill("handoff")
        create.click()
        expect(page.locator("#session-facts")).to_contain_text("3 artifacts")
        expect(page.locator("#artifact-list")).to_contain_text("note-3.txt")

        # Artifact insertion performs a real composer transition.
        page.locator("[data-action='insert']").first.click()
        page.locator("#generic-dialog [data-insert-artifact]").first.click()
        expect(page.locator("#composer")).to_have_class("composer open")
        expect(page.locator("#composer textarea")).to_have_value("[artifact:audit-notes.md]")
        page.keyboard.press("Escape")

        # Rename rejects empty, then updates every visible identity.
        page.locator("#session-options").click()
        page.locator("#rename-action").click()
        page.locator("#rename-input").fill("")
        assert page.locator("#save-name").is_disabled()
        page.locator("#rename-input").fill("Unread semantics verified")
        page.locator("#save-name").click()
        expect(page.locator("#session-title")).to_have_text("Unread semantics verified")
        expect(page.locator(".session[data-branch='jf-unread-audit'] .session-name")).to_have_text("Unread semantics verified")

        # Color persists across dialog openings; share validation only accepts fixture matches.
        page.locator("#session-options").click()
        page.locator("#color-action").click()
        page.locator("#color-choices [aria-label='Blue']").click()
        assert page.locator("#color-choices [aria-label='Blue']").get_attribute("aria-pressed") == "true"
        page.locator("#generic-dialog [data-close-dialog]").last.click()
        expect(page.locator("#session-options")).to_be_focused()
        page.locator("#session-options").click()
        page.locator("#color-action").click()
        assert page.locator("#color-choices [aria-label='Blue']").get_attribute("aria-pressed") == "true"
        assert page.locator("#color-choices [aria-label='Purple']").get_attribute("aria-pressed") == "false"
        page.locator("#generic-dialog [data-close-dialog]").last.click()
        page.locator("#session-options").click()
        page.locator("#share-action").click()
        assert page.locator("#share-continue").is_disabled()
        page.locator("#share-target").fill("does-not-exist <script>alert(1)</script>")
        page.locator("#share-continue").click()
        expect(page.locator("#share-result")).to_contain_text("No session fixture matches")
        expect(page.locator("#share-result")).to_contain_text("Choose an available session name or branch")
        assert page.locator("#share-target").get_attribute("aria-invalid") == "true"
        expect(page.locator("#share-target")).to_be_focused()
        page.locator("#share-target").fill("jf-gatepost-proxy")
        page.locator("#share-continue").click()
        expect(page.locator("#share-result")).to_contain_text("Validated target: Gatepost proxy consolidation (jf-gatepost-proxy)")
        page.locator("#generic-dialog [data-close-dialog]").last.click()
        expect(page.locator("#session-options")).to_be_focused()

        # Upload CTA is explicitly disabled placement-study UI.
        page.locator("[data-action='image']").first.click()
        expect(page.locator("#generic-dialog button[disabled]")).to_contain_text("placement study only")
        page.locator("#generic-dialog [data-close-dialog]").last.click()

        # New session validates and becomes a fully-rendered session.
        page.locator("#new-session").click()
        assert page.locator("#create-session").is_disabled()
        page.locator("#new-name").fill("Fixture session")
        page.locator("#create-session").click()
        expect(page.locator("#session-title")).to_have_text("Fixture session")
        expect(page.locator("#session-facts")).to_contain_text("0 routes")
        expect(page.locator("#status-content")).to_contain_text("new fixture created")

        # Stale-review CTAs transition or are explicitly unavailable.
        page.locator("#stale-launch").click()
        assert page.get_by_role("button", name="Open unavailable in fixture").is_disabled()
        page.locator("#prune-action").click()
        expect(page.locator("#prune-action")).to_contain_text("Confirm prune")
        page.locator("#prune-action").click()
        assert page.locator("#prune-action").is_disabled()
        page.locator("#reviewed-action").click()
        assert page.locator("#reviewed-action").is_disabled()
        page.locator("#stale-dialog [data-close-dialog]").click()

        # State-gallery app-level placements.
        page.locator("#state-gallery").click()
        page.locator("#show-image-toast").click()
        expect(page.locator(".toast img[alt='Remote CLI image preview']")).to_be_visible()
        page.locator(".toast [data-open-image]").click()
        expect(page.locator("#generic-dialog img[alt='Expanded remote CLI image preview']")).to_be_visible()
        page.locator("#generic-dialog [data-close-dialog]").last.click()
        page.locator(".toast [data-dismiss-toast]").click()
        page.locator("#state-gallery").click()
        page.locator("#show-flag-toast").click()
        page.locator(".toast.flag [data-view-flag]").click()
        expect(page.locator("#session-title")).to_have_text("Gatepost proxy consolidation")

        # Escape dismisses only the top custom layer and menu-launched surfaces restore visible owners.
        if page.locator("#status-panel").evaluate("el => el.classList.contains('open')"):
            page.locator("#status-toggle").click()
        page.locator("#status-toggle").click()
        page.locator("#session-options").click()
        page.keyboard.press("Escape")
        expect(page.locator("#session-options")).to_be_focused()
        assert page.locator("#status-panel").evaluate("el => el.classList.contains('open')")
        page.keyboard.press("Escape")
        expect(page.locator("#status-toggle")).to_be_focused()
        assert not page.locator("#status-panel").evaluate("el => el.classList.contains('open')")
        page.locator("#session-options").click()
        page.get_by_role("button", name="Routes and status details").click()
        page.keyboard.press("Escape")
        expect(page.locator("#session-options")).to_be_focused()
        assert not page.locator("#status-panel").evaluate("el => el.classList.contains('open')")
        all_errors += errors
        page.close()

        # Tablet drawer is absent from tree when closed; gallery toast closes it and receives focus.
        page, errors = new_page(browser, 768, 1024)
        assert page.locator("#navigator").get_attribute("aria-hidden") == "true"
        assert page.locator("#navigator").evaluate("el => el.inert")
        page.keyboard.press("Tab")
        assert page.evaluate("document.activeElement.id") == "open-nav"
        page.locator("#open-nav").click()
        expect(page.locator("#session-search")).to_be_focused()
        assert page.locator("#navigator").get_attribute("role") == "dialog"
        page.locator("#session-search").fill("no session has this value")
        expect(page.locator("#no-results")).to_be_visible()
        page.locator("#clear-filter").click()
        expect(page.locator(".session").first).to_be_visible()
        page.locator("#state-gallery").click()
        page.locator("#show-image-toast").click()
        assert page.locator("#navigator").evaluate("el => el.inert")
        assert page.locator("#navigator").get_attribute("aria-hidden") == "true"
        expect(page.locator(".toast [data-open-image]")).to_be_focused()
        page.keyboard.press("Tab")
        expect(page.locator(".toast [data-dismiss-toast]")).to_be_focused()
        assert_shell(page, 768, 1024)
        all_errors += errors
        page.close()

        # Mobile destination exclusivity, focus entry, isolation, and empty input validation.
        for width, height in [(390,844),(320,700)]:
            page, errors = new_page(browser, width, height)
            assert page.locator("#mobile-send").is_disabled()

            # Gallery actions leave the modal navigator and enter the keyboard order at phone widths.
            page.locator("#mobile-sessions").click()
            page.locator("#state-gallery").click()
            page.locator("#show-image-toast").click()
            assert page.locator("#navigator").evaluate("el => el.inert")
            expect(page.locator(".toast [data-open-image]")).to_be_focused()
            page.keyboard.press("Tab")
            expect(page.locator(".toast [data-dismiss-toast]")).to_be_focused()
            page.locator(".toast [data-dismiss-toast]").click()

            page.locator("#mobile-status").click()
            expect(page.locator("#status-heading")).to_be_focused()
            assert page.locator("#stage").evaluate("el => el.inert")
            assert page.locator("#status-panel").evaluate("el => getComputedStyle(el).overflowY === 'auto'")
            if width == 320:
                assert page.locator("#status-panel").evaluate("el => el.scrollHeight > el.clientHeight")
            page.locator("#mobile-artifacts").click()
            expect(page.locator("#artifact-heading")).to_be_focused()
            assert not page.locator("#status-panel").evaluate("el => el.classList.contains('open')")
            assert page.locator("#artifact-panel").evaluate("el => el.classList.contains('open')")
            assert page.locator(".terminal-pane").evaluate("el => el.inert")
            assert page.locator("#mobile-artifacts").get_attribute("aria-current") == "page"
            page.keyboard.press("Escape")
            expect(page.locator("#mobile-artifacts")).to_be_focused()
            assert not page.locator("#artifact-panel").evaluate("el => el.classList.contains('open')")

            # Phone action is truthful, opens the synchronized Artifacts destination, and restores its owner.
            page.locator("#mobile-actions").click()
            expect(page.locator("#actions-menu [data-action='split']")).to_have_text("Show artifacts")
            page.locator("#actions-menu [data-action='split']").click()
            expect(page.locator("#artifact-heading")).to_be_focused()
            assert page.locator("#artifact-panel").evaluate("el => el.classList.contains('open')")
            assert page.locator("#mobile-artifacts").get_attribute("aria-current") == "page"
            assert page.locator("#mobile-terminal").get_attribute("aria-current") is None
            page.keyboard.press("Escape")
            expect(page.locator("#mobile-actions")).to_be_focused()
            assert page.locator("#mobile-terminal").get_attribute("aria-current") == "page"

            # Menu-launched dialog returns focus to the visible mobile actions trigger.
            page.locator("#mobile-actions").click()
            page.locator("#actions-menu [data-action='view']").click()
            page.keyboard.press("Escape")
            expect(page.locator("#mobile-actions")).to_be_focused()

            page.locator("#mobile-text").fill("mobile valid")
            assert page.locator("#mobile-send").is_enabled()
            page.locator("#mobile-send").click()
            expect(page.locator("#terminal-content")).to_contain_text("mobile valid")
            assert_shell(page, width, height)
            all_errors += errors
            page.close()

        browser.close()
        assert not all_errors, all_errors
        print("PASS: 8 viewports contained; fixture validation, focus restoration, gallery toasts, synchronized phone artifacts, persisted color, quick selection, and 0 console/page errors")


if __name__ == "__main__":
    run()
