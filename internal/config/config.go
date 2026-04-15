package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type ValidatorConfig struct {
	Name    string `yaml:"name"`
	Address string `yaml:"address"`
}

type Config struct {
	Network    string            `yaml:"network"`
	Host       string            `yaml:"turboflakes_api_host"`
	RPCURL     string            `yaml:"rpc_url"` // NEW: RPC endpoint
	Validators []ValidatorConfig `yaml:"validators"`
	// Performance Settings
	ScrapeInterval   time.Duration `yaml:"scrape_interval"`   // e.g., 60s
	ConcurrencyLimit int           `yaml:"concurrency_limit"` // e.g., 5
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	// Set defaults
	cfg.ScrapeInterval = 60 * time.Second
	cfg.ConcurrencyLimit = 5

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
