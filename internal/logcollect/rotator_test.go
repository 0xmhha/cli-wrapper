package logcollect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRotator_WritesAndRotates(t *testing.T) {
	dir := t.TempDir()
	r, err := NewFileRotator(RotatorOptions{
		Dir:      dir,
		BaseName: "test",
		MaxSize:  16,
		MaxFiles: 3,
	})
	require.NoError(t, err)
	defer r.Close()

	// Each write is 8 bytes; after two writes the rotator should rotate.
	_, err = r.Write([]byte("AAAABBBB"))
	require.NoError(t, err)
	_, err = r.Write([]byte("CCCCDDDD"))
	require.NoError(t, err)
	_, err = r.Write([]byte("EEEEFFFF")) // triggers rotation
	require.NoError(t, err)

	files, err := filepath.Glob(filepath.Join(dir, "test*.log"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(files), 2, "should have rotated at least once")
}

func TestRotator_EnforcesMaxFiles(t *testing.T) {
	dir := t.TempDir()
	r, err := NewFileRotator(RotatorOptions{
		Dir:      dir,
		BaseName: "t",
		MaxSize:  4,
		MaxFiles: 2,
	})
	require.NoError(t, err)
	defer r.Close()

	for i := 0; i < 10; i++ {
		_, err := r.Write([]byte("abcde"))
		require.NoError(t, err)
	}

	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.LessOrEqual(t, len(files), 2)
}
