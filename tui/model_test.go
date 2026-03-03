package tui

import (
	"strings"
	"testing"
)

// newTestModel returns a minimal model configured for a given terminal height
// with showPreview disabled (non-preview mode keeps ensureCursorVisible math
// simple and independent of lastVisibleSession's render simulation).
func newTestModel(height int, sessionCount int) *model {
	m := InitialModel()
	m.height = height
	m.width = 80
	m.showPreview = false

	sessions := make([]sessionItem, sessionCount)
	for i := range sessions {
		sessions[i] = sessionItem{name: strings.Repeat("s", 8)}
	}
	m.sessions = sessions
	return m
}

// ---------------------------------------------------------------------------
// ensureCursorVisible — non-preview mode
// ---------------------------------------------------------------------------

// TestEnsureCursorVisible_ScrollUp verifies that when the cursor is above the
// current scroll window the offset snaps down to the cursor position.
func TestEnsureCursorVisible_ScrollUp(t *testing.T) {
	m := newTestModel(24, 20)
	m.scrollOffset = 10
	m.cursor = 3 // above the window

	m.ensureCursorVisible()

	if m.scrollOffset != 3 {
		t.Errorf("scrollOffset = %d, want 3", m.scrollOffset)
	}
}

// TestEnsureCursorVisible_ScrollDown verifies that when the cursor is below the
// visible area the offset advances so the cursor is at the bottom of the window.
func TestEnsureCursorVisible_ScrollDown(t *testing.T) {
	m := newTestModel(24, 20)
	// viewOverheadLines() returns 4 for a plain list with no banners and height<35.
	// sessionLines = (24-2) - 4 = 18.  With scrollOffset=0, visibleEnd = 17.
	m.scrollOffset = 0
	m.cursor = 19 // session index 19 (last), below visibleEnd of 17

	m.ensureCursorVisible()

	// scrollOffset should be cursor - sessionLines + 1 = 19 - 18 + 1 = 2
	if m.scrollOffset != 2 {
		t.Errorf("scrollOffset = %d, want 2", m.scrollOffset)
	}
}

// TestEnsureCursorVisible_CursorAlreadyVisible checks that scrollOffset is
// unchanged when the cursor already falls within the visible window.
func TestEnsureCursorVisible_CursorAlreadyVisible(t *testing.T) {
	m := newTestModel(24, 20)
	m.scrollOffset = 0
	m.cursor = 5

	m.ensureCursorVisible()

	if m.scrollOffset != 0 {
		t.Errorf("scrollOffset = %d, want 0 (cursor was already visible)", m.scrollOffset)
	}
}

// TestEnsureCursorVisible_ScrollOffsetNeverNegative verifies the clamp that
// prevents scrollOffset from going below zero.
func TestEnsureCursorVisible_ScrollOffsetNeverNegative(t *testing.T) {
	m := newTestModel(24, 5)
	// With 5 sessions and sessionLines=18, scrollOffset should never go negative.
	m.scrollOffset = 0
	m.cursor = 4

	m.ensureCursorVisible()

	if m.scrollOffset < 0 {
		t.Errorf("scrollOffset = %d, must not be negative", m.scrollOffset)
	}
}

// TestEnsureCursorVisible_BudgetFloor confirms the minimum sessionLines floor
// of 5 is applied when the terminal is very short.
func TestEnsureCursorVisible_BudgetFloor(t *testing.T) {
	// height=8 -> sessionLines = (8-2) - 4 = 2, which is below the floor of 5.
	// So sessionLines is clamped to 5, visibleEnd = 0 + 5 - 1 = 4.
	m := newTestModel(8, 10)
	m.scrollOffset = 0
	m.cursor = 4 // index 4 == visibleEnd with clamped budget of 5

	m.ensureCursorVisible()

	if m.scrollOffset != 0 {
		t.Errorf("cursor at visibleEnd: scrollOffset = %d, want 0", m.scrollOffset)
	}

	// Moving cursor one further should cause a scroll.
	m.cursor = 5
	m.ensureCursorVisible()

	if m.scrollOffset != 1 {
		t.Errorf("cursor past visibleEnd: scrollOffset = %d, want 1", m.scrollOffset)
	}
}

// ---------------------------------------------------------------------------
// filterScrollOffset computation (tested via renderSessionList output)
// ---------------------------------------------------------------------------

// makeFilterModel returns a model configured for filter mode with the given
// budget and a specified number of matching sessions, with searchCursor at
// the supplied position.
func makeFilterModel(budget, matchCount, searchCursor int) *model {
	m := newTestModel(50, matchCount)
	m.filterActive = true
	m.searchFilter = "x"
	indices := make([]int, matchCount)
	for i := range indices {
		indices[i] = i
	}
	m.filteredIndices = indices
	m.searchCursor = searchCursor
	return m
}

// TestFilterScrollOffset_NoScrollWhenFits checks that when all results fit
// within the budget no "↑ more" indicator is emitted.
func TestFilterScrollOffset_NoScrollWhenFits(t *testing.T) {
	// budget=10, 5 results, searchCursor=0 — all results fit, no scroll needed.
	m := makeFilterModel(10, 5, 0)
	entries := m.buildFilteredEntries()

	var w strings.Builder
	m.renderSessionList(&w, entries, false, 10)

	output := w.String()
	if strings.Contains(output, "↑ more") {
		t.Errorf("did not expect '↑ more' when all results fit in budget")
	}
}

// TestFilterScrollOffset_ScrollAppliedWhenCursorExceedsBudget verifies that
// when searchCursor > budget-3 the "↑ more" indicator is shown, confirming
// filterScrollOffset is non-zero.
func TestFilterScrollOffset_ScrollAppliedWhenCursorExceedsBudget(t *testing.T) {
	// budget=5, 10 results, searchCursor=5.
	// filterScrollOffset = searchCursor - (budget-3) = 5 - 2 = 3 > 0.
	m := makeFilterModel(5, 10, 5)
	entries := m.buildFilteredEntries()

	var w strings.Builder
	m.renderSessionList(&w, entries, false, 5)

	output := w.String()
	if !strings.Contains(output, "↑ more") {
		t.Errorf("expected '↑ more' indicator when cursor is beyond the scroll threshold")
	}
}

// TestFilterScrollOffset_ThresholdBoundary_AtEdge checks the boundary where
// searchCursor == budget-3 (the last position before scrolling begins).
// filterScrollOffset should be 0, so no "↑ more" line is rendered.
func TestFilterScrollOffset_ThresholdBoundary_AtEdge(t *testing.T) {
	// budget=5, searchCursor=2 (== budget-3=2). filterScrollOffset = 0.
	m := makeFilterModel(5, 10, 2)
	entries := m.buildFilteredEntries()

	var w strings.Builder
	m.renderSessionList(&w, entries, false, 5)

	output := w.String()
	if strings.Contains(output, "↑ more") {
		t.Errorf("did not expect '↑ more' when searchCursor == budget-3 (threshold, not yet scrolled)")
	}
}

// TestFilterScrollOffset_ThresholdBoundary_OneOver checks the boundary where
// searchCursor == budget-2 (one over the threshold), which should trigger
// filterScrollOffset=1 and therefore the "↑ more" indicator.
func TestFilterScrollOffset_ThresholdBoundary_OneOver(t *testing.T) {
	// budget=5, searchCursor=3 (== budget-2). filterScrollOffset = 3 - 2 = 1.
	m := makeFilterModel(5, 10, 3)
	entries := m.buildFilteredEntries()

	var w strings.Builder
	m.renderSessionList(&w, entries, false, 5)

	output := w.String()
	if !strings.Contains(output, "↑ more") {
		t.Errorf("expected '↑ more' when searchCursor == budget-2 (one past threshold)")
	}
}

// TestFilterScrollOffset_SmallBudgets exercises the degenerate budget values
// (1, 2, 3) that could cause negative or zero thresholds. The code must not
// panic and should behave consistently.
func TestFilterScrollOffset_SmallBudgets(t *testing.T) {
	for _, budget := range []int{1, 2, 3} {
		m := makeFilterModel(budget, 5, 0)
		entries := m.buildFilteredEntries()

		var w strings.Builder
		// Must not panic.
		m.renderSessionList(&w, entries, false, budget)
	}
}

// ---------------------------------------------------------------------------
// buildFilteredEntries
// ---------------------------------------------------------------------------

// TestBuildFilteredEntries_NormalMode checks that without an active filter every
// session appears as an entry with filterIdx == -1.
func TestBuildFilteredEntries_NormalMode(t *testing.T) {
	m := newTestModel(24, 3)

	entries := m.buildFilteredEntries()

	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}
	for i, e := range entries {
		if e.sessIdx != i {
			t.Errorf("entries[%d].sessIdx = %d, want %d", i, e.sessIdx, i)
		}
		if e.filterIdx != -1 {
			t.Errorf("entries[%d].filterIdx = %d, want -1 (no filter)", i, e.filterIdx)
		}
	}
}

// TestBuildFilteredEntries_FilterMode checks that with an active filter the
// entries reflect filteredIndices, with filterIdx assigned by position.
func TestBuildFilteredEntries_FilterMode(t *testing.T) {
	m := newTestModel(24, 5)
	m.filterActive = true
	m.searchFilter = "q"
	m.filteredIndices = []int{1, 3} // sessions 1 and 3 match

	entries := m.buildFilteredEntries()

	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].sessIdx != 1 || entries[0].filterIdx != 0 {
		t.Errorf("entries[0] = {%d,%d}, want {1,0}", entries[0].sessIdx, entries[0].filterIdx)
	}
	if entries[1].sessIdx != 3 || entries[1].filterIdx != 1 {
		t.Errorf("entries[1] = {%d,%d}, want {3,1}", entries[1].sessIdx, entries[1].filterIdx)
	}
}

// TestBuildFilteredEntries_EmptyFilter checks that when filteredIndices is nil
// (filter active but no results) the result is an empty slice.
func TestBuildFilteredEntries_EmptyFilter(t *testing.T) {
	m := newTestModel(24, 5)
	m.filterActive = true
	m.searchFilter = "zzz"
	m.filteredIndices = []int{}

	entries := m.buildFilteredEntries()

	if len(entries) != 0 {
		t.Errorf("len(entries) = %d, want 0 for empty filter results", len(entries))
	}
}

// ---------------------------------------------------------------------------
// renderSessionList — "No matching sessions" empty-filter path
// ---------------------------------------------------------------------------

// TestRenderSessionList_NoMatchingMessage verifies the fallback message written
// when the filter is active but no sessions pass the filter.
func TestRenderSessionList_NoMatchingMessage(t *testing.T) {
	m := newTestModel(24, 3)
	m.filterActive = true
	m.searchFilter = "zzz"

	var w strings.Builder
	m.renderSessionList(&w, []displayEntry{}, false)

	if !strings.Contains(w.String(), "No matching sessions") {
		t.Errorf("expected 'No matching sessions' message, got %q", w.String())
	}
}
