package cli_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/texops/tx/internal/cli"
)

// makeTestJWT creates a minimal JWT-like token with a given expiry for testing.
// It has a valid three-part structure with a base64url-encoded payload containing
// an exp claim, but no valid signature.
func makeTestJWT(exp time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{"exp": exp.Unix(), "sub": "usr_test"})
	payload := base64.RawURLEncoding.EncodeToString(claims)
	return header + "." + payload + ".fake-sig"
}

func TestResolveAPIURL(t *testing.T) {
	t.Run("returns default when nothing is set", func(t *testing.T) {
		t.Setenv("TX_API_URL", "")

		url := cli.ResolveAPIURL(cli.Config{})
		assert.Equal(t, cli.DefaultAPIURL, url)
	})

	t.Run("returns value from config when set", func(t *testing.T) {
		t.Setenv("TX_API_URL", "")

		url := cli.ResolveAPIURL(cli.Config{APIURL: "https://custom.example.com"})
		assert.Equal(t, "https://custom.example.com", url)
	})

	t.Run("returns env var over config", func(t *testing.T) {
		t.Setenv("TX_API_URL", "https://env.example.com")

		url := cli.ResolveAPIURL(cli.Config{APIURL: "https://custom.example.com"})
		assert.Equal(t, "https://env.example.com", url)
	})

	t.Run("returns env var over default", func(t *testing.T) {
		t.Setenv("TX_API_URL", "https://env.example.com")

		url := cli.ResolveAPIURL(cli.Config{})
		assert.Equal(t, "https://env.example.com", url)
	})
}

func withTempCredentialsDir(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	origFn := cli.CredentialsFilePath
	t.Cleanup(func() { cli.CredentialsFilePath = origFn })
	credPath := filepath.Join(tmpDir, "texops", "credentials.yaml")
	cli.CredentialsFilePath = func() string { return credPath }
	return credPath
}

func TestCredentialsFilePath(t *testing.T) {
	t.Run("returns default path when XDG_CONFIG_HOME is not set", func(t *testing.T) {
		origFn := cli.CredentialsFilePath
		defer func() { cli.CredentialsFilePath = origFn }()

		t.Setenv("XDG_CONFIG_HOME", "")

		path := cli.CredentialsFilePath()
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		expected := filepath.Join(home, ".config", "texops", "credentials.yaml")
		assert.Equal(t, expected, path)
	})

	t.Run("respects XDG_CONFIG_HOME", func(t *testing.T) {
		origFn := cli.CredentialsFilePath
		defer func() { cli.CredentialsFilePath = origFn }()

		t.Setenv("XDG_CONFIG_HOME", "/custom/config")

		path := cli.CredentialsFilePath()
		assert.Equal(t, filepath.Join("/custom/config", "texops", "credentials.yaml"), path)
	})

	t.Run("ignores relative XDG_CONFIG_HOME", func(t *testing.T) {
		origFn := cli.CredentialsFilePath
		defer func() { cli.CredentialsFilePath = origFn }()

		t.Setenv("XDG_CONFIG_HOME", ".config")

		path := cli.CredentialsFilePath()
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		expected := filepath.Join(home, ".config", "texops", "credentials.yaml")
		assert.Equal(t, expected, path)
	})
}

func TestResolveAuth(t *testing.T) {
	t.Run("env var wins over everything", func(t *testing.T) {
		credPath := withTempCredentialsDir(t)
		require.NoError(t, os.MkdirAll(filepath.Dir(credPath), 0700))
		require.NoError(t, os.WriteFile(credPath, []byte("jwt: file-jwt\n"), 0600))

		origGet := cli.KeyringGet
		defer func() { cli.KeyringGet = origGet }()
		cli.KeyringGet = func(service, user string) (string, error) {
			if user == "jwt" {
				return "keyring-jwt", nil
			}
			return "", errors.New("not found")
		}

		t.Setenv("TX_API_TOKEN", "texops_token_env-token")

		token, err := cli.ResolveAuth()
		require.NoError(t, err)
		assert.Equal(t, "texops_token_env-token", token)
	})

	t.Run("credentials file wins over keyring", func(t *testing.T) {
		credPath := withTempCredentialsDir(t)
		require.NoError(t, os.MkdirAll(filepath.Dir(credPath), 0700))
		require.NoError(t, os.WriteFile(credPath, []byte("jwt: file-jwt\n"), 0600))

		origGet := cli.KeyringGet
		defer func() { cli.KeyringGet = origGet }()
		cli.KeyringGet = func(service, user string) (string, error) {
			if user == "jwt" {
				return "keyring-jwt", nil
			}
			return "", errors.New("not found")
		}

		t.Setenv("TX_API_TOKEN", "")

		token, err := cli.ResolveAuth()
		require.NoError(t, err)
		assert.Equal(t, "file-jwt", token)
	})

	t.Run("falls back to keyring when no env var or file", func(t *testing.T) {
		_ = withTempCredentialsDir(t) // no file created

		origGet := cli.KeyringGet
		defer func() { cli.KeyringGet = origGet }()
		cli.KeyringGet = func(service, user string) (string, error) {
			if user == "jwt" {
				return "keyring-jwt", nil
			}
			return "", errors.New("not found")
		}

		t.Setenv("TX_API_TOKEN", "")

		token, err := cli.ResolveAuth()
		require.NoError(t, err)
		assert.Equal(t, "keyring-jwt", token)
	})

	t.Run("returns error when nothing available", func(t *testing.T) {
		_ = withTempCredentialsDir(t) // no file created

		origGet := cli.KeyringGet
		defer func() { cli.KeyringGet = origGet }()
		cli.KeyringGet = func(service, user string) (string, error) {
			return "", fmt.Errorf("not found")
		}
		t.Setenv("TX_API_TOKEN", "")

		_, err := cli.ResolveAuth()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not authenticated")
		assert.Contains(t, err.Error(), "tx login")
		assert.Contains(t, err.Error(), "TX_API_TOKEN")
	})

	t.Run("expired JWT falls back to TX_API_TOKEN", func(t *testing.T) {
		_ = withTempCredentialsDir(t)
		expiredJWT := makeTestJWT(time.Now().Add(-1 * time.Hour))

		origGet := cli.KeyringGet
		defer func() { cli.KeyringGet = origGet }()
		cli.KeyringGet = func(service, user string) (string, error) {
			if user == "jwt" {
				return expiredJWT, nil
			}
			return "", errors.New("not found")
		}
		t.Setenv("TX_API_TOKEN", "texops_token_fallback-token")

		token, err := cli.ResolveAuth()
		require.NoError(t, err)
		assert.Equal(t, "texops_token_fallback-token", token)
	})

	t.Run("expired JWT with no token returns not authenticated error", func(t *testing.T) {
		_ = withTempCredentialsDir(t)
		expiredJWT := makeTestJWT(time.Now().Add(-1 * time.Hour))

		origGet := cli.KeyringGet
		defer func() { cli.KeyringGet = origGet }()
		cli.KeyringGet = func(service, user string) (string, error) {
			if user == "jwt" {
				return expiredJWT, nil
			}
			return "", errors.New("not found")
		}
		t.Setenv("TX_API_TOKEN", "")

		_, err := cli.ResolveAuth()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not authenticated")
		assert.Contains(t, err.Error(), "tx login")
	})

	t.Run("valid JWT is returned even when near expiry", func(t *testing.T) {
		_ = withTempCredentialsDir(t)
		validJWT := makeTestJWT(time.Now().Add(1 * time.Hour))

		origGet := cli.KeyringGet
		defer func() { cli.KeyringGet = origGet }()
		cli.KeyringGet = func(service, user string) (string, error) {
			if user == "jwt" {
				return validJWT, nil
			}
			return "", errors.New("not found")
		}
		t.Setenv("TX_API_TOKEN", "")

		token, err := cli.ResolveAuth()
		require.NoError(t, err)
		assert.Equal(t, validJWT, token)
	})

	t.Run("TX_API_KEY is no longer recognized", func(t *testing.T) {
		_ = withTempCredentialsDir(t)

		origGet := cli.KeyringGet
		defer func() { cli.KeyringGet = origGet }()
		cli.KeyringGet = func(service, user string) (string, error) {
			return "", errors.New("not found")
		}
		t.Setenv("TX_API_TOKEN", "")
		t.Setenv("TX_API_KEY", "old-api-key")

		_, err := cli.ResolveAuth()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not authenticated")
	})
}
