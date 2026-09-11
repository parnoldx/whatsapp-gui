package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	jidRe        = regexp.MustCompile(`^[A-Za-z0-9._:-]+@[A-Za-z0-9._-]+$`)
	urlRe        = regexp.MustCompile(`https?://[^\s<>"']+`)
	mentionIDRe  = regexp.MustCompile(`@(\d{8,})\b`)
	albumRe      = regexp.MustCompile(`^\[Album:\s*(\d+)\s+\w+\]$`)
	wsRe         = regexp.MustCompile(`\s+`)
	whitespaceRe = regexp.MustCompile(`\s`)
)

func storeDir(explicit string) string {
	if explicit != "" {
		p, _ := expandHome(explicit)
		return p
	}
	if env := os.Getenv("WACLI_STORE_DIR"); env != "" {
		p, _ := expandHome(env)
		return p
	}
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "wacli")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "wacli")
}

func stateDir() string {
	var p string
	if raw := os.Getenv("PA_WHATSAPP_STATE"); raw != "" {
		p, _ = expandHome(raw)
	} else if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		p = filepath.Join(xdg, "pa-whatsapp")
	} else {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, ".local", "state", "pa-whatsapp")
	}
	_ = os.MkdirAll(p, 0o700)
	_ = os.Chmod(p, 0o700)
	return p
}

func expandHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path, err
		}
		return filepath.Join(home, path[1:]), nil
	}
	return path, nil
}

func dbPath(store string) string { return filepath.Join(store, "wacli.db") }

func prefsPath() string     { return filepath.Join(stateDir(), "prefs.json") }
func mediaIndexPath() string { return filepath.Join(stateDir(), "media-index.json") }

func mediaKey(jid, msgID string) string {
	sum := sha256.Sum256([]byte(jid + "\n" + msgID))
	return hex.EncodeToString(sum[:])
}

var mimeExt = map[string]string{
	"image/jpeg": ".jpg", "image/jpg": ".jpg", "image/png": ".png",
	"image/webp": ".webp", "image/gif": ".gif", "video/mp4": ".mp4",
	"audio/ogg": ".ogg", "audio/mpeg": ".mp3", "audio/mp4": ".m4a",
	"application/pdf": ".pdf",
}

func mediaExt(mime, filename, kind string) string {
	name := strings.TrimPrefix(filepath.Ext(filename), ".")
	if name != "" && len(name) <= 8 && !whitespaceRe.MatchString(name) {
		return "." + name
	}
	mime = strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
	if ext, ok := mimeExt[mime]; ok {
		return ext
	}
	switch kind {
	case "image":
		return ".jpg"
	case "sticker":
		return ".webp"
	case "gif":
		return ".mp4"
	case "video":
		return ".mp4"
	case "voice":
		return ".ogg"
	case "audio":
		return ".ogg"
	default:
		return ""
	}
}

func fileURL(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	return u.String()
}

// --- JSON state files (prefs, media index, avatar index, link cache) ---

// atomicWriteJSON writes indented JSON through a 0600 temp file + rename.
func atomicWriteJSON(path string, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return
	}
	data = append(data, '\n')
	tmp := filepath.Join(filepath.Dir(path), ".tmp-"+randomHex(8))
	fd, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return
	}
	if _, err := fd.Write(data); err == nil {
		_ = fd.Sync()
	}
	_ = fd.Close()
	_ = os.Rename(tmp, path)
	_ = os.Chmod(path, 0o600)
}

func readJSONMap(path string) map[string]any {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil || data == nil {
		return nil
	}
	return data
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = cryptoRead(b)
	return hex.EncodeToString(b)
}

// --- prefs ---

type prefs struct {
	Receipts bool          `json:"receipts"`
	Acks     map[string]int64 `json:"acks"`
}

func loadPrefs() prefs {
	out := prefs{Acks: map[string]int64{}}
	data := readJSONMap(prefsPath())
	if data == nil {
		return out
	}
	out.Receipts, _ = data["receipts"].(bool)
	if acks, ok := data["acks"].(map[string]any); ok {
		for key, value := range acks {
			if !jidRe.MatchString(key) {
				continue
			}
			if ts, ok := value.(float64); ok {
				out.Acks[key] = int64(ts)
			}
		}
	}
	return out
}

func savePrefs(p prefs) {
	if p.Acks == nil {
		p.Acks = map[string]int64{}
	}
	atomicWriteJSON(prefsPath(), map[string]any{"receipts": p.Receipts, "acks": p.Acks})
}

// --- media index ---

func loadMediaIndex() map[string]string {
	raw := readJSONMap(mediaIndexPath())
	out := map[string]string{}
	for key, value := range raw {
		path, ok := value.(string)
		if !ok {
			continue
		}
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			out[key] = path
		}
	}
	return out
}

func rememberMedia(jid, msgID, local string) {
	index := loadMediaIndex()
	index[mediaKey(jid, msgID)] = local
	atomicWriteJSON(mediaIndexPath(), index)
}

func cachedMedia(jid, msgID string) string {
	return loadMediaIndex()[mediaKey(jid, msgID)]
}

// --- error humanizing ---

func humanizeWacliError(text string) string {
	text = strings.ReplaceAll(strings.TrimSpace(text), "\n", " ")
	low := strings.ToLower(text)
	switch {
	case strings.Contains(low, "locked") || strings.Contains(low, "lock-wait") || strings.Contains(low, "another wacli"):
		return "WhatsApp is busy syncing. Try again in a moment."
	case strings.Contains(low, "timed out") || strings.Contains(low, "timeout"):
		return "WhatsApp took too long to respond."
	case text == "":
		return "wacli command failed"
	}
	if len(text) > 180 {
		text = text[:180]
	}
	return text
}

// --- names and placeholders ---

func looksLikeID(text, jid string) bool {
	name := strings.TrimSpace(text)
	if name == "" || strings.EqualFold(name, "unknown") || strings.EqualFold(name, "unknown group") {
		return true
	}
	if jid != "" && name == jid {
		return true
	}
	return jidRe.MatchString(name)
}

func fallbackFromJID(jid string) string {
	value := strings.TrimSpace(jid)
	if value == "" {
		return "Unknown"
	}
	local, host, _ := strings.Cut(value, "@")
	if host == "g.us" || host == "lid" {
		return "Unknown group"
	}
	if isAllDigits(local) && len(local) >= 8 {
		return "+" + local
	}
	return "Unknown"
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// pickName returns the first part that looks like a real name.
func pickName(jid string, parts ...string) string {
	for _, part := range parts {
		text := strings.TrimSpace(part)
		if text != "" && !looksLikeID(text, jid) {
			return text
		}
	}
	return fallbackFromJID(jid)
}

// realPersonName is pickName, but empty when we'd only have a number or JID.
func realPersonName(jid string, parts ...string) string {
	name := pickName(jid, parts...)
	if name == "" || looksLikeID(name, jid) || isAllDigits(strings.TrimPrefix(name, "+")) {
		return ""
	}
	return name
}

var placeholderMap = map[string]string{
	"sent image": "Photo", "image": "Photo",
	"sent video": "Video", "video": "Video",
	"sent gif": "GIF", "gif": "GIF",
	"sent sticker": "Sticker", "sticker": "Sticker",
	"sent audio": "Audio", "audio": "Audio",
	"sent voice": "Voice note", "voice": "Voice note", "ptt": "Voice note",
	"sent document": "Document", "document": "Document",
	"sent location": "Location", "location": "Location",
	"sent contact": "Contact", "contact": "Contact",
}

func albumCount(text string) int {
	m := albumRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func placeholderLabel(value string) string {
	key := wsRe.ReplaceAllString(strings.TrimSpace(value), " ")
	if key == "(message)" || key == "message" {
		return " "
	}
	if albumCount(key) > 0 {
		return "Photo"
	}
	return placeholderMap[strings.ToLower(key)]
}

func firstURL(text string) string {
	m := urlRe.FindString(text)
	if m == "" {
		return ""
	}
	return strings.TrimRight(m, ").,;:!?]}")
}

func humanPreview(raw string) string {
	text := strings.TrimSpace(strings.ReplaceAll(raw, "\n", " "))
	if text == "" {
		return ""
	}
	var last string
	for _, line := range strings.Split(raw, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			last = trimmed
		}
	}
	if mapped := strings.TrimSpace(placeholderLabel(last)); mapped != "" {
		return truncate(mapped, 180)
	}
	if placeholderLabel(last) != "" {
		return "Unsupported message"
	}
	if u := firstURL(text); u != "" {
		preview := describeLink(u)
		if preview.site != "" && preview.site != preview.host {
			if preview.label != "" && preview.label != preview.site {
				return preview.site + " " + preview.label
			}
			return preview.site
		}
	}
	return truncate(text, 180)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Don't split a UTF-8 rune.
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// visibleBody splits a stored row into the bubble body and the media caption.
func visibleBody(display, text, caption, kind string) (string, string) {
	captionText := strings.TrimSpace(caption)
	rawText := strings.TrimSpace(text)
	displayText := strings.TrimSpace(display)
	if placeholderLabel(captionText) != "" {
		captionText = ""
	}
	if mediaKinds[kind] {
		body := captionText
		if body == "" {
			body = rawText
		}
		if placeholderLabel(body) != "" {
			body = ""
		}
		return body, body
	}
	body := rawText
	if body == "" {
		body = displayText
	}
	lines := strings.Split(body, "\n")
	for len(lines) > 0 && placeholderLabel(strings.TrimSpace(lines[len(lines)-1])) != "" {
		lines = lines[:len(lines)-1]
	}
	body = strings.TrimSpace(strings.Join(lines, "\n"))
	if placeholderLabel(body) != "" {
		body = captionText
	}
	return body, captionText
}

// --- mentions ---

func mentionIDsIn(texts ...string) map[string]bool {
	out := map[string]bool{}
	for _, text := range texts {
		if text == "" {
			continue
		}
		for _, id := range mentionIDRe.FindAllStringSubmatch(text, -1) {
			out[id[1]] = true
		}
	}
	return out
}

func applyMentionNames(text string, names map[string]string) string {
	if text == "" || len(names) == 0 {
		return text
	}
	return mentionIDRe.ReplaceAllStringFunc(text, func(match string) string {
		id := match[1:]
		if name, ok := names[id]; ok {
			return "@" + name
		}
		return match
	})
}

func isMuted(mutedUntil, now int64) bool {
	if mutedUntil == -1 {
		return true
	}
	return mutedUntil > now
}

func now() int64 { return time.Now().Unix() }

// fileURLOk is fileURL, but only for files that exist.
func fileURLOk(path string) string {
	if path != "" && fileExists(path) {
		return fileURL(path)
	}
	return ""
}
