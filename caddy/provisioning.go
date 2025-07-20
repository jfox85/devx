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
	
	// Replace underscores with hyphens
	normalized = strings.ReplaceAll(normalized, "_", "-")
	
	// Remove any non-alphanumeric characters except hyphens
	var result strings.Builder
	for _, r := range normalized {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
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
	
	routes := make(map[string]string)
	var errors []string
	
	for serviceName, port := range services {
		// Normalize service name for DNS compatibility
		dnsServiceName := NormalizeDNSName(serviceName)
		
		if dnsServiceName == "" {
			errors = append(errors, fmt.Sprintf("service name '%s' cannot be converted to valid DNS name", serviceName))
			continue
		}
		
		_, err := client.CreateRouteWithProject(sessionName, dnsServiceName, port, projectAlias)
		if err != nil {
			errors = append(errors, fmt.Sprintf("failed to create route for %s: %v", dnsServiceName, err))
			continue
		}
		
		// Generate route ID with project prefix if provided
		routeID := fmt.Sprintf("sess-%s-%s", sessionName, dnsServiceName)
		if projectAlias != "" {
			routeID = fmt.Sprintf("sess-%s-%s-%s", projectAlias, sessionName, dnsServiceName)
		}
		routes[serviceName] = routeID
		
		// Generate hostname with project prefix if provided
		hostname := fmt.Sprintf("%s-%s.localhost", sessionName, dnsServiceName)
		if projectAlias != "" {
			hostname = fmt.Sprintf("%s-%s-%s.localhost", projectAlias, sessionName, dnsServiceName)
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