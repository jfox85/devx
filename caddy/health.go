package caddy

import (
	"fmt"
	"strings"
)

// RouteStatus represents the status of a Caddy route
type RouteStatus struct {
	SessionName string
	ServiceName string
	RouteID     string
	Hostname    string
	Port        int
	Exists      bool
	IsFirst     bool // Whether route appears before catch-all
	ServiceUp   bool // Whether the service is responding
	Error       string
}

// HealthCheckResult contains the overall Caddy health status
type HealthCheckResult struct {
	CaddyRunning   bool
	CaddyError     string
	RouteStatuses  []RouteStatus
	CatchAllFirst  bool // Whether catch-all route is blocking specific routes
	RoutesNeeded   int
	RoutesExisting int
	RoutesWorking  int
}

// CheckCaddyHealth performs a comprehensive health check of Caddy and all routes
func CheckCaddyHealth(sessions map[string]*SessionInfo) (*HealthCheckResult, error) {
	result := &HealthCheckResult{
		RouteStatuses: []RouteStatus{},
	}

	client := NewCaddyClient()

	// Check if Caddy is running
	if err := client.CheckCaddyConnection(); err != nil {
		result.CaddyRunning = false
		result.CaddyError = err.Error()
		return result, nil // Return result, not error, so we can show status
	}
	result.CaddyRunning = true

	// Get all current routes
	routes, err := client.GetAllRoutes()
	if err != nil {
		return nil, fmt.Errorf("failed to get routes: %w", err)
	}

	// Check if there are any catch-all routes (routes without IDs)
	// and if they appear before specific routes
	catchAllPosition := -1
	lastSpecificRoutePosition := -1

	for i, route := range routes {
		if route.ID == "" && catchAllPosition == -1 {
			catchAllPosition = i
		} else if route.ID != "" {
			lastSpecificRoutePosition = i
		}
	}

	// If catch-all exists and there are specific routes after it, routing is broken
	if catchAllPosition != -1 && lastSpecificRoutePosition > catchAllPosition {
		result.CatchAllFirst = true
	}

	// Build a map of existing routes
	existingRoutes := make(map[string]int) // routeID -> position
	for i, route := range routes {
		if route.ID != "" {
			existingRoutes[route.ID] = i
		}
	}

	// Check each session's expected routes
	for sessionName, sessionInfo := range sessions {
		for serviceName, port := range sessionInfo.Ports {
			// Normalize service name for DNS compatibility
			normalizedServiceName := NormalizeDNSName(serviceName)

			// Generate expected route ID and hostname
			routeID := fmt.Sprintf("sess-%s-%s", sessionName, normalizedServiceName)
			hostname := fmt.Sprintf("%s-%s.localhost", sessionName, normalizedServiceName)

			// Handle project prefixes if present
			if sessionInfo.ProjectAlias != "" {
				routeID = fmt.Sprintf("sess-%s-%s-%s", sessionInfo.ProjectAlias, sessionName, normalizedServiceName)
				hostname = fmt.Sprintf("%s-%s-%s.localhost", sessionInfo.ProjectAlias, sessionName, normalizedServiceName)
			}

			status := RouteStatus{
				SessionName: sessionName,
				ServiceName: serviceName,
				RouteID:     routeID,
				Hostname:    hostname,
				Port:        port,
			}

			result.RoutesNeeded++

			// Check if route exists
			if position, exists := existingRoutes[routeID]; exists {
				status.Exists = true
				status.IsFirst = !result.CatchAllFirst || position == 0
				result.RoutesExisting++

				// TODO: Check if service is actually responding
				// This would require making HTTP requests to test
				status.ServiceUp = true // Placeholder
				if status.ServiceUp {
					result.RoutesWorking++
				}
			}

			result.RouteStatuses = append(result.RouteStatuses, status)
		}
	}

	return result, nil
}

// SessionInfo represents basic session information needed for health checks
type SessionInfo struct {
	Name         string
	Ports        map[string]int
	ProjectAlias string
}

// RepairRoutes attempts to fix any routing issues found during health check
func RepairRoutes(result *HealthCheckResult, sessions map[string]*SessionInfo) error {
	client := NewCaddyClient()

	if !result.CaddyRunning {
		return fmt.Errorf("Caddy is not running")
	}

	// If catch-all is first, we need to reorder all routes
	if result.CatchAllFirst {
		fmt.Println("Fixing route order (catch-all route is blocking specific routes)...")
		if err := reorderRoutes(client); err != nil {
			return fmt.Errorf("failed to reorder routes: %w", err)
		}
	}

	// Create missing routes
	var errors []string
	for _, status := range result.RouteStatuses {
		if !status.Exists {
			fmt.Printf("Creating missing route for %s-%s...\n", status.SessionName, status.ServiceName)

			sessionInfo := sessions[status.SessionName]
			if sessionInfo == nil {
				errors = append(errors, fmt.Sprintf("session info not found for %s", status.SessionName))
				continue
			}

			// Use normalized service name for route creation
			normalizedServiceName := NormalizeDNSName(status.ServiceName)
			_, err := client.CreateRouteWithProject(
				status.SessionName,
				normalizedServiceName,
				status.Port,
				sessionInfo.ProjectAlias,
			)
			if err != nil {
				errors = append(errors, fmt.Sprintf("failed to create route for %s-%s: %v",
					status.SessionName, status.ServiceName, err))
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("some routes failed to create: %s", strings.Join(errors, "; "))
	}

	// If we created new routes and catch-all exists, reorder again
	if result.CatchAllFirst {
		fmt.Println("Reordering routes after creating new ones...")
		if err := reorderRoutes(client); err != nil {
			return fmt.Errorf("failed to reorder routes after creation: %w", err)
		}
	}

	return nil
}
