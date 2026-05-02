package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the root application configuration loaded from YAML and environment variables.
type Config struct {
	DevMode  bool           `yaml:"dev_mode"`
	Server   ServerConfig   `yaml:"server"`
	Security SecurityConfig `yaml:"security"`
	Storage  StorageConfig  `yaml:"storage"`
	Cleanup  CleanupConfig  `yaml:"cleanup"`
	Limits   LimitsConfig   `yaml:"limits"`
	Database DatabaseConfig `yaml:"database"`
	Auth     AuthConfig     `yaml:"auth"`
	Upload   UploadConfig   `yaml:"upload"`
	Share    ShareConfig    `yaml:"share"`
	LDAP     LDAPConfig     `yaml:"ldap"`
	UI       UIConfig       `yaml:"ui"`
}

// ServerConfig controls the HTTP listener, public URL generation, and trusted proxies.
type ServerConfig struct {
	Listen         string   `yaml:"listen"`
	PublicURL      string   `yaml:"public_url"`
	TrustedProxies []string `yaml:"trusted_proxies"`
}

// SecurityConfig contains secrets and deployment security switches.
type SecurityConfig struct {
	SessionSecret      string `yaml:"session_secret"`
	TokenSecret        string `yaml:"token_secret"`
	BehindReverseProxy bool   `yaml:"behind_reverse_proxy"`
}

// StorageConfig selects the local content-addressable storage path.
type StorageConfig struct {
	Path string `yaml:"path"`
}

// CleanupConfig controls background cleanup intervals and incomplete upload retention.
type CleanupConfig struct {
	Interval                     string `yaml:"interval"`
	DeleteIncompleteUploadsAfter string `yaml:"delete_incomplete_uploads_after"`
}

// LimitsConfig defines share and upload size limits.
type LimitsConfig struct {
	MaxFilesPerShare string `yaml:"max_files_per_share"`
	MaxShareSize     string `yaml:"max_share_size"`
	MaxFileSize      string `yaml:"max_file_size"`
}

// DatabaseConfig selects the SQLite database path.
type DatabaseConfig struct {
	Path string `yaml:"path"`
}

// AuthConfig contains bootstrap credentials and login rate limiting.
type AuthConfig struct {
	StaticPassword    string          `yaml:"static_password"`
	BootstrapPassword string          `yaml:"bootstrap_password"`
	RateLimit         RateLimitConfig `yaml:"rate_limit"`
}

// RateLimitConfig controls per-client authentication throttling.
type RateLimitConfig struct {
	Enabled bool    `yaml:"enabled"`
	Rate    float64 `yaml:"rate"`  // requests per second
	Burst   int     `yaml:"burst"` // max burst size
}

// UploadConfig controls resumable upload limits and temporary upload retention.
type UploadConfig struct {
	MaxSize    string `yaml:"max_size"`
	Expiration string `yaml:"expiration"`
}

// ShareConfig defines default and maximum public share lifetimes.
type ShareConfig struct {
	DefaultExpiration string `yaml:"default_expiration"`
	MaxExpiration     string `yaml:"max_expiration"`
}

// LDAPConfig reserves the planned LDAP realm settings.
//
// ldap.enabled=true is rejected during validation until authentication support
// is implemented, preventing a configured-but-inactive directory from becoming
// a silent no-op.
type LDAPConfig struct {
	Enabled      bool                 `yaml:"enabled"`
	URL          string               `yaml:"url"`
	Realm        string               `yaml:"realm"`
	StartTLS     bool                 `yaml:"starttls"`
	Insecure     bool                 `yaml:"insecure_skip_verify"`
	BindDN       string               `yaml:"bind_dn"`
	BindPassword string               `yaml:"bind_password"`
	BaseDN       string               `yaml:"base_dn"`
	UserFilter   string               `yaml:"user_filter"`
	Attributes   LDAPAttributesConfig `yaml:"attributes"`
	TLS          LDAPTLSConfig        `yaml:"tls"`
}

// LDAPAttributesConfig maps LDAP attributes to local user fields.
type LDAPAttributesConfig struct {
	Login       string `yaml:"login"`
	DisplayName string `yaml:"display_name"`
	Email       string `yaml:"email"`
	ExternalID  string `yaml:"external_id"`
}

// LDAPTLSConfig controls LDAP certificate trust material.
type LDAPTLSConfig struct {
	CAFile string `yaml:"ca_file"`
}

// UIConfig contains optional branding customizations for rendered pages.
type UIConfig struct {
	LogoURL      string `yaml:"logo_url"`
	PrimaryColor string `yaml:"primary_color"`
	CustomCSS    string `yaml:"custom_css_path"`
}

const (
	defaultSessionSecret  = "change-me-session"
	defaultTokenSecret    = "change-me-token"
	defaultStaticPassword = "changeme"
)

// DefaultConfig returns development-oriented defaults.
func DefaultConfig() *Config {
	return &Config{
		DevMode: false,
		Server: ServerConfig{
			Listen:         "127.0.0.1:8080",
			PublicURL:      "http://localhost:8080",
			TrustedProxies: []string{"127.0.0.1"},
		},
		Security: SecurityConfig{
			SessionSecret:      defaultSessionSecret,
			TokenSecret:        defaultTokenSecret,
			BehindReverseProxy: true,
		},
		Storage: StorageConfig{
			Path: "data/storage",
		},
		Cleanup: CleanupConfig{
			Interval:                     "15m",
			DeleteIncompleteUploadsAfter: "24h",
		},
		Limits: LimitsConfig{
			MaxFilesPerShare: "50",
			MaxShareSize:     "20GiB",
			MaxFileSize:      "20GiB",
		},
		Database: DatabaseConfig{
			Path: "data/db/ferry.db",
		},
		Auth: AuthConfig{
			StaticPassword:    defaultStaticPassword,
			BootstrapPassword: "",
			RateLimit: RateLimitConfig{
				Enabled: true,
				Rate:    0.2,
				Burst:   5,
			},
		},
		LDAP: LDAPConfig{
			Enabled:      false,
			URL:          "",
			Realm:        "",
			StartTLS:     false,
			Insecure:     false,
			BindDN:       "",
			BindPassword: "",
			BaseDN:       "",
			UserFilter:   "",
			Attributes: LDAPAttributesConfig{
				Login:       "sAMAccountName",
				DisplayName: "displayName",
				Email:       "mail",
				ExternalID:  "objectGUID",
			},
			TLS: LDAPTLSConfig{
				CAFile: "",
			},
		},
		Upload: UploadConfig{
			MaxSize:    "20GiB",
			Expiration: "24h",
		},
		Share: ShareConfig{
			DefaultExpiration: "168h", // 7 days
			MaxExpiration:     "720h", // 30 days
		},
	}
}

// InitResult describes whether InitConfig created or updated a config file.
type InitResult struct {
	Created bool
	Updated bool
}

// InitConfig creates or repairs a YAML config file with generated secrets.
func InitConfig(path string) (InitResult, error) {
	result := InitResult{}
	cfg := DefaultConfig()

	f, err := os.Open(path)
	if err == nil {
		decoder := yaml.NewDecoder(f)
		if decodeErr := decoder.Decode(cfg); decodeErr != nil {
			_ = f.Close()
			return result, fmt.Errorf("failed to decode config: %w", decodeErr)
		}
		if closeErr := f.Close(); closeErr != nil {
			return result, closeErr
		}
	} else if os.IsNotExist(err) {
		result.Created = true
	} else {
		return result, err
	}

	changed, err := ensureGeneratedSecrets(cfg)
	if err != nil {
		return result, err
	}
	result.Updated = changed || result.Created
	if !result.Updated {
		return result, nil
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return result, fmt.Errorf("failed to encode config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return result, err
	}
	return result, nil
}

func ensureGeneratedSecrets(cfg *Config) (bool, error) {
	changed := false
	setSecret := func(current *string, values ...string) error {
		if !isPlaceholderSecret(*current, values...) {
			return nil
		}
		secret, err := generateSecret(32)
		if err != nil {
			return err
		}
		*current = secret
		changed = true
		return nil
	}

	if err := setSecret(&cfg.Security.SessionSecret, defaultSessionSecret, "generate-a-very-long-random-string-here"); err != nil {
		return changed, err
	}
	if err := setSecret(&cfg.Security.TokenSecret, defaultTokenSecret, "generate-another-long-random-string-here"); err != nil {
		return changed, err
	}
	if err := setSecret(&cfg.Auth.StaticPassword, defaultStaticPassword, "CHANGEME_TO_SOMETHING_VERY_STRONG"); err != nil {
		return changed, err
	}
	if err := setSecret(&cfg.Auth.BootstrapPassword, "", "CHANGEME_TO_A_DIFFERENT_STRONG_SECRET"); err != nil {
		return changed, err
	}

	return changed, nil
}

func generateSecret(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func isPlaceholderSecret(value string, placeholders ...string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		for _, placeholder := range placeholders {
			if placeholder == "" {
				return true
			}
		}
		return false
	}
	upper := strings.ToUpper(trimmed)
	if strings.Contains(upper, "CHANGEME") || strings.Contains(upper, "CHANGE-ME") || strings.Contains(upper, "GENERATE-") {
		return true
	}
	for _, placeholder := range placeholders {
		if trimmed == placeholder {
			return true
		}
	}
	return false
}

// Load reads configuration from path, applies FERRY_* environment overrides, and validates it.
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	f, err := os.Open(path)
	if err == nil {
		defer f.Close()
		decoder := yaml.NewDecoder(f)
		if err := decoder.Decode(cfg); err != nil {
			return nil, fmt.Errorf("failed to decode config: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// Always apply environment overrides
	applyEnvOverrides(reflect.ValueOf(cfg).Elem(), "FERRY")

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyEnvOverrides(v reflect.Value, prefix string) {
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		// Get the yaml tag for the environment variable name
		name := fieldType.Tag.Get("yaml")
		if name == "" {
			name = strings.ToLower(fieldType.Name)
		}

		// Handle comma-separated YAML tags (e.g. name,omitempty)
		tagParts := strings.Split(name, ",")
		envKey := prefix + "_" + strings.ToUpper(tagParts[0])

		if field.Kind() == reflect.Struct {
			applyEnvOverrides(field, envKey)
			continue
		}

		envVal, exists := os.LookupEnv(envKey)
		if !exists {
			continue
		}

		if !field.CanSet() {
			continue
		}

		switch field.Kind() {
		case reflect.String:
			field.SetString(envVal)
		case reflect.Bool:
			b, err := strconv.ParseBool(envVal)
			if err == nil {
				field.SetBool(b)
			}
		case reflect.Float64:
			f, err := strconv.ParseFloat(envVal, 64)
			if err == nil {
				field.SetFloat(f)
			}
		case reflect.Int:
			n, err := strconv.Atoi(envVal)
			if err == nil {
				field.SetInt(int64(n))
			}
		case reflect.Slice:
			if field.Type().Elem().Kind() == reflect.String {
				parts := strings.Split(envVal, ",")
				for i := range parts {
					parts[i] = strings.TrimSpace(parts[i])
				}
				field.Set(reflect.ValueOf(parts))
			}
		}
	}
}

// Validate rejects unsafe defaults and unsupported or incompatible settings.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}

	if !c.DevMode {
		if isPlaceholderSecret(c.Security.SessionSecret, defaultSessionSecret, "generate-a-very-long-random-string-here") {
			return fmt.Errorf("security.session_secret must be set to a unique secret; run 'ferry init-config' or enable dev_mode explicitly")
		}
		if isPlaceholderSecret(c.Security.TokenSecret, defaultTokenSecret, "generate-another-long-random-string-here") {
			return fmt.Errorf("security.token_secret must be set to a unique secret; run 'ferry init-config' or enable dev_mode explicitly")
		}
		if isPlaceholderSecret(c.Auth.StaticPassword, defaultStaticPassword, "CHANGEME_TO_SOMETHING_VERY_STRONG") {
			return fmt.Errorf("auth.static_password must be set to a unique secret; run 'ferry init-config' or enable dev_mode explicitly")
		}
	}

	if !c.LDAP.Enabled {
		return nil
	}

	return fmt.Errorf("ldap.enabled is reserved for a future release and is not implemented yet")
}
