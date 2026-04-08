package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExpandEnv_Simple(t *testing.T) {
	t.Setenv("FOO", "bar")
	got, err := ExpandEnv("hello ${FOO}")
	require.NoError(t, err)
	require.Equal(t, "hello bar", got)
}

func TestExpandEnv_MissingIsError(t *testing.T) {
	_, err := ExpandEnv("hello ${NOT_SET}")
	require.Error(t, err)
}

func TestExpandEnv_DefaultFallback(t *testing.T) {
	got, err := ExpandEnv("hello ${NOT_SET:-world}")
	require.NoError(t, err)
	require.Equal(t, "hello world", got)
}

func TestExpandEnv_MultipleVars(t *testing.T) {
	t.Setenv("A", "1")
	t.Setenv("B", "2")
	got, err := ExpandEnv("${A}+${B}=${C:-3}")
	require.NoError(t, err)
	require.Equal(t, "1+2=3", got)
}

func TestExpandEnv_NoVarsIsPassThrough(t *testing.T) {
	got, err := ExpandEnv("plain text")
	require.NoError(t, err)
	require.Equal(t, "plain text", got)
}

func TestExpandEnv_EscapeDollar(t *testing.T) {
	got, err := ExpandEnv("price is $$5")
	require.NoError(t, err)
	require.Equal(t, "price is $5", got)
}
