package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var (
	staleDaysFlag int
	pruneDaysFlag int
	staleJSONFlag bool
	pruneDryRun   bool
)

var sessionStaleCmd = &cobra.Command{
	Use:   "stale",
	Short: "Show stale sessions and cleanup safety",
	Long:  `Show sessions grouped by whether they are active, stale and safe to clean, stale and needing review, or broken.`,
	RunE:  runSessionStale,
}

var sessionPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove stale sessions that are safe to clean",
	Long:  `Remove only stale-clean sessions: old sessions with no active editor/tmux, no modified files, no untracked files, and no known unpushed commits. Ignored generated files alone do not block cleanup.`,
	RunE:  runSessionPrune,
}

func init() {
	sessionCmd.AddCommand(sessionStaleCmd)
	sessionCmd.AddCommand(sessionPruneCmd)

	sessionStaleCmd.Flags().IntVar(&staleDaysFlag, "days", 14, "Mark sessions stale after this many inactive days")
	sessionStaleCmd.Flags().BoolVar(&staleJSONFlag, "json", false, "Output stale session data as JSON")

	sessionPruneCmd.Flags().IntVar(&pruneDaysFlag, "days", 14, "Remove stale-clean sessions inactive for this many days")
	sessionPruneCmd.Flags().BoolVar(&pruneDryRun, "dry-run", false, "Show what would be removed without deleting anything")
	sessionPruneCmd.Flags().BoolVarP(&forceFlag, "force", "f", false, "Force removal without confirmation")
}

func runSessionStale(cmd *cobra.Command, args []string) error {
	threshold, err := daysToDuration(staleDaysFlag)
	if err != nil {
		return err
	}
	store, err := session.LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions: %w", err)
	}
	summary := session.AnalyzeStaleSessions(store, threshold)
	if staleJSONFlag {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(summary)
	}
	displayStaleSummary(summary)
	return nil
}

func runSessionPrune(cmd *cobra.Command, args []string) (retErr error) {
	threshold, err := daysToDuration(pruneDaysFlag)
	if err != nil {
		return err
	}
	store, err := session.LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions: %w", err)
	}
	summary := session.AnalyzeStaleSessions(store, threshold)
	var clean []session.StaleStatus
	for _, status := range summary.Statuses {
		if status.Category == session.StaleCategoryClean {
			clean = append(clean, status)
		}
	}
	if len(clean) == 0 {
		fmt.Printf("No stale-clean sessions older than %d day(s).\n", summary.ThresholdDays)
		if summary.NeedsReview > 0 {
			fmt.Printf("%d stale session(s) need review; run 'devx session stale --days %d'.\n", summary.NeedsReview, summary.ThresholdDays)
		}
		return nil
	}

	fmt.Printf("Stale-clean sessions older than %d day(s):\n", summary.ThresholdDays)
	for _, status := range clean {
		fmt.Printf("  %s (%s)\n", status.SessionName, staleReasons(status))
	}
	if pruneDryRun {
		fmt.Println("Dry run only; no sessions removed.")
		return nil
	}

	if !forceFlag {
		fmt.Printf("Remove %d stale-clean session(s)? (y/N): ", len(clean))
		var response string
		_, _ = fmt.Scanln(&response)
		if response != "y" && response != "Y" && response != "yes" && response != "Yes" {
			fmt.Println("Aborted")
			return nil
		}
	}

	removed := 0
	defer func() {
		if removed == 0 {
			return
		}
		if err := syncAllCaddyRoutes(); err != nil {
			fmt.Printf("Warning: failed to sync Caddy routes: %v\n", err)
		}
		if err := syncAllCloudflareRoutes(); err != nil && retErr == nil {
			retErr = fmt.Errorf("removed %d session(s) locally, but failed to sync Cloudflare routes: %w", removed, err)
		}
	}()

	for _, status := range clean {
		currentStore, err := session.LoadSessions()
		if err != nil {
			return fmt.Errorf("failed to reload sessions before pruning %q: %w", status.SessionName, err)
		}
		current, exists := currentStore.GetSession(status.SessionName)
		if !exists {
			continue
		}
		currentStatus := session.AnalyzeStaleSession(current, threshold)
		if currentStatus.Category != session.StaleCategoryClean {
			fmt.Printf("Skipping %s; no longer stale-clean (%s).\n", status.SessionName, staleReasons(currentStatus))
			continue
		}
		if err := removeSessionByName(status.SessionName, removeSessionOptions{SkipConfirm: true, DiscardArtifacts: false, SyncRoutes: false}); err != nil {
			return err
		}
		removed++
	}
	fmt.Printf("Removed %d stale-clean session(s).\n", removed)
	if summary.NeedsReview > 0 {
		fmt.Printf("%d stale session(s) still need review.\n", summary.NeedsReview)
	}
	return nil
}

func displayStaleSummary(summary session.StaleSummary) {
	fmt.Printf("Stale session summary (threshold: %d day(s))\n", summary.ThresholdDays)
	fmt.Printf("  clean: %d  needs review: %d  broken: %d  active/recent: %d\n\n", summary.Clean, summary.NeedsReview, summary.Broken, summary.Active)
	displayStaleGroup("SAFE TO REMOVE", summary.Statuses, session.StaleCategoryClean)
	displayStaleGroup("NEEDS REVIEW", summary.Statuses, session.StaleCategoryNeedsReview)
	displayStaleGroup("BROKEN", summary.Statuses, session.StaleCategoryBroken)
}

func displayStaleGroup(title string, statuses []session.StaleStatus, category string) {
	var group []session.StaleStatus
	for _, status := range statuses {
		if status.Category == category {
			group = append(group, status)
		}
	}
	if len(group) == 0 {
		return
	}
	fmt.Println(title)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, status := range group {
		fmt.Fprintf(w, "  %s\t%s\t%s\n", status.SessionName, ageLabel(status.AgeSeconds), staleReasons(status))
	}
	_ = w.Flush()
	fmt.Println()
}

func daysToDuration(days int) (time.Duration, error) {
	threshold, err := session.StaleThresholdDuration(days)
	if err != nil {
		return 0, err
	}
	return threshold, nil
}

func ageLabel(ageSeconds int64) string {
	if ageSeconds < 0 {
		ageSeconds = 0
	}
	days := ageSeconds / int64((24 * time.Hour).Seconds())
	if days > 0 {
		return fmt.Sprintf("%dd", days)
	}
	hours := ageSeconds / int64(time.Hour.Seconds())
	return fmt.Sprintf("%dh", hours)
}

func staleReasons(status session.StaleStatus) string {
	if len(status.Reasons) == 0 {
		return status.Category
	}
	return strings.Join(status.Reasons, "; ")
}
