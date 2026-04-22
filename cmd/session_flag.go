package cmd

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	clearFlag     bool
	forceFlagFlag bool
)

var sessionFlagCmd = &cobra.Command{
	Use:   "flag <name> [reason]",
	Short: "Flag a session for attention",
	Long: `Flag a session to indicate it needs attention. This will show a visual indicator 
in the TUI and can be used by external tools (like Claude Code) to signal when work is complete.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runSessionFlag,
}

func init() {
	sessionCmd.AddCommand(sessionFlagCmd)
	sessionFlagCmd.Flags().BoolVar(&clearFlag, "clear", false, "Clear the attention flag instead of setting it")
	sessionFlagCmd.Flags().BoolVar(&forceFlagFlag, "force", false, "Force flagging even if it's the current session")
}

func runSessionFlag(cmd *cobra.Command, args []string) error {
	sessionName := args[0]

	if clearFlag {
		// Clear the flag
		if err := session.ClearAttentionFlag(sessionName); err != nil {
			return fmt.Errorf("failed to clear attention flag: %w", err)
		}
		fmt.Printf("Cleared attention flag for session '%s'\n", sessionName)
		notifyWebServer(sessionName, false, "")
		return nil
	}

	// Set the flag
	reason := "manual"
	if len(args) > 1 {
		reason = args[1]
	}

	// Check if this is the current session (unless forced)
	if !forceFlagFlag {
		currentSession := session.GetCurrentSessionName()
		if currentSession == sessionName {
			fmt.Printf("Not flagging session '%s' because it's currently active (use --force to override)\n", sessionName)
			return nil
		}
	}

	if err := session.SetAttentionFlag(sessionName, reason); err != nil {
		return fmt.Errorf("failed to set attention flag: %w", err)
	}

	fmt.Printf("Flagged session '%s' for attention (reason: %s)\n", sessionName, reason)
	notifyWebServer(sessionName, true, reason)
	return nil
}

// notifyWebServer fires a POST to /api/sessions/flag-notify so the browser
// learns about the flag change immediately via SSE. All errors are silently
// ignored — the web server may not be running, which is fine.
func notifyWebServer(name string, flagged bool, reason string) {
	token := viper.GetString("web_secret_token")
	port := viper.GetInt("web_port")
	if token == "" || port == 0 {
		return
	}
	flaggedStr := "false"
	if flagged {
		flaggedStr = "true"
	}
	q := url.Values{}
	q.Set("name", name)
	q.Set("flagged", flaggedStr)
	if reason != "" {
		q.Set("reason", reason)
	}
	addr := fmt.Sprintf("http://localhost:%d/api/sessions/flag-notify?%s", port, q.Encode())
	req, err := http.NewRequest(http.MethodPost, addr, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
