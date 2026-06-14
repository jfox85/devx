package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var (
	reviewBaseFlag           string
	reviewJSONFlag           bool
	reviewNoPersistFlag      bool
	reviewHarnessFlag        string
	reviewHarnessCommandFlag string
	reviewTimeoutFlag        time.Duration
	reviewClearFlag          bool
)

var sessionReviewCmd = &cobra.Command{
	Use:   "review <name>",
	Short: "Review a session for cleanup-worthy work",
	Long: `Review a session/worktree against a base branch and summarize whether it
contains work worth preserving before cleanup. This command is advisory only; it
never deletes sessions or modifies worktree contents.`,
	Args: cobra.ExactArgs(1),
	RunE: runSessionReview,
}

func init() {
	sessionCmd.AddCommand(sessionReviewCmd)
	sessionReviewCmd.Flags().StringVar(&reviewBaseFlag, "base", "", "Base branch/ref to compare against (default: origin/main, main, origin/master, master)")
	sessionReviewCmd.Flags().BoolVar(&reviewJSONFlag, "json", false, "Print review as JSON")
	sessionReviewCmd.Flags().BoolVar(&reviewNoPersistFlag, "no-persist", false, "Do not save the review result to session metadata")
	sessionReviewCmd.Flags().StringVar(&reviewHarnessFlag, "harness", "", "Name of agent harness used for review output")
	sessionReviewCmd.Flags().StringVar(&reviewHarnessCommandFlag, "harness-command", "", "Command to run for agent review; placeholders: {prompt_file}, {prompt}, {session}, {path}, {base}")
	sessionReviewCmd.Flags().DurationVar(&reviewTimeoutFlag, "timeout", 5*time.Minute, "Agent harness timeout")
	sessionReviewCmd.Flags().BoolVar(&reviewClearFlag, "clear", false, "Clear stored review for this session")
}

func runSessionReview(cmd *cobra.Command, args []string) error {
	name := args[0]
	if reviewClearFlag {
		if err := session.ClearSessionReview(name); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Cleared review for %s\n", name)
		return nil
	}

	store, err := session.LoadSessions()
	if err != nil {
		return err
	}
	sess, ok := store.GetSession(name)
	if !ok {
		return fmt.Errorf("session %q not found", name)
	}

	review, err := session.ReviewSession(sess, session.ReviewOptions{BaseBranch: reviewBaseFlag})
	if err != nil {
		return err
	}
	if reviewHarnessCommandFlag != "" {
		harness := reviewHarnessFlag
		if harness == "" {
			harness = "custom"
		}
		ctx, cancel := context.WithTimeout(context.Background(), reviewTimeoutFlag)
		defer cancel()
		updated, harnessErr := session.RunReviewHarness(ctx, sess, review, harness, []string{"sh", "-lc", reviewHarnessCommandFlag})
		if updated != nil {
			review = updated
		}
		if harnessErr != nil {
			review.Error = harnessErr.Error()
		}
	} else if reviewHarnessFlag != "" {
		review.Harness = reviewHarnessFlag
		review.Details = "Harness name recorded, but no --harness-command was provided. Deterministic review only."
	}

	if !reviewNoPersistFlag {
		if err := store.UpdateSession(name, func(s *session.Session) { s.Review = review }); err != nil {
			return err
		}
	}

	if reviewJSONFlag {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(review)
	}
	printReview(cmd, name, review)
	return nil
}

func printReview(cmd *cobra.Command, name string, review *session.SessionReview) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Session: %s\n", name)
	fmt.Fprintf(out, "Classification: %s\n", review.Classification)
	if review.BaseBranch != "" {
		fmt.Fprintf(out, "Base: %s\n", review.BaseBranch)
	}
	if review.Harness != "" {
		fmt.Fprintf(out, "Harness: %s\n", review.Harness)
	}
	if review.Summary != "" {
		fmt.Fprintf(out, "Summary: %s\n", review.Summary)
	}
	if len(review.UniqueCommits) > 0 {
		fmt.Fprintf(out, "\nUnique commits:\n")
		for _, c := range review.UniqueCommits {
			fmt.Fprintf(out, "  %s\n", c)
		}
	}
	if len(review.ChangedFiles) > 0 {
		fmt.Fprintf(out, "\nChanged vs base:\n")
		for _, f := range review.ChangedFiles {
			fmt.Fprintf(out, "  %s\n", f)
		}
	}
	if len(review.DirtyFiles) > 0 {
		fmt.Fprintf(out, "\nDirty files:\n")
		for _, f := range review.DirtyFiles {
			fmt.Fprintf(out, "  %s\n", f)
		}
	}
	if len(review.UntrackedFiles) > 0 {
		fmt.Fprintf(out, "\nUntracked files:\n")
		for _, f := range review.UntrackedFiles {
			fmt.Fprintf(out, "  %s\n", f)
		}
	}
	if review.Details != "" {
		fmt.Fprintf(out, "\nAgent details:\n%s\n", review.Details)
	}
	if review.Error != "" {
		fmt.Fprintf(out, "\nError: %s\n", review.Error)
	}
}
