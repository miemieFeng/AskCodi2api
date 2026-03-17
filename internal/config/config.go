package config

import "os"

type Config struct {
	DatabasePath string
	ListenAddr   string
}

func Load() *Config {
	cfg := &Config{
		DatabasePath: "./data.db",
		ListenAddr:   ":8000",
	}
	if v := os.Getenv("DATABASE_PATH"); v != "" {
		cfg.DatabasePath = v
	}
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	return cfg
}
