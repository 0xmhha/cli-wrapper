// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"
	"strings"
)

// ExpandEnv replaces ${VAR} and ${VAR:-default} occurrences in s.
// An unresolved ${VAR} without a default returns an error.
func ExpandEnv(s string) (string, error) {
	var out strings.Builder
	i := 0
	for i < len(s) {
		// $$ -> literal $
		if i+1 < len(s) && s[i] == '$' && s[i+1] == '$' {
			out.WriteByte('$')
			i += 2
			continue
		}
		if i+1 < len(s) && s[i] == '$' && s[i+1] == '{' {
			end := strings.Index(s[i+2:], "}")
			if end < 0 {
				return "", fmt.Errorf("config: unterminated ${ at %d", i)
			}
			expr := s[i+2 : i+2+end]
			val, err := resolveEnv(expr)
			if err != nil {
				return "", err
			}
			out.WriteString(val)
			i += 2 + end + 1
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String(), nil
}

func resolveEnv(expr string) (string, error) {
	if idx := strings.Index(expr, ":-"); idx >= 0 {
		name := expr[:idx]
		def := expr[idx+2:]
		if v, ok := os.LookupEnv(name); ok {
			return v, nil
		}
		return def, nil
	}
	v, ok := os.LookupEnv(expr)
	if !ok {
		return "", fmt.Errorf("config: env var %q not set", expr)
	}
	return v, nil
}
