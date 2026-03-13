package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
	"gopkg.in/yaml.v3"
)

const (
	DefaultAPIURL  = "https://api.texops.dev"
	keyringService = "texops"
	keyringJWTUser = "jwt"
)

func ResolveAPIURL(cfg Config) string {
	if v := os.Getenv("TX_API_URL"); v != "" {
		return v
	}
	if cfg.APIURL != "" {
		return cfg.APIURL
	}
	return DefaultAPIURL
}

var KeyringGet = keyring.Get

var CredentialsFilePath = credentialsFilePath

func credentialsFilePath() string {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir != "" {
		if !filepath.IsAbs(configDir) {
			// XDG spec requires absolute paths; ignore relative values
			// to avoid writing credentials under the current working directory
			configDir = ""
		}
	}
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.Getenv("HOME")
		}
		if home == "" {
			return ""
		}
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "texops", "credentials.yaml")
}

// jwtExpiry extracts the exp claim from a JWT without validating the signature.
// Returns zero time if the JWT cannot be decoded or has no exp claim.
func jwtExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}

// ResolveAuth returns a Bearer token for API authentication.
// It checks in order: TX_API_TOKEN env var, JWT from credentials file,
// JWT from keyring. Each source can override the ones below it.
// If a JWT is found but expired, it falls through to the next source.
func ResolveAuth() (string, error) {
	// 1. API token from environment variable (highest priority override)
	if token := os.Getenv("TX_API_TOKEN"); token != "" {
		return token, nil
	}

	// 2. JWT from credentials file (explicit file override)
	if jwt, err := readJWTCredential(); err == nil && jwt != "" {
		if exp := jwtExpiry(jwt); !exp.IsZero() && exp.Before(time.Now()) {
			// JWT expired, fall through
		} else {
			return jwt, nil
		}
	}

	// 3. JWT from keyring (default storage)
	if jwt, err := KeyringGet(keyringService, keyringJWTUser); err == nil && jwt != "" {
		if exp := jwtExpiry(jwt); !exp.IsZero() && exp.Before(time.Now()) {
			// JWT expired, fall through
		} else {
			return jwt, nil
		}
	}

	return "", fmt.Errorf("not authenticated: run 'tx login' or set TX_API_TOKEN")
}

func storeJWT(jwt string) error {
	if err := KeyringSet(keyringService, keyringJWTUser, jwt); err != nil {
		// Keyring unavailable, fall back to file-based storage
		path, fileErr := writeJWTCredential(jwt)
		if fileErr != nil {
			return fmt.Errorf("failed to store JWT: keyring error: %v, file error: %w", err, fileErr)
		}
		_ = path // stored successfully in file
		return nil
	}
	return nil
}

type jwtCredentialsFile struct {
	JWT string `yaml:"jwt"`
}

func readJWTCredential() (string, error) {
	path := CredentialsFilePath()
	if path == "" {
		return "", fmt.Errorf("cannot determine credentials file path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var creds jwtCredentialsFile
	if err := yaml.Unmarshal(data, &creds); err != nil {
		return "", err
	}
	return creds.JWT, nil
}

func writeJWTCredential(jwt string) (string, error) {
	path := CredentialsFilePath()
	if path == "" {
		return "", fmt.Errorf("cannot determine credentials file path: no home directory")
	}
	dir := filepath.Dir(path)

	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create credentials directory: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to set credentials directory permissions: %w", err)
	}

	creds := jwtCredentialsFile{JWT: jwt}
	data, err := yaml.Marshal(creds)
	if err != nil {
		return "", fmt.Errorf("failed to marshal credentials: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", fmt.Errorf("failed to write credentials file: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return "", fmt.Errorf("failed to set credentials file permissions: %w", err)
	}

	return path, nil
}
