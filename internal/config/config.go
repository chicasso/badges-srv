package config

import (
	"encoding/hex"
	"fmt"

	"github.com/kelseyhightower/envconfig"
)

type EnvType string

var (
	Production  EnvType = "production"
	Development EnvType = "development"
)

type Config struct {
	Port        int    `envconfig:"PORT" default:"8080"`
	FrontendURL string `envconfig:"FRONTEND_URL" default:"http://localhost:3000"`

	DatabaseURL string `envconfig:"DATABASE_URL" required:"true"`

	GithubClientID     string `envconfig:"GITHUB_CLIENT_ID" required:"true"`
	GithubRedirectURL  string `envconfig:"GITHUB_REDIRECT_URL" required:"true"`
	GithubClientSecret string `envconfig:"GITHUB_CLIENT_SECRET" required:"true"`

	JWTSecret string `envconfig:"JWT_SECRET" required:"true"`

	EncryptionKey string `envconfig:"ENCRYPTION_KEY" required:"true"`

	SMTPHost  string `envconfig:"SMTP_HOST" required:"true"`
	SMTPPort  int    `envconfig:"SMTP_PORT" default:"587"`
	SMTPUser  string `envconfig:"SMTP_USER" required:"true"`
	SMTPPass  string `envconfig:"SMTP_PASS" required:"true"`
	EmailFrom string `envconfig:"EMAIL_FROM" required:"true"`

	AdminKey string `envconfig:"ADMIN_KEY" default:""`

	Env EnvType `envconfig:"ENVIRONMENT" default:""`

	BootStrapServers string `envconfig:"BOOTSTRAP_SERVERS" default:"localhost:9092"`
}

func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	return &cfg, nil
}

/**
 * EncryptionKeyBytes decodes the hex encryption key into a 32-byte slice.
 */
func (c *Config) EncryptionKeyBytes() []byte {
	b, _ := hex.DecodeString(c.EncryptionKey)
	return b
}
