package session

import "time"

// Service status constants
const (
	StatusRunning = "running"
	StatusStopped = "stopped"
	StatusFailed  = "failed"
)

// ServiceState represents runtime state of a managed service
type ServiceState struct {
	Name      string    `json:"name"`
	PID       int       `json:"pid"`
	PGID      int       `json:"pgid"` // process group ID
	StartTime time.Time `json:"start_time"`
	Port      int       `json:"port"`
	Status    string    `json:"status"` // running, stopped, failed
	LogFile   string    `json:"log_file"`
}

// RouteMetadata stores route info for reconstruction after Caddy restart
type RouteMetadata struct {
	Hostname string `json:"hostname"`
	Port     int    `json:"port"`
	RouteID  string `json:"route_id,omitempty"`
}
