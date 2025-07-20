package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

type Config struct {
	BaseDomain    string   `mapstructure:"basedomain"`
	CaddyAPI      string   `mapstructure:"caddy_api"`
	TmuxpTemplate string   `mapstructure:"tmuxp_template"`
	Ports         []string `mapstructure:"ports"`
}

func LoadConfig() (*Config, error) {
	var cfg Config
	err := viper.Unmarshal(&cfg)
	if err != nil {
		return nil, fmt.Errorf("unable to decode config: %w", err)
	}
	
	// Expand ~ in tmuxp_template path
	if cfg.TmuxpTemplate != "" && cfg.TmuxpTemplate[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		cfg.TmuxpTemplate = filepath.Join(home, cfg.TmuxpTemplate[1:])
	}
	
	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
	// Ensure config directory exists
	configPath := filepath.Join(os.Getenv("HOME"), ".config", "devx")
	if err := os.MkdirAll(configPath, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	
	// Set the values in viper
	viper.Set("basedomain", cfg.BaseDomain)
	viper.Set("caddy_api", cfg.CaddyAPI)
	viper.Set("tmuxp_template", cfg.TmuxpTemplate)
	
	// Write the config file
	configFile := filepath.Join(configPath, "config.yaml")
	return viper.WriteConfigAs(configFile)
}

func GetConfigValue(key string) interface{} {
	return viper.Get(key)
}

func SetConfigValue(key string, value interface{}) error {
	viper.Set(key, value)
	return viper.WriteConfig()
}