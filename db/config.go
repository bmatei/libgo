package db

type Config struct {
	URL string `json:"url" toml:"url" yaml:"url" env:"DATABASE_URL"`
}
