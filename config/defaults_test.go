package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GETH_RPC_URL", "http://127.0.0.1:8545")
	t.Setenv("GETH_WS_URL", "ws://127.0.0.1:8545")
	t.Setenv("SCHEMA_AUTH_MODE", "none")
}

func TestLoad_BuiltInDefault(t *testing.T) {
	setRequiredEnv(t)
	dir := t.TempDir()
	t.Setenv("SHINZO_DATA_DIR", dir)
	t.Setenv("SHINZO_KEY_PASSPHRASE", "x")

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "<built-in default>", cfg.Source)
	assert.Equal(t, dir, cfg.DefraDB.Store.Path)
}

func TestLoad_DataDirDefaultsToHome(t *testing.T) {
	setRequiredEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("SHINZO_DATA_DIR", "")
	t.Setenv("DEFRADB_STORE_PATH", "")
	t.Setenv("SHINZO_KEY_PASSPHRASE", "x")

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".shinzo", "generator"), cfg.DefraDB.Store.Path)
}

func TestLoad_PassphraseGeneratedThenReused(t *testing.T) {
	setRequiredEnv(t)
	dir := t.TempDir()
	t.Setenv("SHINZO_DATA_DIR", dir)
	t.Setenv("SHINZO_KEY_PASSPHRASE", "")
	t.Setenv("DEFRADB_KEYRING_SECRET", "")
	t.Setenv("DEFRA_KEYRING_SECRET", "")

	first, err := Load("")
	require.NoError(t, err)
	assert.True(t, first.PassphraseGenerated)
	assert.Len(t, first.DefraDB.KeyringSecret, 64)
	info, err := os.Stat(filepath.Join(dir, PassphraseFileName))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	second, err := Load("")
	require.NoError(t, err)
	assert.False(t, second.PassphraseGenerated)
	assert.Equal(t, first.DefraDB.KeyringSecret, second.DefraDB.KeyringSecret)
}
