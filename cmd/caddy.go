package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/jfox85/devx/caddy"
	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var (
	fixFlag bool
)

var caddyCmd = &cobra.Command{
	Use:   "caddy",
	Short: "Manage Caddy routes for development sessions",
	Long:  `Commands for managing and troubleshooting Caddy reverse proxy routes.`,
}

var caddyCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Check Caddy status and verify all session routes",
	Long:  `Checks if Caddy is running, verifies all session routes are properly configured, and identifies any issues.`,
	RunE:  runCaddyCheck,
}

func init() {
	rootCmd.AddCommand(caddyCmd)
	caddyCmd.AddCommand(caddyCheckCmd)
	caddyCheckCmd.Flags().BoolVarP(&fixFlag, "fix", "f", false, "Attempt to fix any issues found")
}

func runCaddyCheck(cmd *cobra.Command, args []string) error {
	// Load sessions
	store, err := session.LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions: %w", err)
	}
	
	// Load project registry to get project aliases
	registry, err := config.LoadProjectRegistry()
	if err != nil {
		return fmt.Errorf("failed to load project registry: %w", err)
	}
	
	// Convert sessions to format needed by health check
	sessionInfos := make(map[string]*caddy.SessionInfo)
	for name, sess := range store.Sessions {
		info := &caddy.SessionInfo{
			Name:  name,
			Ports: sess.Ports,
		}
		
		// Find project alias if session is in a project
		for alias, project := range registry.Projects {
			if sess.ProjectPath == project.Path {
				info.ProjectAlias = alias
				break
			}
		}
		
		sessionInfos[name] = info
	}
	
	// Perform health check
	result, err := caddy.CheckCaddyHealth(sessionInfos)
	if err != nil {
		return fmt.Errorf("failed to check Caddy health: %w", err)
	}
	
	// Display results
	displayHealthCheckResults(result)
	
	// Fix issues if requested
	if fixFlag && (result.RoutesNeeded > result.RoutesExisting || result.CatchAllFirst) {
		fmt.Println("\nAttempting to fix issues...")
		if err := caddy.RepairRoutes(result, sessionInfos); err != nil {
			return fmt.Errorf("failed to repair routes: %w", err)
		}
		
		// Re-run health check to show updated status
		fmt.Println("\nRechecking after repairs...")
		result, err = caddy.CheckCaddyHealth(sessionInfos)
		if err != nil {
			return fmt.Errorf("failed to recheck Caddy health: %w", err)
		}
		displayHealthCheckResults(result)
	}
	
	return nil
}

func displayHealthCheckResults(result *caddy.HealthCheckResult) {
	// Display Caddy status
	fmt.Println("=== Caddy Status ===")
	if result.CaddyRunning {
		fmt.Println("✓ Caddy is running")
	} else {
		fmt.Printf("✗ Caddy is not running: %s\n", result.CaddyError)
		fmt.Println("\nTo start Caddy, ensure it's installed and run:")
		fmt.Println("  caddy run --config ~/.config/devx/Caddyfile")
		return
	}
	
	// Display route summary
	fmt.Printf("\n=== Route Summary ===\n")
	fmt.Printf("Routes needed:   %d\n", result.RoutesNeeded)
	fmt.Printf("Routes existing: %d\n", result.RoutesExisting)
	fmt.Printf("Routes working:  %d\n", result.RoutesWorking)
	
	if result.CatchAllFirst {
		fmt.Println("\n⚠️  WARNING: Catch-all route is blocking specific routes!")
		fmt.Println("   This prevents session hostnames from working properly.")
		if !fixFlag {
			fmt.Println("   Run with --fix to repair this issue.")
		}
	}
	
	// Display individual route status
	if len(result.RouteStatuses) > 0 {
		fmt.Println("\n=== Route Details ===")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "SESSION\tSERVICE\tHOSTNAME\tPORT\tSTATUS")
		fmt.Fprintln(w, "-------\t-------\t--------\t----\t------")
		
		for _, status := range result.RouteStatuses {
			statusText := "✗ Missing"
			if status.Exists {
				if status.IsFirst || !result.CatchAllFirst {
					statusText = "✓ Configured"
				} else {
					statusText = "⚠️  Blocked"
				}
			}
			
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n",
				status.SessionName,
				status.ServiceName,
				status.Hostname,
				status.Port,
				statusText,
			)
		}
		w.Flush()
		
		// Show missing routes
		missingCount := 0
		for _, status := range result.RouteStatuses {
			if !status.Exists {
				missingCount++
			}
		}
		
		if missingCount > 0 && !fixFlag {
			fmt.Printf("\n%d routes are missing. Run with --fix to create them.\n", missingCount)
		}
	}
	
	// Final status
	fmt.Println()
	if result.RoutesNeeded == result.RoutesExisting && !result.CatchAllFirst {
		fmt.Println("✓ All routes are properly configured")
	} else {
		fmt.Println("✗ Some issues were found with Caddy routes")
		if !fixFlag {
			fmt.Println("  Run 'devx caddy check --fix' to attempt automatic repair")
		}
	}
}