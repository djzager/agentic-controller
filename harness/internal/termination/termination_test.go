package termination

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWriteRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "termination-log")
	t.Setenv(envPath, path)

	want := `source repository "https://svn.example/repo" uses SCM kind "subversion"; only git is supported`
	if err := Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read termination log: %v", err)
	}
	if string(raw) != want {
		t.Errorf("termination log = %q, want %q", raw, want)
	}
}

func TestWriteTruncatesLongMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "termination-log")
	t.Setenv(envPath, path)

	long := strings.Repeat("x", maxMessageLen*2)
	if err := Write(long); err != nil {
		t.Fatalf("Write: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read termination log: %v", err)
	}
	// The message must stay under the kubelet's 4096-byte cap.
	if len(raw) > 4096 {
		t.Errorf("payload = %d bytes, want <= 4096", len(raw))
	}
	if len(raw) != maxMessageLen {
		t.Errorf("message len = %d, want %d", len(raw), maxMessageLen)
	}
}

func TestWriteTruncatesOnRuneBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "termination-log")
	t.Setenv(envPath, path)

	// "世" is 3 bytes; a run of them straddles the cut point so a naive
	// byte-slice truncation would leave a partial (invalid) rune.
	long := strings.Repeat("世", maxMessageLen)
	if err := Write(long); err != nil {
		t.Fatalf("Write: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read termination log: %v", err)
	}
	if len(raw) > maxMessageLen {
		t.Errorf("payload = %d bytes, want <= %d", len(raw), maxMessageLen)
	}
	if !utf8.Valid(raw) {
		t.Errorf("truncated payload is not valid UTF-8: %q", raw)
	}
}

func TestWriteDefaultPath(t *testing.T) {
	// With no override set, path() falls back to the kubelet default.
	t.Setenv(envPath, "")
	if got := path(); got != defaultPath {
		t.Errorf("path() = %q, want %q", got, defaultPath)
	}
}
