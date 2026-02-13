package caddy

import (
	"fmt"
	"sort"
)

// RouteStatus represents the status of a Caddy route
type RouteStatus struct {
	SessionName string
	ServiceName string
	RouteID     string
	Hostname    string
	Port        int
	Exists      bool
	Error       string
}

// HealthCheckResult contains the overall Caddy health status
type HealthCheckResult struct {
	CaddyRunning   bool
	CaddyError     string
	RouteStatuses  []RouteStatus
	RoutesNeeded   int
	RoutesExisting int
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

	// Build a map of existing routes
	existingRoutes := make(map[string]bool)
	for _, route := range routes {
		if route.ID != "" {
			existingRoutes[route.ID] = true
		}
	}

	// Check each session's expected routes
	for sessionName, sessionInfo := range sessions {
		for serviceName, port := range sessionInfo.Ports {
			hostname := BuildHostname(sessionName, serviceName, sessionInfo.ProjectAlias)
			if hostname == "" {
				continue
			}
			routeID := BuildRouteID(sessionName, serviceName, sessionInfo.ProjectAlias)

			status := RouteStatus{
				SessionName: sessionName,
				ServiceName: serviceName,
				RouteID:     routeID,
				Hostname:    hostname,
				Port:        port,
			}

			result.RoutesNeeded++

			// Check if route exists
			if existingRoutes[routeID] {
				status.Exists = true
				result.RoutesExisting++
			}

			result.RouteStatuses = append(result.RouteStatuses, status)
		}
	}

	// Sort route statuses for deterministic display output
	sort.Slice(result.RouteStatuses, func(i, j int) bool {
		if result.RouteStatuses[i].SessionName != result.RouteStatuses[j].SessionName {
			return result.RouteStatuses[i].SessionName < result.RouteStatuses[j].SessionName
		}
		return result.RouteStatuses[i].ServiceName < result.RouteStatuses[j].ServiceName
	})

	return result, nil
}

// SessionInfo represents basic session information needed for health checks
type SessionInfo struct {
	Name         string
	Ports        map[string]int
	ProjectAlias string
}
