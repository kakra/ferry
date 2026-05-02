package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_EnvOverrides(t *testing.T) {
	// Set some environment variables
	os.Setenv("FERRY_SECURITY_SESSION_SECRET", "test-session-secret")
	os.Setenv("FERRY_SECURITY_TOKEN_SECRET", "test-token-secret")
	os.Setenv("FERRY_AUTH_STATIC_PASSWORD", "test-static-secret")
	os.Setenv("FERRY_SERVER_PUBLIC_URL", "https://env.example.com")
	os.Setenv("FERRY_SECURITY_BEHIND_REVERSE_PROXY", "false")
	os.Setenv("FERRY_SERVER_TRUSTED_PROXIES", "10.0.0.1, 10.0.0.2")
	os.Setenv("FERRY_AUTH_RATE_LIMIT_RATE", "0.5")
	os.Setenv("FERRY_AUTH_RATE_LIMIT_BURST", "10")

	defer func() {
		os.Unsetenv("FERRY_SECURITY_SESSION_SECRET")
		os.Unsetenv("FERRY_SECURITY_TOKEN_SECRET")
		os.Unsetenv("FERRY_AUTH_STATIC_PASSWORD")
		os.Unsetenv("FERRY_SERVER_PUBLIC_URL")
		os.Unsetenv("FERRY_SECURITY_BEHIND_REVERSE_PROXY")
		os.Unsetenv("FERRY_SERVER_TRUSTED_PROXIES")
		os.Unsetenv("FERRY_AUTH_RATE_LIMIT_RATE")
		os.Unsetenv("FERRY_AUTH_RATE_LIMIT_BURST")
	}()

	// Load config (even with non-existent file)
	cfg, err := Load("non-existent.yaml")
	assert.NoError(t, err)

	assert.Equal(t, "https://env.example.com", cfg.Server.PublicURL)
	assert.False(t, cfg.Security.BehindReverseProxy)
	assert.Equal(t, []string{"10.0.0.1", "10.0.0.2"}, cfg.Server.TrustedProxies)
	assert.Equal(t, 0.5, cfg.Auth.RateLimit.Rate)
	assert.Equal(t, 10, cfg.Auth.RateLimit.Burst)
}

func TestLoad_RejectsDefaultSecretsOutsideDevMode(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "security.session_secret")
}

func TestLoad_AllowsDefaultSecretsInDevMode(t *testing.T) {
	t.Setenv("FERRY_DEV_MODE", "true")

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	require.NoError(t, err)
	assert.True(t, cfg.DevMode)
	assert.Equal(t, defaultSessionSecret, cfg.Security.SessionSecret)
}

func TestLoad_RejectsEnabledLDAPUntilImplemented(t *testing.T) {
	t.Setenv("FERRY_DEV_MODE", "true")
	t.Setenv("FERRY_LDAP_ENABLED", "true")

	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ldap.enabled is reserved")
}

func TestInitConfig_CreatesConfigWithGeneratedSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	result, err := InitConfig(path)
	require.NoError(t, err)
	assert.True(t, result.Created)
	assert.True(t, result.Updated)

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.False(t, isPlaceholderSecret(cfg.Security.SessionSecret, defaultSessionSecret))
	assert.False(t, isPlaceholderSecret(cfg.Security.TokenSecret, defaultTokenSecret))
	assert.False(t, isPlaceholderSecret(cfg.Auth.StaticPassword, defaultStaticPassword))
	assert.NotEmpty(t, cfg.Auth.BootstrapPassword)
}

func TestInitConfig_RepairsPlaceholderSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte(`security:
  session_secret: "change-me-session"
  token_secret: "generate-another-long-random-string-here"
auth:
  static_password: "CHANGEME_TO_SOMETHING_VERY_STRONG"
  bootstrap_password: "CHANGEME_TO_A_DIFFERENT_STRONG_SECRET"
`)
	require.NoError(t, os.WriteFile(path, content, 0o600))

	result, err := InitConfig(path)
	require.NoError(t, err)
	assert.False(t, result.Created)
	assert.True(t, result.Updated)

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.False(t, isPlaceholderSecret(cfg.Security.SessionSecret, defaultSessionSecret))
	assert.False(t, isPlaceholderSecret(cfg.Security.TokenSecret, defaultTokenSecret))
	assert.False(t, isPlaceholderSecret(cfg.Auth.StaticPassword, defaultStaticPassword))
	assert.False(t, isPlaceholderSecret(cfg.Auth.BootstrapPassword, ""))
}
