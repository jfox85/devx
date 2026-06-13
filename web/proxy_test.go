package web

import (
	"net/http/httptest"
	"testing"
)

func TestEffectiveHostsIncludesForwardedHost(t *testing.T) {
	req := httptest.NewRequest("GET", "/terminal/demo/ws", nil)
	req.Host = "localhost"
	req.Header.Set("X-Forwarded-Host", "devx-demo-web.example.com")

	hosts := effectiveHosts(req)
	if len(hosts) != 2 || hosts[0] != "localhost" || hosts[1] != "devx-demo-web.example.com" {
		t.Fatalf("unexpected effectiveHosts: %#v", hosts)
	}
}

func TestEffectiveHostsTakesFirstForwardedHostValue(t *testing.T) {
	req := httptest.NewRequest("GET", "/terminal/demo/ws", nil)
	req.Host = "localhost"
	req.Header.Set("X-Forwarded-Host", " devx-demo-web.example.com , internal")

	hosts := effectiveHosts(req)
	if len(hosts) != 2 || hosts[1] != "devx-demo-web.example.com" {
		t.Fatalf("expected first forwarded host trimmed, got %#v", hosts)
	}
}

func TestEffectiveHostsNoForwardedHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "/terminal/demo/ws", nil)
	req.Host = "localhost:7777"

	hosts := effectiveHosts(req)
	if len(hosts) != 1 || hosts[0] != "localhost:7777" {
		t.Fatalf("expected only r.Host, got %#v", hosts)
	}
}

func TestUpgraderCheckOriginHonorsForwardedHost(t *testing.T) {
	req := httptest.NewRequest("GET", "/terminal/demo/ws", nil)
	req.Host = "localhost"
	req.Header.Set("X-Forwarded-Host", "devx-demo-web.example.com")
	req.Header.Set("Origin", "https://devx-demo-web.example.com")

	if !upgrader.CheckOrigin(req) {
		t.Fatal("WebSocket CheckOrigin should accept forwarded-host Origin")
	}
}

func TestUpgraderCheckOriginRejectsForeignOrigin(t *testing.T) {
	req := httptest.NewRequest("GET", "/terminal/demo/ws", nil)
	req.Host = "localhost"
	req.Header.Set("X-Forwarded-Host", "devx-demo-web.example.com")
	req.Header.Set("Origin", "https://evil.example.com")

	if upgrader.CheckOrigin(req) {
		t.Fatal("WebSocket CheckOrigin must reject a foreign Origin")
	}
}

func TestUpgraderCheckOriginAllowsMissingOrigin(t *testing.T) {
	req := httptest.NewRequest("GET", "/terminal/demo/ws", nil)
	req.Host = "localhost"

	if !upgrader.CheckOrigin(req) {
		t.Fatal("WebSocket CheckOrigin should allow a missing Origin (same-origin)")
	}
}
