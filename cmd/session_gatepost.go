package cmd

import (
	"context"
	"fmt"

	"github.com/jfox85/devx/session"
	"github.com/jfox85/devx/target"
	"github.com/spf13/cobra"
)

var sessionGatepostCmd = &cobra.Command{Use: "gatepost", Short: "Manage Gatepost-backed sessions"}

var sessionGatepostBypassCmd = &cobra.Command{
	Use:   "bypass <session>",
	Short: "Host-side emergency bypass for a Gatepost session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return setGatepostBypass(args[0], true)
	},
}

var sessionGatepostEnforceCmd = &cobra.Command{
	Use:   "enforce <session>",
	Short: "Re-enable Gatepost enforcement for a bypassed session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return setGatepostBypass(args[0], false)
	},
}

func init() {
	sessionCmd.AddCommand(sessionGatepostCmd)
	sessionGatepostCmd.AddCommand(sessionGatepostBypassCmd)
	sessionGatepostCmd.AddCommand(sessionGatepostEnforceCmd)
}

func setGatepostBypass(name string, bypass bool) error {
	store, err := session.LoadSessions()
	if err != nil {
		return err
	}
	sess, ok := store.GetSession(name)
	if !ok {
		return fmt.Errorf("session %q not found", name)
	}
	if sess.Target.Type != "gatepost" || !sess.Target.Gatepost.Enabled {
		return fmt.Errorf("session %q is not a gatepost session", name)
	}
	ctx := context.Background()
	if err := target.SetGatepostBypass(ctx, sess.Target, bypass); err != nil {
		return err
	}
	if bypass {
		fmt.Printf("Gatepost bypass enabled for %s. New traffic may use direct egress.\n", name)
	} else {
		fmt.Printf("Gatepost enforcement restored for %s.\n", name)
	}
	return store.UpdateSession(name, func(s *session.Session) { s.Target.Gatepost.Bypass = bypass })
}
