// Package termination writes a human-readable failure message to the pod's
// termination log. The kubelet copies the termination message into the
// container's terminated state, from where the controller surfaces it on the
// AgentRun's Ready condition so the reason is visible to whoever created the
// run (e.g. a non-git source; see issue #143).
package termination

import (
	"os"
	"unicode/utf8"
)

// defaultPath is the kubelet's default terminationMessagePath. The harness
// container relies on this default (no custom path set on the pod spec).
const defaultPath = "/dev/termination-log"

// envPath overrides the termination log path, primarily for tests.
const envPath = "HARNESS_TERMINATION_LOG"

// maxMessageLen bounds the message so it stays well under the kubelet's
// 4096-byte termination-message cap.
const maxMessageLen = 3072

// Write records a human-readable failure message to the termination log. The
// path is taken from HARNESS_TERMINATION_LOG when set, otherwise
// /dev/termination-log. The message is truncated to fit the kubelet's cap.
// It is best-effort: callers ignore the error, since the failure itself is
// already surfaced via the process exit code and logs.
func Write(message string) error {
	return os.WriteFile(path(), []byte(truncate(message)), 0o644)
}

// truncate bounds message to maxMessageLen bytes without splitting a
// multi-byte UTF-8 rune, so the result stays valid UTF-8 in
// `kubectl describe agentrun`.
func truncate(message string) string {
	if len(message) <= maxMessageLen {
		return message
	}
	cut := maxMessageLen
	// Back off to the start of the rune that straddles the cut point.
	for cut > 0 && !utf8.RuneStart(message[cut]) {
		cut--
	}
	return message[:cut]
}

func path() string {
	if p := os.Getenv(envPath); p != "" {
		return p
	}
	return defaultPath
}
