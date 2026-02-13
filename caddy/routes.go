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

// CaddyClient manages communication with Caddy's admin API
type CaddyClient struct {
	client     *resty.Client
	baseURL    string
	serverName string
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
		client:     client,
		baseURL:    caddyAPI,
		serverName: "devx",
	}
}

// serverPath returns the Caddy config path for the discovered HTTP server.
func (c *CaddyClient) serverPath() string {
	return "/config/apps/http/servers/" + c.serverName
}

// GetAllRoutes retrieves all routes from Caddy
func (c *CaddyClient) GetAllRoutes() ([]Route, error) {
	resp, err := c.client.R().Get(c.baseURL + c.serverPath() + "/routes")
	if err != nil {
		return nil, fmt.Errorf("failed to list routes: %w", err)
	}

	// Routes path doesn't exist yet — treat as empty
	if resp.StatusCode() == http.StatusNotFound {
		return []Route{}, nil
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("caddy API returned status %d: %s", resp.StatusCode(), resp.String())
	}

	// Handle null or empty body
	body := strings.TrimSpace(string(resp.Body()))
	if body == "null" || body == "" {
		return []Route{}, nil
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
