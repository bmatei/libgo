package server

import (
	"github.com/ilyakaznacheev/cleanenv"
	"github.com/rs/zerolog/log"
)

type Config struct {
	Hostname      string `toml:"hostname" yaml:"hostname" env:"SERVICE_HOSTNAME" env-default:""`
	ListenAddress string `toml:"listen_address" yaml:"listen_address" env:"SERVICE_LISTEN_ADDRESS" env-default:"localhost"`
	Port          int    `toml:"port" yaml:"port" env:"SERVICE_PORT" env-default:""`
}

func NewConfig(path string) *Config {
	var cfg Config
	err := cleanenv.ReadConfig(path, &cfg)
	if err != nil {
		log.Error().Err(err).Str("path", path).Msg("Failed to read config")

		return nil
	}

	return &cfg
}
