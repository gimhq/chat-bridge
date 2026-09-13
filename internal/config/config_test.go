package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var envKeys = []string{"CHATBRIDGE_CONFIG", "CHATBRIDGE_SERVER_TOKEN", "CHATBRIDGE_SERVER_TOKEN_FILE", "CHATBRIDGE_SERVER_ADDR",
	"CHATBRIDGE_LOG_LEVEL", "CHATBRIDGE_STORAGE_DATA_DIR", "CHATBRIDGE_MEDIA_AUTO_DOWNLOAD", "CHATBRIDGE_MEDIA_AUTO_DOWNLOAD_MAX_MB",
	"CHATBRIDGE_MEDIA_MAX_UPLOAD_MB", "CHATBRIDGE_EVENTS_RETENTION_DAYS"}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range envKeys {
		t.Setenv(k, "")
	}
	t.Chdir(t.TempDir()) // no stray ./chat-bridge.yaml
}

func TestLoadEnvOnly(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
		check   func(Config) bool
	}{
		{
			name: "defaults with token",
			env:  map[string]string{"CHATBRIDGE_SERVER_TOKEN": "0123456789abcdef"},
			check: func(c Config) bool {
				return c.Server.Addr == ":8080" && filepath.IsAbs(c.Storage.DataDir) && strings.HasSuffix(c.Storage.DataDir, "data") &&
					c.Log.Level == "info" && c.Media.MaxUploadMB == 64 && len(c.Media.AutoDownload) == 3 &&
					c.Media.AutoDownloadMaxMB == 16 && c.Events.RetentionDays == 7 && c.Source == ""
			},
		},
		{
			name: "overrides",
			env: map[string]string{
				"CHATBRIDGE_SERVER_TOKEN": "0123456789abcdef", "CHATBRIDGE_SERVER_ADDR": ":9000", "CHATBRIDGE_LOG_LEVEL": "debug",
				"CHATBRIDGE_MEDIA_MAX_UPLOAD_MB": "8", "CHATBRIDGE_MEDIA_AUTO_DOWNLOAD": " image , file", "CHATBRIDGE_STORAGE_DATA_DIR": "/var/lib/cb",
			},
			check: func(c Config) bool {
				return c.Server.Addr == ":9000" && c.Log.Level == "debug" && c.Media.MaxUploadMB == 8 &&
					len(c.Media.AutoDownload) == 2 && c.Media.AutoDownload[1] == "file" && c.Storage.DataDir == "/var/lib/cb"
			},
		},
		{name: "missing token", env: map[string]string{}, wantErr: "server.token"},
		{name: "short token", env: map[string]string{"CHATBRIDGE_SERVER_TOKEN": "short"}, wantErr: "server.token"},
		{name: "bad log level", env: map[string]string{"CHATBRIDGE_SERVER_TOKEN": "0123456789abcdef", "CHATBRIDGE_LOG_LEVEL": "loud"}, wantErr: "invalid config"},
		{name: "bad retention", env: map[string]string{"CHATBRIDGE_SERVER_TOKEN": "0123456789abcdef", "CHATBRIDGE_EVENTS_RETENTION_DAYS": "0"}, wantErr: "invalid config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			cfg, err := Load("")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !tt.check(cfg) {
				t.Fatalf("unexpected config %+v", cfg)
			}
		})
	}
}

func TestLoadFileAndPrecedence(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("file-token-0123456789\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cb.yaml")
	if err := os.WriteFile(path, []byte(`
server:
  addr: ":9001"
  token_file: `+tokenFile+`
storage:
  data_dir: `+filepath.Join(dir, "state")+`
media:
  auto_download: [image]
  max_upload_mb: 32
log:
  level: warn
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Token != "file-token-0123456789" || cfg.Server.Addr != ":9001" || cfg.Log.Level != "warn" ||
		cfg.Media.MaxUploadMB != 32 || len(cfg.Media.AutoDownload) != 1 || cfg.Source != path {
		t.Fatalf("file config: %+v", cfg)
	}
	if cfg.Events.RetentionDays != 7 {
		t.Fatalf("default not kept under file: %d", cfg.Events.RetentionDays)
	}
	// Environment overrides the file.
	t.Setenv("CHATBRIDGE_LOG_LEVEL", "error")
	t.Setenv("CHATBRIDGE_MEDIA_AUTO_DOWNLOAD", "image,video")
	cfg, err = Load(path)
	if err != nil || cfg.Log.Level != "error" || len(cfg.Media.AutoDownload) != 2 {
		t.Fatalf("env precedence: %+v %v", cfg, err)
	}
	// CHATBRIDGE_CONFIG selects the file when no path is given; a missing explicit file is an error.
	t.Setenv("CHATBRIDGE_CONFIG", path)
	if cfg, err = Load(""); err != nil || cfg.Source != path {
		t.Fatalf("env config path: %v %s", err, cfg.Source)
	}
	if _, err := Load(filepath.Join(dir, "nope.yaml")); err == nil {
		t.Fatal("missing explicit file accepted")
	}
	// A default ./chat-bridge.yaml is picked up from the working directory.
	t.Setenv("CHATBRIDGE_CONFIG", "")
	wd := t.TempDir()
	t.Chdir(wd)
	if err := os.WriteFile(filepath.Join(wd, defaultFile), []byte("server:\n  token: cwd-token-0123456789\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load("")
	if err != nil || cfg.Server.Token != "cwd-token-0123456789" || cfg.Storage.DataDir != filepath.Join(wd, "data") {
		t.Fatalf("cwd default file: %+v %v", cfg, err)
	}
	if err := os.WriteFile(filepath.Join(wd, defaultFile), []byte("server: [broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(""); err == nil {
		t.Fatal("broken yaml accepted")
	}
}
