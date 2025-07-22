// Package utils provides utility functions for the model validation operator
package utils //nolint:revive

import (
	"flag"
	"os"
)

// StringFlagOrEnv defines a string flag which can be set by an environment variable.
// Precedence: flag > env var > default value.
func StringFlagOrEnv(p *string, name string, envName string, defaultValue string, usage string) {
	envValue := os.Getenv(envName)
	if envValue != "" {
		defaultValue = envValue
	}
	flag.StringVar(p, name, defaultValue, usage)
}
