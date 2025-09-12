package caddy

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// NormalizeDNSName converts a service name to be DNS-compatible
func NormalizeDNSName(serviceName string) string {
	// Convert to lowercase
	normalized := strings.ToLower(serviceName)

	// Replace underscores and spaces with hyphens
	normalized = strings.ReplaceAll(normalized, "_", "-")
	normalized = strings.ReplaceAll(normalized, " ", "-")

	// Replace any non-alphanumeric characters with hyphens
	var result strings.Builder
	for _, r := range normalized {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			result.WriteRune(r)
		} else if r != '-' {
			// Replace any non-alphanumeric character with hyphen
			result.WriteRune('-')
		} else {
			result.WriteRune(r)
		}
	}

	// Remove leading/trailing hyphens and collapse multiple hyphens
	final := strings.Trim(result.String(), "-")
	for strings.Contains(final, "--") {
		final = strings.ReplaceAll(final, "--", "-")
	}

	return final
}

// SanitizeHostname converts a session name to be hostname-compatible
func SanitizeHostname(sessionName string) string {
	// Convert to lowercase
	normalized := strings.ToLower(sessionName)

	// Replace slashes, underscores, and spaces with hyphens
	normalized = strings.ReplaceAll(normalized, "/", "-")
	normalized = strings.ReplaceAll(normalized, "_", "-")
	normalized = strings.ReplaceAll(normalized, " ", "-")

	// Replace any non-alphanumeric characters with hyphens
	var result strings.Builder
	for _, r := range normalized {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			result.WriteRune(r)
		} else if r != '-' {
			// Replace any non-alphanumeric character with hyphen
			result.WriteRune('-')
		} else {
			result.WriteRune(r)
		}
	}

	// Remove leading/trailing hyphens and collapse multiple hyphens
	final := strings.Trim(result.String(), "-")
	for strings.Contains(final, "--") {
		final = strings.ReplaceAll(final, "--", "-")
	}

	return final
}

// ProvisionSessionRoutes creates Caddy routes for all services in a session
func ProvisionSessionRoutes(sessionName string, services map[string]int) (map[string]string, error) {
	return ProvisionSessionRoutesWithProject(sessionName, services, "")
}

// ProvisionSessionRoutesWithProject creates Caddy routes for all services in a session with optional project prefix
func ProvisionSessionRoutesWithProject(sessionName string, services map[string]int, projectAlias string) (map[string]string, error) {
	// Check if Caddy provisioning is enabled
	if viper.GetBool("disable_caddy") {
		return make(map[string]string), nil
	}

	client := NewCaddyClient()

	// Check if Caddy is running
	if err := client.CheckCaddyConnection(); err != nil {
		fmt.Printf("Warning: Caddy not available, skipping route provisioning: %v\n", err)
		return make(map[string]string), nil
	}

	// Check if there are any catch-all routes that need to be moved to the end
	existingRoutes, err := client.GetAllRoutes()
	if err == nil {
		hasCatchAll := false
		for _, route := range existingRoutes {
			if route.ID == "" {
				hasCatchAll = true
				break
			}
		}

		// If there's a catch-all route, we'll need to reorder after adding new routes
		if hasCatchAll {
			defer func() {
				// Reorder routes to ensure specific routes come before catch-all
				if err := reorderRoutes(client); err != nil {
					fmt.Printf("Warning: Failed to reorder routes after creation: %v\n", err)
				}
			}()
		}
	}

	routes := make(map[string]string)
	var errors []string

	// Sanitize session name for hostname compatibility
	sanitizedSessionName := SanitizeHostname(sessionName)

	for serviceName, port := range services {
		// Normalize service name for DNS compatibility
		dnsServiceName := NormalizeDNSName(serviceName)

		if dnsServiceName == "" {
			errors = append(errors, fmt.Sprintf("service name '%s' cannot be converted to valid DNS name", serviceName))
			continue
		}

		_, err := client.CreateRouteWithProject(sanitizedSessionName, dnsServiceName, port, projectAlias)
		if err != nil {
			errors = append(errors, fmt.Sprintf("failed to create route for %s: %v", dnsServiceName, err))
			continue
		}

		// Generate route ID with project prefix if provided
		routeID := fmt.Sprintf("sess-%s-%s", sanitizedSessionName, dnsServiceName)
		if projectAlias != "" {
			routeID = fmt.Sprintf("sess-%s-%s-%s", projectAlias, sanitizedSessionName, dnsServiceName)
		}
		routes[serviceName] = routeID

		// Generate hostname with project prefix if provided
		hostname := fmt.Sprintf("%s-%s.localhost", sanitizedSessionName, dnsServiceName)
		if projectAlias != "" {
			hostname = fmt.Sprintf("%s-%s-%s.localhost", projectAlias, sanitizedSessionName, dnsServiceName)
		}

		fmt.Printf("Created route: http://%s -> port %d\n", hostname, port)
	}

	if len(errors) > 0 {
		return routes, fmt.Errorf("some routes failed: %s", strings.Join(errors, "; "))
	}

	return routes, nil
}

// DestroySessionRoutes removes all Caddy routes for a session
func DestroySessionRoutes(sessionName string, routes map[string]string) error {
	// Check if Caddy provisioning is enabled
	if viper.GetBool("disable_caddy") {
		return nil
	}

	client := NewCaddyClient()

	// Check if Caddy is running
	if err := client.CheckCaddyConnection(); err != nil {
		fmt.Printf("Warning: Caddy not available, skipping route cleanup: %v\n", err)
		return nil
	}

	// Delete all routes for the session
	if err := client.DeleteSessionRoutes(sessionName); err != nil {
		return fmt.Errorf("failed to delete session routes: %w", err)
	}

	fmt.Printf("Deleted Caddy routes for session '%s'\n", sessionName)
	return nil
}

// reorderRoutes moves specific routes before the catch-all routes
func reorderRoutes(client *CaddyClient) error {
	// Get current routes
	routes, err := client.GetAllRoutes()
	if err != nil {
		return err
	}

	// Separate specific routes (with IDs) and catch-all routes (without IDs)
	var specificRoutes, catchAllRoutes []Route
	for _, route := range routes {
		if route.ID != "" {
			specificRoutes = append(specificRoutes, route)
		} else {
			catchAllRoutes = append(catchAllRoutes, route)
		}
	}

	// If all routes are already in the correct order, no need to reorder
	if len(catchAllRoutes) == 0 || len(routes) == len(specificRoutes)+len(catchAllRoutes) {
		// Check if catch-all routes are already at the end
		foundCatchAll := false
		for _, route := range routes {
			if route.ID == "" {
				foundCatchAll = true
			} else if foundCatchAll {
				// Found a specific route after a catch-all, need to reorder
				break
			}
		}
		if !foundCatchAll || routes[len(routes)-1].ID == "" {
			// Already in correct order
			return nil
		}
	}

	// Combine with specific routes first
	orderedRoutes := append(specificRoutes, catchAllRoutes...)

	// Delete all routes and recreate in correct order
	if err := client.ReplaceAllRoutes(orderedRoutes); err != nil {
		return err
	}

	return nil
}
