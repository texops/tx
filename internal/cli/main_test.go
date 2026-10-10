package cli_test

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	for _, name := range agentEnvVars {
		os.Unsetenv(name)
	}
	os.Exit(m.Run())
}
