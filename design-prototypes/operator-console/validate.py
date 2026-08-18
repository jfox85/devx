#!/usr/bin/env python3
"""Deterministic browser checks for the Operator Console v2 static prototype."""
from pathlib import Path
from playwright.sync_api import sync_playwright, expect

URL = "http://127.0.0.1:4181/"
ROOT = Path(__file__).resolve().parent
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

        # Color and share-target actions provide selection/validation feedback.
        page.locator("#session-options").click()
        page.locator("#color-action").click()
        page.locator("#color-choices [aria-label='Blue']").click()
        assert page.locator("#color-choices [aria-label='Blue']").get_attribute("aria-pressed") == "true"
        page.locator("#generic-dialog [data-close-dialog]").last.click()
        page.locator("#session-options").click()
        page.locator("#share-action").click()
        assert page.locator("#share-continue").is_disabled()
        page.locator("#share-target").fill("gatepost-proxy")
        page.locator("#share-continue").click()
        expect(page.locator("#share-result")).to_contain_text("Validated target: gatepost-proxy")
        page.locator("#generic-dialog [data-close-dialog]").last.click()

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

        # Escape dismisses only the top custom layer and restores its trigger.
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
        all_errors += errors
        page.close()

        # Tablet drawer is absent from tree when closed; open state traps/restores.
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
        page.keyboard.press("Escape")
        expect(page.locator("#open-nav")).to_be_focused()
        assert page.locator("#navigator").evaluate("el => el.inert")
        all_errors += errors
        page.close()

        # Mobile destination exclusivity, focus entry, isolation, and empty input validation.
        for width, height in [(390,844),(320,700)]:
            page, errors = new_page(browser, width, height)
            assert page.locator("#mobile-send").is_disabled()
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
            page.locator("#mobile-text").fill("mobile valid")
            assert page.locator("#mobile-send").is_enabled()
            page.locator("#mobile-send").click()
            expect(page.locator("#terminal-content")).to_contain_text("mobile valid")
            assert_shell(page, width, height)
            all_errors += errors
            page.close()

        # Current screenshots after behavioral/layout changes.
        for width, height, name in [(1440,1000,'desktop-1440x1000.png'),(768,1024,'tablet-768x1024.png'),(390,844,'mobile-390x844.png')]:
            page, errors = new_page(browser, width, height)
            page.screenshot(path=str(ROOT / name))
            all_errors += errors
            page.close()

        browser.close()
        assert not all_errors, all_errors
        print("PASS: 8 viewports contained; fixture/actions/layers/mobile accessibility flows passed; 0 console/page errors")


if __name__ == "__main__":
    run()
