package session

import (
	"fmt"
	"testing"
)

func TestUniquePorts(t *testing.T) {
	// Test that AllocatePorts returns unique ports
	// We'll allocate many ports at once to test uniqueness
	serviceNames := make([]string, 20)
	for i := 0; i < 20; i++ {
		serviceNames[i] = fmt.Sprintf("service%d", i)
	}
	
	allocation, err := AllocatePorts(serviceNames)
	if err != nil {
		t.Fatalf("failed to allocate ports: %v", err)
	}
	
	seen := map[int]bool{}
	for name, port := range allocation.Ports {
		if seen[port] {
			t.Fatalf("duplicate port %d for service %s", port, name)
		}
		seen[port] = true
	}
	
	if len(allocation.Ports) != 20 {
		t.Errorf("expected 20 unique ports, got %d", len(allocation.Ports))
	}
}

func TestAllocatePorts(t *testing.T) {
	portNames := []string{"TEST_PORT1", "TEST_PORT2", "TEST_PORT3"}
	allocation, err := AllocatePorts(portNames)
	if err != nil {
		t.Fatalf("failed to allocate ports: %v", err)
	}

	if len(allocation.Ports) != 3 {
		t.Errorf("expected 3 ports, got %d", len(allocation.Ports))
	}

	// Check all ports are unique
	seenPorts := make(map[int]bool)
	for name, port := range allocation.Ports {
		if seenPorts[port] {
			t.Errorf("duplicate port %d for %s", port, name)
		}
		seenPorts[port] = true

		if port < 1024 || port > 65535 {
			t.Errorf("port %d for %s out of valid range", port, name)
		}
	}

	// Check all expected port names are present
	for _, name := range portNames {
		if _, exists := allocation.Ports[name]; !exists {
			t.Errorf("expected port name %s not found", name)
		}
	}
}

func TestAllocatePortsLegacy(t *testing.T) {
	fePort, apiPort, err := AllocatePortsLegacy()
	if err != nil {
		t.Fatalf("failed to allocate ports: %v", err)
	}

	if fePort == apiPort {
		t.Errorf("allocated same port for frontend and API: %d", fePort)
	}

	if fePort < 1024 || fePort > 65535 {
		t.Errorf("frontend port %d out of valid range", fePort)
	}

	if apiPort < 1024 || apiPort > 65535 {
		t.Errorf("API port %d out of valid range", apiPort)
	}
}

func TestValidatePort(t *testing.T) {
	tests := []struct {
		port    int
		wantErr bool
	}{
		{8080, false},
		{3000, false},
		{65535, false},
		{1024, false},
		{1023, true},  // requires root
		{80, true},    // requires root
		{0, true},     // invalid
		{-1, true},    // invalid
		{65536, true}, // out of range
	}

	for _, tt := range tests {
		err := ValidatePort(tt.port)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidatePort(%d) error = %v, wantErr %v", tt.port, err, tt.wantErr)
		}
	}
}

func TestAllocatePortsUniqueness(t *testing.T) {
	// Test multiple concurrent allocations
	portNames := []string{"PORT1", "PORT2", "PORT3"}
	allocations := make([]*PortAllocation, 10)

	for i := 0; i < 10; i++ {
		allocation, err := AllocatePorts(portNames)
		if err != nil {
			t.Fatalf("failed to allocate ports on iteration %d: %v", i, err)
		}
		allocations[i] = allocation
	}

	// Check all ports are unique across all allocations
	seen := map[int]bool{}
	for i, allocation := range allocations {
		for name, port := range allocation.Ports {
			if seen[port] {
				t.Errorf("duplicate port %d for %s at iteration %d", port, name, i)
			}
			seen[port] = true
		}
	}
}

func TestAllocatePortsEmpty(t *testing.T) {
	allocation, err := AllocatePorts([]string{})
	if err != nil {
		t.Fatalf("failed to allocate empty ports: %v", err)
	}

	if len(allocation.Ports) != 0 {
		t.Errorf("expected 0 ports for empty list, got %d", len(allocation.Ports))
	}
}
