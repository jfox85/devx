package target

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

type gatepostSecret struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Scheme   string `json:"scheme"`
	Header   string `json:"header"`
	Username string `json:"username,omitempty"`
	Value    string `json:"value"`
}

func gatepostSecretsFromEnv() []gatepostSecret {
	secrets := []gatepostSecret{}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		secrets = append(secrets, gatepostSecret{Name: "openai-key", Host: "api.openai.com", Scheme: "bearer", Header: "Authorization", Value: v})
	}
	if v := os.Getenv("GEMINI_API_KEY"); v != "" {
		secrets = append(secrets, gatepostSecret{Name: "gemini-key", Host: "generativelanguage.googleapis.com", Scheme: "header", Header: "x-goog-api-key", Value: v})
	}
	if v := os.Getenv("CLIPROXYAPI_API_KEY"); v != "" {
		secrets = append(secrets, gatepostSecret{Name: "cliproxy-key", Host: "host.docker.internal", Scheme: "bearer", Header: "Authorization", Value: v})
	}
	return secrets
}

func registerGatepostSecrets(controlURL, token string) ([]string, error) {
	registered := []string{}
	for _, secret := range gatepostSecretsFromEnv() {
		if err := postGatepostSecret(controlURL, token, secret); err != nil {
			return registered, err
		}
		registered = append(registered, secret.Name)
	}
	return registered, nil
}

func postGatepostSecret(controlURL, token string, secret gatepostSecret) error {
	body, err := json.Marshal(secret)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, controlURL+"/secrets", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("register gatepost secret %s: status %d", secret.Name, resp.StatusCode)
	}
	return nil
}
