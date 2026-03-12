package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var showCmd = &cobra.Command{
	Use:   "show <path>",
	Short: "Push an image to the browser",
	Long: `Uploads an image file to the devx web server and triggers a toast
notification in the browser, so you can view it from a remote device.

Example:
  devx show /tmp/screenshot.png`,
	Args: cobra.ExactArgs(1),
	RunE: runShow,
}

func init() {
	rootCmd.AddCommand(showCmd)
}

func runShow(cmd *cobra.Command, args []string) error {
	filePath := args[0]

	token := viper.GetString("web_secret_token")
	if token == "" {
		return fmt.Errorf("web_secret_token is not set in config; devx web is not configured")
	}
	port := viper.GetInt("web_port")

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("cannot open file: %w", err)
	}
	defer f.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("image", filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("failed to create form: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}
	mw.Close()

	url := fmt.Sprintf("http://localhost:%d/api/show", port)
	req, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("web server not reachable (is devx web running?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err == nil && errResp["error"] != "" {
			return fmt.Errorf("server error: %s", errResp["error"])
		}
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("invalid server response: %w", err)
	}

	fmt.Printf("http://localhost:%d%s\n", port, result["url"])
	return nil
}
