package main

import (
	"os"
	"os/exec"
	"strings"
	"time"
)

// --- clipboard ---

// Indirection for tests (the Python port monkeypatched these).
var (
	clipboardTextFn     = clipboardText
	setClipboardFn      = setClipboard
	emojiOverlayOpenFn  = emojiOverlayOpen
	summonEmojiPickerFn = summonEmojiPicker
)

func clipboardText() string {
	wl, err := execLookPath("wl-paste")
	if err != nil {
		return ""
	}
	cmd := exec.Command(wl, "-n")
	done := make(chan error, 1)
	var out []byte
	go func() {
		var err error
		out, err = cmd.Output()
		done <- err
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		return ""
	}
	if <-done != nil || len(out) == 0 {
		return ""
	}
	return string(out)
}

func setClipboard(text string) {
	wl, err := execLookPath("wl-copy")
	if err != nil {
		return
	}
	cmd := exec.Command(wl, "--type", "text/plain")
	cmd.Stdin = strings.NewReader(text)
	done := make(chan struct{}, 1)
	go func() { _ = cmd.Run(); done <- struct{}{} }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
	}
}

func emojiOverlayOpen() bool {
	out, err := exec.Command("hyprctl", "-j", "layers").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "omarchy-emojis")
}

func summonEmojiPicker() *helperError {
	shell, err := execLookPath("omarchy-shell")
	if err != nil {
		return fail("omarchy-shell is not installed")
	}
	cmd := exec.Command(shell, "shell", "summon", "omarchy.emojis", "{}")
	if err := cmd.Run(); err != nil {
		return fail("could not open emoji menu")
	}
	return nil
}

// reactionFromClipboard rejects junk so a stale clipboard entry doesn't fire.
func reactionFromClipboard(text string) string {
	s := strings.TrimSpace(text)
	if s == "" || strings.Contains(s, "\n") || len(s) > 32 {
		return ""
	}
	return s
}

// cmdPickEmoji opens the Omarchy emoji overlay and returns the chosen glyph.
//
// The overlay copies via wl-copy then pastes with wtype; we watch the
// clipboard. ponytail: empty the clipboard first so picking the already-
// copied emoji still registers; restore on cancel.
//
// watchOnly: the GUI already summoned the overlay (so it appears without
// waiting for this helper). Summoning again would toggle it closed.
func cmdPickEmoji(watchOnly bool) (map[string]any, *helperError) {
	old := clipboardTextFn()
	seen := false
	if watchOnly {
		appear := time.Now().Add(2 * time.Second)
		for time.Now().Before(appear) {
			if emojiOverlayOpenFn() {
				seen = true
				break
			}
			if got := reactionFromClipboard(clipboardTextFn()); got != "" {
				return map[string]any{"emoji": got}, nil
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !seen {
			return map[string]any{"emoji": reactionFromClipboard(clipboardTextFn())}, nil
		}
		setClipboardFn("")
	} else {
		setClipboardFn("")
		if he := summonEmojiPickerFn(); he != nil {
			return nil, he
		}
	}
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if emojiOverlayOpenFn() {
			seen = true
		}
		if got := reactionFromClipboard(clipboardTextFn()); got != "" {
			return map[string]any{"emoji": got}, nil
		}
		if seen && !emojiOverlayOpenFn() {
			time.Sleep(300 * time.Millisecond)
			if got := reactionFromClipboard(clipboardTextFn()); got != "" {
				return map[string]any{"emoji": got}, nil
			}
			if !watchOnly {
				setClipboardFn(old)
			}
			return map[string]any{"emoji": ""}, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !watchOnly {
		setClipboardFn(old)
	}
	return map[string]any{"emoji": ""}, nil
}

func cmdPickFiles() (map[string]any, *helperError) {
	zenity, err := execLookPath("zenity")
	if err != nil {
		return nil, fail("zenity is not installed")
	}
	cmd := exec.Command(zenity, "--file-selection", "--multiple", "--separator=\n", "--title=Attach files")
	var out []byte
	done := make(chan error, 1)
	go func() {
		var err error
		out, err = cmd.Output()
		done <- err
	}()
	select {
	case <-done:
	case <-time.After(300 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return map[string]any{"files": []map[string]any{}}, nil
	}
	if <-done != nil {
		return map[string]any{"files": []map[string]any{}}, nil
	}
	files := []map[string]any{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		path, he := validateFile(line)
		if he != nil {
			continue
		}
		files = append(files, map[string]any{
			"path": path, "fileUrl": fileURL(path), "name": fileNameOf(path),
		})
		if len(files) >= maxFiles {
			break
		}
	}
	return map[string]any{"files": files}, nil
}

func fileNameOf(path string) string {
	i := strings.LastIndexByte(path, '/')
	if i < 0 {
		return path
	}
	return path[i+1:]
}

func cmdClipboard() (map[string]any, *helperError) {
	wl, err := execLookPath("wl-paste")
	if err != nil {
		return nil, fail("wl-clipboard is not installed")
	}
	listed, err := exec.Command(wl, "-l").Output()
	if err != nil {
		return nil, fail("wl-clipboard is not installed")
	}
	lines := strings.Split(string(listed), "\n")
	imageExt := map[string]string{
		"image/png": ".png", "image/jpeg": ".jpg", "image/jpg": ".jpg",
		"image/webp": ".webp", "image/gif": ".gif",
	}
	imageType := ""
	for _, candidate := range []string{"image/png", "image/jpeg", "image/jpg", "image/webp", "image/gif"} {
		for _, line := range lines {
			if strings.TrimSpace(line) == candidate {
				imageType = candidate
				break
			}
		}
		if imageType != "" {
			break
		}
	}
	if imageType != "" {
		folder := strings.Join([]string{stateDir(), "clipboard"}, string(os.PathSeparator))
		if err := os.MkdirAll(folder, 0o700); err != nil {
			return nil, fail("could not prepare the clipboard folder")
		}
		_ = os.Chmod(folder, 0o700)
		dest := folder + "/" + randomHex(16) + imageExt[imageType]
		var out []byte
		cmd := exec.Command(wl, "-t", imageType)
		done := make(chan error, 1)
		go func() {
			var err error
			out, err = cmd.Output()
			done <- err
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			return nil, fail("clipboard image is empty")
		}
		if <-done != nil || len(out) == 0 {
			return nil, fail("clipboard image is empty")
		}
		if len(out) > maxFile {
			return nil, fail("clipboard image is too large")
		}
		if err := os.WriteFile(dest, out, 0o600); err != nil {
			return nil, fail("could not save the clipboard image")
		}
		return map[string]any{"kind": "file", "path": dest, "fileUrl": fileURL(dest), "name": fileNameOf(dest)}, nil
	}
	body, err := exec.Command(wl).Output()
	if err != nil {
		return nil, fail("clipboard is empty")
	}
	if len(body) > maxText {
		return nil, fail("clipboard text is too long")
	}
	return map[string]any{"kind": "text", "text": string(body)}, nil
}

func cmdOpenFile(pathStr string) (map[string]any, *helperError) {
	path, he := validateFile(pathStr)
	if he != nil {
		return nil, he
	}
	opener, err := execLookPath("xdg-open")
	if err != nil {
		opener = "xdg-open"
	}
	cmd := exec.Command(opener, path)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = detachProcAttr()
	if err := cmd.Start(); err != nil {
		return nil, fail("could not open the file")
	}
	go cmd.Wait()
	return map[string]any{"opened": true}, nil
}
