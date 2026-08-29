package config

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// defaultConfigYAML is config/config.yaml compiled into the binary, so a node
// runs with no config file at all. A file found by cmd (or -config) replaces
// it wholesale; environment variables layer on top of either.
//
//go:embed config.yaml
var defaultConfigYAML []byte

// PassphraseFileName is where an auto-generated passphrase lives, inside the data dir.
const PassphraseFileName = "passphrase"

// Load reads the config at path, or the compiled-in default when path is "".
func Load(path string) (*Config, error) {
	_ = godotenv.Load() // .env in the working directory, if any
	if path == "" {
		return loadBytes(defaultConfigYAML, "<built-in default>")
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	return loadBytes(data, path)
}

// DefaultDataDir is where node state goes when neither SHINZO_DATA_DIR nor the
// config says otherwise: $XDG_DATA_HOME/shinzo/generator, else ~/.shinzo/generator.
func DefaultDataDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "shinzo", "generator")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "data" // last resort: cwd-relative, as before
	}
	return filepath.Join(home, ".shinzo", "generator")
}

// resolveDataDir pins node state under one directory. Precedence:
// SHINZO_DATA_DIR, DEFRADB_STORE_PATH (legacy), the config's store path, DefaultDataDir().
func resolveDataDir(cfg *Config) {
	dir := strings.TrimSpace(os.Getenv("SHINZO_DATA_DIR"))
	if dir == "" {
		dir = strings.TrimSpace(cfg.DefraDB.Store.Path) // DEFRADB_STORE_PATH already applied here
	}
	if dir == "" {
		dir = DefaultDataDir()
	}
	cfg.DefraDB.Store.Path = dir
}

// ensureKeyringSecret: explicit config/env wins; otherwise the node reads
// <data>/passphrase, creating it (0600) on first run. Losing that file means
// losing the node's identity, which the startup banner says out loud.
func ensureKeyringSecret(cfg *Config) error {
	if cfg.DefraDB.KeyringSecret != "" {
		return nil
	}
	path := filepath.Join(cfg.DefraDB.Store.Path, PassphraseFileName)
	if b, err := os.ReadFile(filepath.Clean(path)); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			cfg.DefraDB.KeyringSecret = s
			cfg.PassphraseFile = path
			return nil
		}
	}
	raw := make([]byte, 32) //nolint:mnd // 256-bit passphrase
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("generate passphrase: %w", err)
	}
	secret := hex.EncodeToString(raw)
	if err := os.MkdirAll(cfg.DefraDB.Store.Path, 0o700); err != nil { //nolint:mnd
		return fmt.Errorf("create data dir %s: %w", cfg.DefraDB.Store.Path, err)
	}
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil { //nolint:mnd
		return fmt.Errorf("write passphrase file %s: %w", path, err)
	}
	cfg.DefraDB.KeyringSecret = secret
	cfg.PassphraseFile = path
	cfg.PassphraseGenerated = true
	return nil
}

func loadBytes(data []byte, source string) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config %s: %w", source, err)
	}
	cfg.Source = source
	if err := applyEnvOverrides(&cfg); err != nil {
		return nil, err
	}
	resolveDataDir(&cfg)
	if err := ensureKeyringSecret(&cfg); err != nil {
		return nil, err
	}
	applyDefaults(&cfg)
	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	return &cfg, nil
}
