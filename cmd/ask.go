package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jfox85/devx/ask"
	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var askJSON bool
var askNoAgent bool
var askTimeout string
var askApproveAlways bool

var askCmd = &cobra.Command{
	Use:   "ask <session> <question>",
	Short: "Ask another DevX session a question",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runAsk,
}

var askPendingCmd = &cobra.Command{
	Use:   "pending",
	Short: "List pending ask approvals",
	RunE: func(cmd *cobra.Command, args []string) error {
		reqs, err := ask.NewStore().Pending()
		if err != nil {
			return err
		}
		return printAskList(cmd, reqs)
	},
}

var askListCmd = &cobra.Command{
	Use:   "list",
	Short: "List ask requests",
	RunE: func(cmd *cobra.Command, args []string) error {
		reqs, err := ask.NewStore().List()
		if err != nil {
			return err
		}
		return printAskList(cmd, reqs)
	},
}

var askReadCmd = &cobra.Command{
	Use:   "read <request-id>",
	Short: "Read an ask request",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		req, err := ask.NewStore().Get(args[0])
		if err != nil {
			return err
		}
		return printAsk(cmd, req)
	},
}

var askApproveCmd = &cobra.Command{
	Use:   "approve <request-id>",
	Short: "Approve a pending ask and run the target responder",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store := ask.NewStore()
		ctx := context.Background()
		policy := ask.Policy{}
		if askTimeout != "" {
			d, err := time.ParseDuration(askTimeout)
			if err != nil {
				return err
			}
			policy = ask.EffectivePolicy()
			policy.Timeout = d
		}
		var req *ask.Request
		var err error
		if askApproveAlways {
			req, err = store.ApproveAlwaysAndExecute(ctx, args[0], policy)
		} else {
			req, err = store.ApproveAndExecute(ctx, args[0], policy)
		}
		if err != nil {
			return err
		}
		return printAsk(cmd, req)
	},
}

var askDenyCmd = &cobra.Command{
	Use:   "deny <request-id>",
	Short: "Deny a pending ask",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store := ask.NewStore()
		req, err := store.Deny(args[0])
		if err != nil {
			return err
		}
		return printAsk(cmd, req)
	},
}

func runAsk(cmd *cobra.Command, args []string) error {
	targetName := args[0]
	question := strings.TrimSpace(strings.Join(args[1:], " "))
	if question == "" {
		return fmt.Errorf("question is required")
	}
	sessions, err := session.LoadSessions()
	if err != nil {
		return err
	}
	target, ok := sessions.GetSession(targetName)
	if !ok {
		return fmt.Errorf("session %q not found", targetName)
	}
	fromName := session.GetCurrentSessionName()
	if fromName == "" {
		return fmt.Errorf("devx ask must be run from inside a DevX session so approvals have a requester identity")
	}
	fromPath := ""
	if fromName != "" {
		if from, ok := sessions.GetSession(fromName); ok {
			fromPath = from.Path
		}
	}
	askStore := ask.NewStore()
	req, err := askStore.Create(fromName, targetName, fromPath, target.Path, question)
	if err != nil {
		return err
	}
	if askNoAgent {
		return printAsk(cmd, req)
	}
	policy := ask.EffectivePolicy()
	if policy.Enabled && policy.Mode == "approval" {
		allowed, err := askStore.IsAllowed(fromName, targetName, fromPath, target.Path)
		if err != nil {
			return err
		}
		if allowed {
			req, err = askStore.ApproveAndExecute(context.Background(), req.ID, policy)
			if err != nil {
				return err
			}
			return printAsk(cmd, req)
		}
	}
	if askNoAgent || !policy.Enabled || policy.Mode == "none" || policy.Mode == "approval" {
		return printAsk(cmd, req)
	}
	if policy.Mode != "always" {
		return fmt.Errorf("unsupported agent_responder.mode %q", policy.Mode)
	}
	req, err = ask.Execute(context.Background(), req, target, ask.ExecuteOptions{Policy: policy, Store: askStore})
	if err != nil {
		return err
	}
	return printAsk(cmd, req)
}

func printAsk(cmd *cobra.Command, req *ask.Request) error {
	if askJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(req)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Ask %s: %s -> %s [%s]\n", req.ID, req.FromSession, req.ToSession, req.Status)
	fmt.Fprintf(cmd.OutOrStdout(), "Question: %s\n", req.Question)
	if req.Response != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", req.Response.Body)
	}
	if req.Error != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Error: %s\n", req.Error)
	}
	return nil
}

func printAskList(cmd *cobra.Command, reqs []*ask.Request) error {
	if askJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(reqs)
	}
	for _, req := range reqs {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s -> %s\t%s\t%s\n", req.ID, req.FromSession, req.ToSession, req.Status, req.Question)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(askCmd)
	askCmd.PersistentFlags().BoolVar(&askJSON, "json", false, "Output JSON")
	askCmd.Flags().BoolVar(&askNoAgent, "no-agent", false, "Create the ask without running a responder")
	askCmd.AddCommand(askPendingCmd, askListCmd, askReadCmd, askApproveCmd, askDenyCmd)
	askApproveCmd.Flags().BoolVar(&askApproveAlways, "always", false, "Approve this ask and remember this requester/target pair for future asks")
	askApproveCmd.Flags().StringVar(&askTimeout, "timeout", "", "Override responder timeout for approve")
}
