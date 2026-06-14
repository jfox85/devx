package cmd

import "testing"

func TestDaysToDurationRejectsNonPositiveAndTooLarge(t *testing.T) {
	for _, days := range []int{0, -1} {
		if _, err := daysToDuration(days); err == nil {
			t.Fatalf("daysToDuration(%d) expected error", days)
		}
	}
	if _, err := daysToDuration(3651); err == nil {
		t.Fatal("daysToDuration above max expected error")
	}
}
