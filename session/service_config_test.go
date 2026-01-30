package session

import (
	"testing"
)

func TestParseServiceConfig(t *testing.T) {
	yaml := `
services:
  frontend:
    command: "npm run dev"
    cwd: frontend
    port: auto
    env:
      VITE_API_URL: "{{BACKEND_HOST}}"
  backend:
    command: "air"
    cwd: backend
    port: 8080
`
	configs, err := ParseServiceConfigs([]byte(yaml))
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	if len(configs) != 2 {
		t.Fatalf("expected 2 services, got %d", len(configs))
	}

	fe := configs["frontend"]
	if fe.Command != "npm run dev" {
		t.Errorf("expected command 'npm run dev', got '%s'", fe.Command)
	}
	if fe.Cwd != "frontend" {
		t.Errorf("expected cwd 'frontend', got '%s'", fe.Cwd)
	}
	if !fe.PortAuto {
		t.Error("expected frontend port to be auto")
	}

	be := configs["backend"]
	if be.Port != 8080 {
		t.Errorf("expected backend port 8080, got %d", be.Port)
	}
}
