package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/jfox85/devx/caddy"
	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all development sessions",
	Long:  `List all development sessions with their status, ports, and routes.`,
	RunE:  runSessionList,
}

func init() {
	sessionCmd.AddCommand(sessionListCmd)
}

type SessionStatus struct {
	Name         string
	Branch       string
	Ports        map[string]int
	Routes       map[string]string
	TmuxStatus   string // "attached", "detached", "none"
	EditorStatus string // "running", "stopped"
	Path         string
}

func runSessionList(cmd *cobra.Command, args []string) error {
	// Load sessions from metadata
	store, err := session.LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions: %w", err)
	}

	if len(store.Sessions) == 0 {
		fmt.Println("No sessions found.")
		return nil
	}

	// Get tmux session status
	tmuxSessions := getTmuxSessions()

	// Get Caddy route status (if available)
	caddyRoutes := getCaddyRoutes()

	// Collect session statuses
	var statuses []SessionStatus
	for name, sess := range store.Sessions {
		status := SessionStatus{
			Name:   name,
			Branch: sess.Branch,
			Ports:  sess.Ports,
			Routes: sess.Routes,
			Path:   sess.Path,
		}

		// Check tmux status
		if tmuxInfo, exists := tmuxSessions[name]; exists {
			status.TmuxStatus = tmuxInfo.Status
		} else {
			status.TmuxStatus = "none"
		}

		// Check editor status
		if sess.EditorPID > 0 && session.IsProcessRunning(sess.EditorPID) {
			status.EditorStatus = "running"
		} else {
			status.EditorStatus = "stopped"
		}

		statuses = append(statuses, status)
	}

	// Sort by name
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Name < statuses[j].Name
	})

	// Display results
	displaySessionList(statuses, caddyRoutes)
	return nil
}

type TmuxSessionInfo struct {
	Name   string
	Status string // "attached" or "detached"
}

func getTmuxSessions() map[string]TmuxSessionInfo {
	sessions := make(map[string]TmuxSessionInfo)

	// Check if tmux is available
	if _, err := exec.LookPath("tmux"); err != nil {
		return sessions
	}

	// Get tmux session list
	cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}:#{session_attached}")
	output, err := cmd.Output()
	if err != nil {
		// No sessions or tmux error - return empty map
		return sessions
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			continue
		}

		name := parts[0]
		attached := parts[1] == "1"

		status := "detached"
		if attached {
			status = "attached"
		}

		sessions[name] = TmuxSessionInfo{
			Name:   name,
			Status: status,
		}
	}

	return sessions
}

func getCaddyRoutes() map[string]bool {
	routes := make(map[string]bool)

	// Skip if Caddy is disabled
	if viper.GetBool("disable_caddy") {
		return routes
	}

	client := caddy.NewCaddyClient()
	if err := client.CheckCaddyConnection(); err != nil {
		// Caddy not available
		return routes
	}

	// Get all routes with session prefix
	caddyRoutes, err := client.GetAllRoutes()
	if err != nil {
		return routes
	}

	// Extract session names from route IDs
	for _, route := range caddyRoutes {
		if strings.HasPrefix(route.ID, "sess-") {
			// Extract session name from route ID format: sess-<session>-<service>
			parts := strings.Split(route.ID, "-")
			if len(parts) >= 3 {
				sessionName := parts[1]
				routes[sessionName] = true
			}
		}
	}

	return routes
}

func displaySessionList(statuses []SessionStatus, caddyRoutes map[string]bool) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer w.Flush()

	// Header
	fmt.Fprintln(w, "NAME\tBRANCH\tPORTS\tHOSTS\tSTATUS")
	fmt.Fprintln(w, "----\t------\t-----\t-----\t------")

	for _, status := range statuses {
		// Format ports
		var portsList []string
		for serviceName, port := range status.Ports {
			portVar := strings.ToUpper(serviceName) + "_PORT"
			portsList = append(portsList, fmt.Sprintf("%s:%d", portVar, port))
		}
		sort.Strings(portsList)
		portsStr := strings.Join(portsList, ",")
		if len(portsStr) > 30 {
			portsStr = portsStr[:27] + "..."
		}

		// Format hosts
		var hostsList []string
		baseDomain := viper.GetString("basedomain")
		for service := range status.Routes {
			host := fmt.Sprintf("%s-%s.%s", status.Name, service, baseDomain)
			hostsList = append(hostsList, host)
		}
		sort.Strings(hostsList)
		hostsStr := strings.Join(hostsList, ",")
		if len(hostsStr) > 30 {
			hostsStr = hostsStr[:27] + "..."
		}

		// Format status
		var statusParts []string

		// Tmux status
		switch status.TmuxStatus {
		case "attached":
			statusParts = append(statusParts, "tmux:attached")
		case "detached":
			statusParts = append(statusParts, "tmux:detached")
		default:
			statusParts = append(statusParts, "tmux:none")
		}

		// Editor status
		if status.EditorStatus == "running" {
			statusParts = append(statusParts, "editor:running")
		} else {
			statusParts = append(statusParts, "editor:stopped")
		}

		// Caddy status
		if _, hasRoutes := caddyRoutes[status.Name]; hasRoutes {
			statusParts = append(statusParts, "caddy:active")
		} else if len(status.Routes) > 0 {
			statusParts = append(statusParts, "caddy:stale")
		}

		statusStr := strings.Join(statusParts, ",")

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			status.Name,
			status.Branch,
			portsStr,
			hostsStr,
			statusStr,
		)
	}
}
