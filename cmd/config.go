package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jfox85/devx/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage devx configuration",
	Long:  `View and modify devx configuration settings.`,
}

var configViewCmd = &cobra.Command{
	Use:   "view",
	Short: "View current configuration",
	Long:  `Display the current configuration including values from config file and environment variables.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		// Convert to YAML for pretty printing
		yamlData, err := yaml.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("failed to marshal config: %w", err)
		}

		fmt.Print(string(yamlData))
		return nil
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get a configuration value",
	Long:  `Get the value of a specific configuration key.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]
		value := config.GetConfigValue(key)

		if value == nil {
			return fmt.Errorf("key '%s' not found", key)
		}

		fmt.Println(value)
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value",
	Long:  `Set the value of a specific configuration key and save to config file.`,
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]
		value := args[1]

		// Validate known keys
		validKeys := map[string]bool{
			"basedomain":     true,
			"caddy_api":      true,
			"tmuxp_template": true,
			"ports":          true,
			"disable_caddy":  true,
			"editor":         true,
		}

		if !validKeys[key] {
			return fmt.Errorf("unknown configuration key: %s", key)
		}

		// Ensure config file exists
		if viper.ConfigFileUsed() == "" {
			// Force creation of config file
			cfg, err := config.LoadConfig()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			if err := config.SaveConfig(cfg); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}
		}

		// Handle special cases for different value types
		var configValue interface{} = value

		if key == "ports" {
			// Parse JSON array for ports
			if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
				var ports []string
				if err := json.Unmarshal([]byte(value), &ports); err != nil {
					return fmt.Errorf("invalid JSON array for ports: %w", err)
				}
				configValue = ports
			} else {
				// Split comma-separated values
				ports := strings.Split(value, ",")
				for i, port := range ports {
					ports[i] = strings.TrimSpace(port)
				}
				configValue = ports
			}
		} else if key == "disable_caddy" {
			// Convert string to boolean
			configValue = value == "true" || value == "True" || value == "1"
		}

		if err := config.SetConfigValue(key, configValue); err != nil {
			return fmt.Errorf("failed to set config value: %w", err)
		}

		fmt.Printf("Set %s = %v\n", key, configValue)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configViewCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
}
