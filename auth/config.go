package auth

type BasicAuthConfig struct {
	Username string        `toml:"username" yaml:"username" env:"BASIC_AUTH_USERNAME"`
	Password string        `toml:"password" yaml:"password" env:"BASIC_AUTH_PASSWORD"`
}
