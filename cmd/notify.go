package cmd

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/spf13/viper"
)

// notifySessionUpdated fires a POST to /api/sessions/flag-notify with
// flagged=false so the web UI re-polls its session list. This is a
// lightweight signal that session metadata changed — the browser's
// background poll (5s) would eventually pick it up, but this makes it
// immediate.
func notifySessionUpdated(name string) {
	token := viper.GetString("web_secret_token")
	port := viper.GetInt("web_port")
	if token == "" || port == 0 {
		return
	}
	q := url.Values{}
	q.Set("name", name)
	q.Set("flagged", "false")
	addr := fmt.Sprintf("http://localhost:%d/api/sessions/flag-notify?%s", port, q.Encode())
	req, err := http.NewRequest(http.MethodPost, addr, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
