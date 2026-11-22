// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// The bounds of an app's environment variables (ADR-0054).
const (
	MaxAppEnv          = 100
	MaxAppEnvValue     = 4096
	MaxAppEnvNameBytes = 64
	// MaxAppEnvBytes bounds the variables as JSON, so a spec stays well
	// below agentrpc.MaxFrame whatever they hold.
	MaxAppEnvBytes = 32 << 10
)

var appEnvNameRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

// reservedAppEnv are set by Hostashi for every app; a variable of the
// same name would replace them (an EnvironmentFile wins over
// Environment=).
var reservedAppEnv = []string{"HOST", "PORT", "HOME", "USER", "LOGNAME", "PATH"}

// AppEnvVar is one environment variable of an app. Its value is a secret:
// String leaves it out, so logs and error messages show the name only.
type AppEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (v AppEnvVar) String() string { return v.Name + "=…" }

// GoString leaves the value out of %#v too.
func (v AppEnvVar) GoString() string { return v.String() }

// ValidateAppEnvName checks a variable's name: upper case letters, digits
// and '_', not starting with a digit, at most 64 characters, and not one
// Hostashi sets itself.
func ValidateAppEnvName(name string) error {
	if !appEnvNameRe.MatchString(name) {
		return fmt.Errorf("app environment: %q is not a name of A-Z, 0-9 and _ (at most %d, no digit first)", name, MaxAppEnvNameBytes)
	}

	for _, r := range reservedAppEnv {
		if name == r {
			return fmt.Errorf("app environment: %s is set by Hostashi", name)
		}
	}

	return nil
}

// ValidateAppEnv checks an app's variables: at most MaxAppEnv, unique
// names, values of at most MaxAppEnvValue bytes of UTF-8 on one line
// (no NUL, newline or carriage return), and at most MaxAppEnvBytes as JSON.
func ValidateAppEnv(env []AppEnvVar) error {
	if len(env) > MaxAppEnv {
		return fmt.Errorf("app environment: at most %d variables", MaxAppEnv)
	}

	seen := map[string]bool{}

	for _, v := range env {
		if err := ValidateAppEnvName(v.Name); err != nil {
			return err
		}

		switch {
		case seen[v.Name]:
			return fmt.Errorf("app environment: %s is set twice", v.Name)
		case len(v.Value) > MaxAppEnvValue:
			return fmt.Errorf("app environment: the value of %s is longer than %d bytes", v.Name, MaxAppEnvValue)
		case !utf8.ValidString(v.Value):
			return fmt.Errorf("app environment: the value of %s is not UTF-8", v.Name)
		case strings.ContainsAny(v.Value, "\x00\n\r"):
			return fmt.Errorf("app environment: the value of %s holds a NUL, newline or carriage return", v.Name)
		}

		seen[v.Name] = true
	}

	b, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("app environment: %w", err)
	}

	if len(b) > MaxAppEnvBytes {
		return errors.New("app environment: the variables are too large together; at most 32 KiB")
	}

	return nil
}
