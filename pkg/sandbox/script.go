package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Script is a file that a Provider copies into the sandbox before execution.
type Script struct {
	InnerPath string      // path relative to the sandbox root
	Mode      os.FileMode // file mode
	Contents  []byte      // literal bytes
}

// WriteInto writes the script into root, creating parent directories as needed.
// InnerPath is interpreted relative to root; a leading slash is ignored.
func (s Script) WriteInto(root string) error {
	rel := strings.TrimPrefix(s.InnerPath, "/")
	dst := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("sandbox: mkdir %s: %w", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, s.Contents, s.Mode); err != nil {
		return fmt.Errorf("sandbox: write %s: %w", dst, err)
	}
	// Ensure the executable bit is honored even if umask would clip it.
	if err := os.Chmod(dst, s.Mode); err != nil {
		return fmt.Errorf("sandbox: chmod %s: %w", dst, err)
	}
	return nil
}

// NewEntrypointScript builds a POSIX-sh script that exports env vars and
// execs cmd with args.
func NewEntrypointScript(cmd string, args []string, env map[string]string) Script {
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -e\n")
	for k, v := range env {
		fmt.Fprintf(&b, "export %s=%s\n", k, shellQuote(v))
	}
	b.WriteString("exec ")
	b.WriteString(cmd)
	for _, a := range args {
		b.WriteString(" ")
		b.WriteString(shellQuote(a))
	}
	b.WriteString("\n")
	return Script{
		InnerPath: "/cliwrap/entrypoint.sh",
		Mode:      0o755,
		Contents:  []byte(b.String()),
	}
}

// shellQuote wraps s in single quotes, escaping any embedded single quotes.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n'\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
