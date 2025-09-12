package caddy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/spf13/viper"
)

// Route represents a Caddy route configuration
type Route struct {
	ID       string         `json:"@id"`
	Match    []RouteMatch   `json:"match"`
	Handle   []RouteHandler `json:"handle"`
	Terminal bool           `json:"terminal"`
}

// RouteMatch represents the match criteria for a route
type RouteMatch struct {
	Host []string `json:"host"`
}

// RouteHandler represents a route handler
type RouteHandler struct {
	Handler   string          `json:"handler"`
	Upstreams []RouteUpstream `json:"upstreams,omitempty"`
}

// RouteUpstream represents an upstream server
type RouteUpstream struct {
	Dial string `json:"dial"`
}

// RouteResponse represents the response from Caddy when creating/updating routes
type RouteResponse struct {
	ETag string `json:"etag,omitempty"`
}

// CaddyClient manages communication with Caddy's admin API
type CaddyClient struct {
	client  *resty.Client
	baseURL string
}

// NewCaddyClient creates a new Caddy API client
func NewCaddyClient() *CaddyClient {
	caddyAPI := viper.GetString("caddy_api")
	if caddyAPI == "" {
		caddyAPI = "http://localhost:2019"
	}

	client := resty.New()
	client.SetTimeout(10 * time.Second)

	return &CaddyClient{
		client:  client,
		baseURL: caddyAPI,
	}
}

// CreateRoute creates a route for a service
func (c *CaddyClient) CreateRoute(sessionName, serviceName string, port int) (string, error) {
	return c.CreateRouteWithProject(sessionName, serviceName, port, "")
}

// CreateRouteWithProject creates a route for a service with optional project prefix
func (c *CaddyClient) CreateRouteWithProject(sessionName, serviceName string, port int, projectAlias string) (string, error) {
	// Use localhost for reliable resolution (works with both IPv4/IPv6)
	upstreams := []RouteUpstream{
		{Dial: fmt.Sprintf("localhost:%d", port)},
	}

	// Sanitize session name for hostname compatibility
	sanitizedSessionName := SanitizeHostname(sessionName)

	// Generate hostname with project prefix if provided
	hostname := fmt.Sprintf("%s-%s.localhost", sanitizedSessionName, serviceName)
	if projectAlias != "" {
		hostname = fmt.Sprintf("%s-%s-%s.localhost", projectAlias, sanitizedSessionName, serviceName)
	}

	// Generate route ID with project prefix if provided
	routeID := fmt.Sprintf("sess-%s-%s", sanitizedSessionName, serviceName)
	if projectAlias != "" {
		routeID = fmt.Sprintf("sess-%s-%s-%s", projectAlias, sanitizedSessionName, serviceName)
	}

	route := Route{
		ID: routeID,
		Match: []RouteMatch{
			{
				Host: []string{hostname},
			},
		},
		Handle: []RouteHandler{
			{
				Handler:   "reverse_proxy",
				Upstreams: upstreams,
			},
		},
		Terminal: true,
	}

	// Convert to JSON
	routeJSON, err := json.Marshal(route)
	if err != nil {
		return "", fmt.Errorf("failed to marshal route JSON: %w", err)
	}

	// POST to Caddy admin API
	resp, err := c.client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(routeJSON).
		Post(c.baseURL + "/config/apps/http/servers/srv1/routes")

	if err != nil {
		return "", fmt.Errorf("failed to create route: %w", err)
	}

	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusCreated {
		return "", fmt.Errorf("caddy API returned status %d: %s", resp.StatusCode(), resp.String())
	}

	// Extract ETag from response headers
	etag := resp.Header().Get("ETag")
	return etag, nil
}

// DeleteRoute deletes a route by ID
func (c *CaddyClient) DeleteRoute(routeID string) error {
	url := fmt.Sprintf("%s/id/%s", c.baseURL, routeID)

	resp, err := c.client.R().Delete(url)
	if err != nil {
		return fmt.Errorf("failed to delete route: %w", err)
	}

	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusNoContent && resp.StatusCode() != http.StatusNotFound {
		return fmt.Errorf("caddy API returned status %d: %s", resp.StatusCode(), resp.String())
	}

	return nil
}

// DeleteSessionRoutes deletes all routes for a session
func (c *CaddyClient) DeleteSessionRoutes(sessionName string) error {
	// Get list of all routes to find session routes
	routes, err := c.GetAllRoutes()
	if err != nil {
		return fmt.Errorf("failed to get routes: %w", err)
	}

	// Sanitize session name for hostname compatibility
	sanitizedSessionName := SanitizeHostname(sessionName)

	// Find and delete routes matching the session
	// Check both with and without project prefix, using both original and sanitized session names
	var errors []string

	for _, route := range routes {
		// Match routes that contain the session name in the expected pattern
		// This handles both sess-{session}-{service} and sess-{project}-{session}-{service}
		// Check both original and sanitized session names for backward compatibility
		if strings.Contains(route.ID, fmt.Sprintf("-%s-", sessionName)) || strings.HasPrefix(route.ID, fmt.Sprintf("sess-%s-", sessionName)) ||
			strings.Contains(route.ID, fmt.Sprintf("-%s-", sanitizedSessionName)) || strings.HasPrefix(route.ID, fmt.Sprintf("sess-%s-", sanitizedSessionName)) {
			if err := c.DeleteRoute(route.ID); err != nil {
				errors = append(errors, fmt.Sprintf("failed to delete route %s: %v", route.ID, err))
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("errors deleting routes: %s", strings.Join(errors, "; "))
	}

	return nil
}

// GetAllRoutes retrieves all routes from Caddy
func (c *CaddyClient) GetAllRoutes() ([]Route, error) {
	resp, err := c.client.R().Get(c.baseURL + "/config/apps/http/servers/srv1/routes")
	if err != nil {
		return nil, fmt.Errorf("failed to list routes: %w", err)
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("caddy API returned status %d: %s", resp.StatusCode(), resp.String())
	}

	// Parse routes response
	var routes []Route
	if err := json.Unmarshal(resp.Body(), &routes); err != nil {
		return nil, fmt.Errorf("failed to parse routes response: %w", err)
	}

	return routes, nil
}

// CheckCaddyConnection verifies that Caddy is running and accessible
func (c *CaddyClient) CheckCaddyConnection() error {
	resp, err := c.client.R().Get(c.baseURL + "/config/")
	if err != nil {
		return fmt.Errorf("failed to connect to Caddy admin API: %w", err)
	}

	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("caddy admin API returned status %d", resp.StatusCode())
	}

	return nil
}

// GetServiceMapping maps port environment variable names to service names
func GetServiceMapping(portName string) string {
	// Remove _PORT suffix if present
	serviceName := strings.TrimSuffix(portName, "_PORT")

	// Convert to lowercase
	serviceName = strings.ToLower(serviceName)

	// Apply special mappings
	switch serviceName {
	case "fe", "web", "frontend":
		return "ui"
	case "api", "backend":
		return "api"
	case "db", "database":
		return "db"
	default:
		// Replace underscores with hyphens for multi-word services
		return strings.ReplaceAll(serviceName, "_", "-")
	}
}

// ReplaceAllRoutes deletes all current routes and creates new ones in the specified order
func (c *CaddyClient) ReplaceAllRoutes(routes []Route) error {
	// First, delete all existing routes
	resp, err := c.client.R().Delete(c.baseURL + "/config/apps/http/servers/srv1/routes")
	if err != nil {
		return fmt.Errorf("failed to delete existing routes: %w", err)
	}

	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("failed to delete routes: status %d", resp.StatusCode())
	}

	// Then create new routes in the correct order
	routesJSON, err := json.Marshal(routes)
	if err != nil {
		return fmt.Errorf("failed to marshal routes: %w", err)
	}

	resp, err = c.client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(routesJSON).
		Post(c.baseURL + "/config/apps/http/servers/srv1/routes")

	if err != nil {
		return fmt.Errorf("failed to create routes: %w", err)
	}

	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusCreated {
		return fmt.Errorf("failed to create routes: status %d: %s", resp.StatusCode(), resp.String())
	}

	return nil
}
