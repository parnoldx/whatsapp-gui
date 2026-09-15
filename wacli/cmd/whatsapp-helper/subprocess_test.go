package main

// Regression tests for the subprocess seams. The rest of the suite stubs every
// exec function, which is how two double-receive deadlocks shipped: the real
// clipboardText and transcodeVoice hung forever on the success path. These call
// the real ones.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// returnsWithin fails instead of hanging the package for the full test timeout
// when fn deadlocks.
func returnsWithin(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not return within %s — deadlocked", what, d)
	}
}

// fakeOnPath puts a stub executable first on PATH.
func fakeOnPath(t *testing.T, name, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestClipboardTextReadsTheClipboard(t *testing.T) {
	fakeOnPath(t, "wl-paste", "#!/bin/sh\necho hi\n")
	got := ""
	returnsWithin(t, 5*time.Second, "clipboardText", func() { got = clipboardText() })
	if strings.TrimSpace(got) != "hi" {
		t.Errorf("clipboardText = %q, want %q", got, "hi")
	}
}

func TestClipboardTextKillsASlowPaste(t *testing.T) {
	// exec, so the kill reaches the sleeper instead of leaving a grandchild
	// holding the output pipe open.
	fakeOnPath(t, "wl-paste", "#!/bin/sh\nexec sleep 30\n")
	got := "unset"
	returnsWithin(t, 5*time.Second, "clipboardText", func() { got = clipboardText() })
	if got != "" {
		t.Errorf("clipboardText = %q, want empty after the timeout", got)
	}
}

func TestRunCommandHonoursItsTimeout(t *testing.T) {
	var err error
	start := time.Now()
	returnsWithin(t, 5*time.Second, "runCommand", func() {
		_, err = runCommand("sleep", []string{"30"}, 200*time.Millisecond)
	})
	if !errors.Is(err, errTimedOut) {
		t.Errorf("runCommand err = %v, want errTimedOut", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("runCommand took %s; the timeout was ignored", elapsed)
	}
}

func TestRunTimeoutReportsACommandThatFails(t *testing.T) {
	cmd := exec.Command("/usr/bin/false")
	if err := runTimeout(cmd, 5*time.Second); err == nil {
		t.Error("runTimeout reported success for a failing command")
	}
}

func TestTranscodeVoiceReturnsAnOpusFile(t *testing.T) {
	if _, err := execLookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	t.Setenv("PA_WHATSAPP_STATE", t.TempDir())
	src := filepath.Join(t.TempDir(), "in.wav")
	if _, err := runCommand("ffmpeg", []string{
		"-y", "-f", "s16le", "-ar", "8000", "-ac", "1", "-t", "1", "-i", "/dev/zero", src,
	}, 30*time.Second); err != nil {
		t.Skipf("could not record a stub wav: %v", err)
	}
	got := ""
	returnsWithin(t, 90*time.Second, "transcodeVoice", func() {
		var he *helperError
		got, he = transcodeVoice(src)
		if he != nil {
			t.Errorf("transcodeVoice failed: %s", he.msg)
		}
	})
	if !isOggOpus(got) {
		t.Errorf("transcodeVoice returned %q, which is not Ogg/Opus", got)
	}
}
