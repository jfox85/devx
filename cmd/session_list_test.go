package cmd

import "testing"

func TestHasActiveCaddyRouteWithProjectPrefixedRouteID(t *testing.T) {
	status := SessionStatus{
		Name:         "jf-redesign",
		ProjectAlias: "croutoncreations",
		Routes:       map[string]string{"FRONTEND": "croutoncreations-jf-redesign-frontend.localhost"},
	}
	routes := map[string]bool{"sess-croutoncreations-jf-redesign-frontend": true}
	if !hasActiveCaddyRoute(status, routes) {
		t.Fatal("expected project-prefixed route ID to be active")
	}
}

func TestHasActiveCaddyRouteRejectsWrongProjectPrefix(t *testing.T) {
	status := SessionStatus{
		Name:         "jf-redesign",
		ProjectAlias: "croutoncreations",
		Routes:       map[string]string{"FRONTEND": "croutoncreations-jf-redesign-frontend.localhost"},
	}
	routes := map[string]bool{"sess-other-jf-redesign-frontend": true}
	if hasActiveCaddyRoute(status, routes) {
		t.Fatal("wrong project-prefixed route ID should not be active")
	}
}
