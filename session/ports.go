package session

import (
	"fmt"

	getport "github.com/jsumners/go-getport"
)

// PortAllocation represents allocated ports with their service names
type PortAllocation struct {
	Ports map[string]int // service name -> port number
}

// AllocatePorts allocates unique ports for the given service names
func AllocatePorts(serviceNames []string) (*PortAllocation, error) {
	if len(serviceNames) == 0 {
		return &PortAllocation{Ports: make(map[string]int)}, nil
	}

	allocation := &PortAllocation{
		Ports: make(map[string]int),
	}

	usedPorts := make(map[int]bool)

	for _, serviceName := range serviceNames {
		var port int

		// Try to get a unique port (max 20 attempts)
		for attempts := 0; attempts < 20; attempts++ {
			result, err := getport.GetPort(getport.TCP, "")
			if err != nil {
				return nil, fmt.Errorf("failed to allocate port for %s: %w", serviceName, err)
			}

			port = result.Port
			if !usedPorts[port] {
				break
			}

			if attempts == 19 {
				return nil, fmt.Errorf("failed to allocate unique port for %s after 20 attempts", serviceName)
			}
		}

		allocation.Ports[serviceName] = port
		usedPorts[port] = true
	}

	return allocation, nil
}

// AllocatePortsLegacy allocates two unique ports for frontend and API (backwards compatibility)
func AllocatePortsLegacy() (fePort, apiPort int, err error) {
	allocation, err := AllocatePorts([]string{"ui", "api"})
	if err != nil {
		return 0, 0, err
	}

	return allocation.Ports["ui"], allocation.Ports["api"], nil
}

// ValidatePort checks if a port number is valid
func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port %d is out of valid range (1-65535)", port)
	}
	if port < 1024 {
		return fmt.Errorf("port %d requires root privileges (< 1024)", port)
	}
	return nil
}
