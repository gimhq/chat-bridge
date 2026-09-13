// Package config loads settings from defaults, an optional YAML file, and CHATBRIDGE_* environment
// variables, in that order of precedence (later wins).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// Config holds every runtime setting. Keys mirror the YAML layout; the environment variable for
// `section.key` is CHATBRIDGE_<SECTION>_<KEY> (e.g. CHATBRIDGE_MEDIA_AUTO_DOWNLOAD_MAX_MB).
type Config struct {
	Server struct {
		Addr      string `koanf:"addr" validate:"required"`
		Token     string `koanf:"token"`
		TokenFile string `koanf:"token_file"`
		// AdapterToken enables /adapter/v1 for out-of-process adapters when set.
		AdapterToken string `koanf:"adapter_token"`
		// PublicURL is the base URL adapters use to reach the media endpoints; derived from the
		// request when empty.
		PublicURL string `koanf:"public_url" validate:"omitempty,url"`
	} `koanf:"server"`
	Storage struct {
		DataDir string `koanf:"data_dir" validate:"required"`
	} `koanf:"storage"`
	Media struct {
		AutoDownload      []string `koanf:"auto_download"`
		AutoDownloadMaxMB int64    `koanf:"auto_download_max_mb" validate:"min=0,max=2048"`
		MaxUploadMB       int64    `koanf:"max_upload_mb" validate:"min=1,max=2048"`
	} `koanf:"media"`
	Events struct {
		RetentionDays int `koanf:"retention_days" validate:"min=1,max=365"`
	} `koanf:"events"`
	Log struct {
		Level string `koanf:"level" validate:"oneof=debug info warn error"`
	} `koanf:"log"`
	// Adapters holds platform-wide settings that every account of a platform inherits.
	Adapters struct {
		Telegram struct {
			APIID   int    `koanf:"api_id"`
			APIHash string `koanf:"api_hash"`
		} `koanf:"telegram"`
	} `koanf:"adapters"`

	// Source is the absolute path of the file that was loaded ("" when none).
	Source string `koanf:"-"`
}

const (
	envPrefix   = "CHATBRIDGE_"
	envConfig   = "CHATBRIDGE_CONFIG"
	defaultFile = "chat-bridge.yaml"
)

var defaults = map[string]any{
	"server.addr":                ":8080",
	"storage.data_dir":           "./data",
	"media.auto_download":        []string{"image", "voice", "sticker"},
	"media.auto_download_max_mb": 16,
	"media.max_upload_mb":        64,
	"events.retention_days":      7,
	"log.level":                  "info",
}

// Load resolves the config file (explicit path → $CHATBRIDGE_CONFIG → ./chat-bridge.yaml if it
// exists), overlays the environment, validates, and returns the result.
func Load(path string) (Config, error) {
	k := koanf.New(".")
	if err := k.Load(confmap.Provider(defaults, "."), nil); err != nil {
		return Config{}, fmt.Errorf("load defaults: %w", err)
	}

	source, err := resolveFile(path)
	if err != nil {
		return Config{}, err
	}
	if source != "" {
		if err := k.Load(file.Provider(source), yaml.Parser()); err != nil {
			return Config{}, fmt.Errorf("load %s: %w", source, err)
		}
	}

	envProvider := env.Provider(".", env.Opt{
		Prefix: envPrefix,
		TransformFunc: func(key, value string) (string, any) {
			if value == "" || key == envConfig {
				return "", nil // empty variables are treated as unset
			}
			mapped := envKey(strings.TrimPrefix(key, envPrefix))
			if mapped == "media.auto_download" {
				return mapped, splitList(value)
			}
			return mapped, value
		},
	})
	if err := k.Load(envProvider, nil); err != nil {
		return Config{}, fmt.Errorf("load env: %w", err)
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}
	cfg.Source = source
	if err := finish(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// envKey maps SERVER_TOKEN_FILE → server.token_file: the first token is the section, the rest
// stays underscored. Under `adapters` the second token is the platform:
// ADAPTERS_TELEGRAM_API_ID → adapters.telegram.api_id.
func envKey(s string) string {
	s = strings.ToLower(s)
	section, rest, ok := strings.Cut(s, "_")
	if !ok {
		return s
	}
	if section == "adapters" {
		if platform, key, ok := strings.Cut(rest, "_"); ok {
			return section + "." + platform + "." + key
		}
	}
	return section + "." + rest
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func resolveFile(path string) (string, error) {
	explicit := path != ""
	if path == "" {
		path = os.Getenv(envConfig)
		explicit = path != ""
	}
	if path == "" {
		path = defaultFile
	}
	if _, err := os.Stat(path); err != nil {
		if explicit || !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("config file %s: %w", path, err)
		}
		return "", nil
	}
	return filepath.Abs(path)
}

// finish applies token_file, makes data_dir absolute, and validates.
func finish(cfg *Config) error {
	if cfg.Server.TokenFile != "" {
		b, err := os.ReadFile(cfg.Server.TokenFile)
		if err != nil {
			return fmt.Errorf("server.token_file: %w", err)
		}
		cfg.Server.Token = strings.TrimSpace(string(b))
	}
	if len(cfg.Server.Token) < 16 {
		return errors.New("invalid config: server.token (or server.token_file) must provide at least 16 characters")
	}
	if cfg.Server.AdapterToken != "" && len(cfg.Server.AdapterToken) < 16 {
		return errors.New("invalid config: server.adapter_token must be at least 16 characters")
	}
	if cfg.Server.AdapterToken == cfg.Server.Token {
		return errors.New("invalid config: server.adapter_token must differ from server.token")
	}
	abs, err := filepath.Abs(cfg.Storage.DataDir)
	if err != nil {
		return fmt.Errorf("storage.data_dir: %w", err)
	}
	cfg.Storage.DataDir = abs
	if err := validator.New().Struct(cfg); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	return nil
}
