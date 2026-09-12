package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func cryptoRead(b []byte) (int, error) { return rand.Read(b) }

func execLookPath(name string) (string, error) { return exec.LookPath(name) }

func runCommand(name string, args []string, timeout time.Duration) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// --- ffmpeg helpers ---

// videoThumb returns the path of a JPEG of the first frame, written next to a
// downloaded video. Empty on miss.
func videoThumb(local string) string {
	src := local
	if st, err := os.Stat(src); err != nil || st.IsDir() {
		return ""
	}
	dest := strings.TrimSuffix(src, filepath.Ext(src)) + ".jpg"
	if st, err := os.Stat(dest); err == nil && st.Size() > 32 {
		if srcSt, err := os.Stat(src); err == nil && st.ModTime().After(srcSt.ModTime()) {
			return dest
		}
	}
	ffmpeg, err := execLookPath("ffmpeg")
	if err == nil {
		cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin",
			"-y", "-ss", "0", "-i", src, "-frames:v", "1", "-q:v", "4", dest)
		cmd.Stdin = nil
		done := make(chan error, 1)
		go func() { done <- cmd.Run() }()
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	if st, err := os.Stat(dest); err == nil && st.Size() > 32 {
		_ = os.Chmod(dest, 0o600)
		return dest
	}
	_ = os.Remove(dest)
	return ""
}

func isOggOpus(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	data := make([]byte, 1024)
	n, _ := f.Read(data)
	data = data[:n]
	return len(data) >= 4 && string(data[:4]) == "OggS" && strings.Contains(string(data), "OpusHead")
}

func transcodeVoice(path string) (string, *helperError) {
	if isOggOpus(path) {
		return path, nil
	}
	ffmpeg, err := execLookPath("ffmpeg")
	if err != nil {
		return "", fail("voice notes must be OGG/Opus")
	}
	folder := filepath.Join(stateDir(), "voice-drafts")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return "", fail("could not prepare the voice note")
	}
	dest := filepath.Join(folder, randomHex(16)+".ogg")
	cmd := exec.Command(ffmpeg, "-y", "-i", path,
		"-c:a", "libopus", "-b:a", "24k", "-ac", "1", "-ar", "48000", dest)
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return "", fail("could not convert the voice note")
	}
	if err := <-done; err != nil || !isOggOpus(dest) {
		_ = os.Remove(dest)
		return "", fail("could not convert the voice note to OGG/Opus")
	}
	_ = os.Chmod(dest, 0o600)
	return dest, nil
}

// --- file validation ---

func validateFile(pathStr string) (string, *helperError) {
	path, _ := expandHome(strings.TrimSpace(pathStr))
	if !filepath.IsAbs(path) {
		return "", fail("file path must be absolute")
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fail("file does not exist")
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return "", fail("path is not a regular file")
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
		return "", fail("path is not a regular file")
	}
	if st.Size() > maxFile {
		return "", fail("file is larger than 100 MiB")
	}
	if st.Size() <= 0 {
		return "", fail("file is empty")
	}
	return path, nil
}

// --- quoted sender / sent-reply bookkeeping ---

func quotedSender(store, jid, msgID string) string {
	if msgID == "" {
		return ""
	}
	sibs := siblingJIDs(store, jid)
	con, err := sql.Open("sqlite3", "file:"+dbPath(store)+"?mode=ro&_busy_timeout=2000")
	if err != nil {
		return ""
	}
	defer con.Close()
	var sender sql.NullString
	con.QueryRow(
		fmt.Sprintf("SELECT sender_jid FROM messages WHERE chat_jid IN (%s) AND msg_id = ?", placeholders(len(sibs))),
		append(toAny(sibs), msgID)...).Scan(&sender)
	return strings.TrimSpace(sender.String)
}

func appendReplyArgs(args []string, store, jid, replyTo string) []string {
	if replyTo == "" {
		return args
	}
	args = append(args, "--reply-to", replyTo)
	if sender := quotedSender(store, jid, replyTo); sender != "" {
		args = append(args, "--reply-to-sender", sender)
	}
	return args
}

func sentMsgID(result map[string]any) string {
	for _, key := range []string{"id", "ID", "message_id", "MessageID", "msg_id"} {
		if v, ok := result[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
			if n, ok := v.(float64); ok {
				return fmt.Sprintf("%d", int64(n))
			}
		}
	}
	return ""
}

// rememberSentReply fills quoted_msg_id when wacli stored the send without the
// reply.
func rememberSentReply(store, jid string, result map[string]any, replyTo, text string) {
	if replyTo == "" {
		return
	}
	sibs := siblingJIDs(store, jid)
	sender := quotedSender(store, jid, replyTo)
	msgID := sentMsgID(result)
	con, err := sql.Open("sqlite3", "file:"+dbPath(store)+"?_busy_timeout=2000")
	if err != nil {
		return
	}
	defer con.Close()
	if msgID == "" && text != "" {
		args := append(toAny(sibs), text, text, text)
		row := con.QueryRow(fmt.Sprintf(`
			SELECT msg_id FROM messages
			WHERE chat_jid IN (%s) AND from_me = 1
			  AND IFNULL(quoted_msg_id, '') = ''
			  AND (IFNULL(text, '') = ? OR IFNULL(display_text, '') = ?
			       OR IFNULL(media_caption, '') = ?)
			ORDER BY ts DESC LIMIT 1`, placeholders(len(sibs))), args...)
		var id sql.NullString
		if row.Scan(&id) == nil {
			msgID = id.String
		}
	}
	if msgID == "" {
		return
	}
	con.Exec(
		fmt.Sprintf(`
		UPDATE messages
		   SET quoted_msg_id = ?,
		       quoted_sender_jid = COALESCE(NULLIF(quoted_sender_jid, ''), ?)
		 WHERE chat_jid IN (%s) AND msg_id = ?
		   AND IFNULL(quoted_msg_id, '') = ''`, placeholders(len(sibs))),
		append([]any{replyTo, sender}, append(toAny(sibs), msgID)...)...)
}

// --- sends ---

// syncActiveFn is stubbed in tests.
var syncActiveFn = syncActive

// withReceiptOwner skips wacli's 2s post-send receipt wait when the sync
// daemon is connected: its persistent connection handles retry receipts, so
// the send only needs to persist and answer. When sync is off, the send's
// own connection is the only one that can handle them — keep the wait.
func withReceiptOwner(args []string) []string {
	if syncActiveFn() {
		args = append(args, "--post-send-wait", "0")
	}
	return args
}

func sendText(store, jid, message, replyTo string, mentions []string) (map[string]any, *helperError) {
	text := strings.ReplaceAll(message, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return nil, fail("message is empty")
	}
	if len(text) > maxText {
		return nil, fail("message is longer than 4096 characters")
	}
	con, he := openDB(store)
	if he != nil {
		return nil, he
	}
	if _, he := requireChat(con, jid); he != nil {
		con.Close()
		return nil, he
	}
	con.Close()
	args := []string{"send", "text", "--to", jid, "--message", text}
	args = appendReplyArgs(args, store, jid, replyTo)
	validated, he := validateMentions(store, jid, mentions)
	if he != nil {
		return nil, he
	}
	for _, mention := range validated {
		args = append(args, "--mention", mention)
	}
	result, he := runWacliFn(withReceiptOwner(args), wacliOpts{store: store, timeout: 90 * time.Second, lockWait: "15s"})
	if he != nil {
		return nil, he
	}
	rememberSentReply(store, jid, result, replyTo, text)
	return result, nil
}

func sendFile(store, jid, pathStr, caption, asType, replyTo string) (map[string]any, *helperError) {
	path, he := validateFile(pathStr)
	if he != nil {
		return nil, he
	}
	con, he := openDB(store)
	if he != nil {
		return nil, he
	}
	if _, he := requireChat(con, jid); he != nil {
		con.Close()
		return nil, he
	}
	con.Close()
	if len(caption) > maxText {
		return nil, fail("caption is too long")
	}
	args := []string{"send", "file", "--to", jid, "--file", path, "--as", asType}
	if strings.TrimSpace(caption) != "" {
		args = append(args, "--caption", caption)
	}
	args = appendReplyArgs(args, store, jid, replyTo)
	result, he := runWacliFn(withReceiptOwner(args), wacliOpts{store: store, timeout: 180 * time.Second, lockWait: "15s"})
	if he != nil {
		return nil, he
	}
	rememberSentReply(store, jid, result, replyTo, caption)
	return result, nil
}

func sendVoice(store, jid, pathStr, replyTo string) (map[string]any, *helperError) {
	validated, he := validateFile(pathStr)
	if he != nil {
		return nil, he
	}
	path, he := transcodeVoice(validated)
	if he != nil {
		return nil, he
	}
	con, he := openDB(store)
	if he != nil {
		return nil, he
	}
	if _, he := requireChat(con, jid); he != nil {
		con.Close()
		return nil, he
	}
	con.Close()
	args := appendReplyArgs([]string{"send", "voice", "--to", jid, "--file", path}, store, jid, replyTo)
	result, he := runWacliFn(withReceiptOwner(args), wacliOpts{store: store, timeout: 120 * time.Second, lockWait: "15s"})
	if he != nil {
		return nil, he
	}
	rememberSentReply(store, jid, result, replyTo, "")
	if filepath.Dir(path) == filepath.Join(stateDir(), "voice-drafts") {
		_ = os.Remove(path)
	}
	return result, nil
}

func sendReact(store, jid, msgID, sender, emoji string) (map[string]any, *helperError) {
	sibs := siblingJIDs(store, jid)
	con, he := openDB(store)
	if he != nil {
		return nil, he
	}
	if _, he := requireChat(con, jid); he != nil {
		con.Close()
		return nil, he
	}
	var chatJ, senderJ sql.NullString
	var fromMe sql.NullInt64
	err := con.QueryRow(
		fmt.Sprintf("SELECT chat_jid, sender_jid, from_me FROM messages WHERE chat_jid IN (%s) AND msg_id = ?", placeholders(len(sibs))),
		append(toAny(sibs), msgID)...).Scan(&chatJ, &senderJ, &fromMe)
	con.Close()
	if err != nil {
		return nil, fail("that message is not in this chat")
	}
	args := []string{"send", "react", "--to", chatJ.String, "--id", msgID, "--reaction", emoji}
	target := strings.TrimSpace(firstNonEmpty(sender, senderJ.String))
	if target != "" && fromMe.Int64 == 0 {
		args = append(args, "--sender", target)
	}
	return runWacliFn(withReceiptOwner(args), wacliOpts{store: store, timeout: 60 * time.Second, lockWait: "15s"})
}

// --- media download ---

func existingMedia(local string) string {
	local = strings.TrimSpace(local)
	if local != "" && fileExists(local) {
		return local
	}
	return ""
}

func resolveDownload(dest string) (string, *helperError) {
	if st, err := os.Stat(dest); err == nil && !st.IsDir() && st.Size() > 0 {
		return dest, nil
	}
	if entries, err := os.ReadDir(dest); err == nil {
		var best string
		var bestTime time.Time
		for _, e := range entries {
			p := filepath.Join(dest, e.Name())
			if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Size() > 0 && st.ModTime().After(bestTime) {
				best, bestTime = p, st.ModTime()
			}
		}
		if best != "" {
			return best, nil
		}
	}
	base := dest
	_ = base
	// siblings: dest* glob in the parent dir
	siblings, _ := filepath.Glob(dest + "*")
	var best string
	var bestTime time.Time
	for _, p := range siblings {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Size() > 0 && st.ModTime().After(bestTime) {
			best, bestTime = p, st.ModTime()
		}
	}
	if best != "" {
		return best, nil
	}
	return "", fail("could not download that media")
}

func downloadMedia(store, jid, msgID string) (map[string]any, *helperError) {
	sibs := siblingJIDs(store, jid)
	con, he := openDB(store)
	if he != nil {
		return nil, he
	}
	if _, he := requireChat(con, jid); he != nil {
		con.Close()
		return nil, he
	}
	var rowID, chatJ, localPath, mimeType, mediaType, filename sql.NullString
	err := con.QueryRow(
		fmt.Sprintf("SELECT msg_id, chat_jid, local_path, mime_type, media_type, filename FROM messages WHERE chat_jid IN (%s) AND msg_id = ?", placeholders(len(sibs))),
		append(toAny(sibs), msgID)...).Scan(&rowID, &chatJ, &localPath, &mimeType, &mediaType, &filename)
	con.Close()
	if err != nil {
		return nil, fail("that message is not in this chat")
	}
	jid = chatJ.String // the sibling (@lid) the message actually lives in
	kind := mediaKind(mediaType.String, mimeType.String)
	local := firstNonEmpty(existingMedia(localPath.String), cachedMedia(jid, msgID))
	if local != "" {
		thumb := ""
		if kind == "video" {
			thumb = fileURL(videoThumb(local))
		}
		return map[string]any{
			"id": msgID, "localPath": local, "fileUrl": fileURL(local),
			"thumbUrl": thumb, "mimeType": mimeType.String, "kind": kind,
			"filename": filename.String,
		}, nil
	}
	folder := filepath.Join(stateDir(), "media")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return nil, fail("could not prepare the media folder")
	}
	_ = os.Chmod(folder, 0o700)
	dest := filepath.Join(folder, mediaKey(jid, msgID)+mediaExt(mimeType.String, filename.String, kind))
	// The transfer takes up to 180s. The daemon serves one request at a time, so
	// run it off the pipe and push the result when it lands; the GUI leaves the
	// message undownloaded until then.
	if pushable() {
		key := jid + "\x00" + msgID
		mediaMu.Lock()
		if !mediaFetching[key] {
			mediaFetching[key] = true
			go func() {
				defer func() {
					mediaMu.Lock()
					delete(mediaFetching, key)
					mediaMu.Unlock()
				}()
				data, he := fetchMedia(store, jid, msgID, dest, mimeType.String, kind, filename.String)
				if he != nil {
					data = map[string]any{"id": msgID, "error": he.msg}
				}
				push("downloaded", data)
			}()
		}
		mediaMu.Unlock()
		return map[string]any{"id": msgID, "pending": true}, nil
	}
	return fetchMedia(store, jid, msgID, dest, mimeType.String, kind, filename.String)
}

// mediaMu guards the in-flight set; one transfer per message at a time.
var (
	mediaMu       sync.Mutex
	mediaFetching = map[string]bool{}
)

// fetchMedia does the slow half of downloadMedia: pull the file and describe it.
func fetchMedia(store, jid, msgID, dest, mimeType, kind, filename string) (map[string]any, *helperError) {
	if _, he := runWacliFn([]string{"media", "download", "--chat", jid, "--id", msgID, "--output", dest},
		wacliOpts{store: store, timeout: 180 * time.Second, readonly: true}); he != nil {
		return nil, he
	}
	localPath2, he := resolveDownload(dest)
	if he != nil {
		return nil, he
	}
	_ = os.Chmod(localPath2, 0o600)
	rememberMedia(jid, msgID, localPath2)
	thumb := ""
	if kind == "video" {
		thumb = fileURL(videoThumb(localPath2))
	}
	name := filename
	if name == "" {
		name = filepath.Base(localPath2)
	}
	return map[string]any{
		"id": msgID, "localPath": localPath2, "fileUrl": fileURL(localPath2),
		"thumbUrl": thumb, "mimeType": mimeType, "kind": kind, "filename": name,
	}, nil
}

// --- sync / status / ack ---

func syncActive() bool {
	out, err := exec.Command("systemctl", "--user", "is-active", "wacli-sync.service").Output()
	return err == nil && strings.TrimSpace(string(out)) == "active"
}

func cmdSync(action string) (map[string]any, *helperError) {
	switch action {
	case "status":
		return map[string]any{"active": syncActive()}, nil
	case "start", "stop":
	default:
		return nil, fail("unknown sync action")
	}
	if err := exec.Command("systemctl", "--user", action, "wacli-sync.service").Run(); err != nil {
		return nil, fail("could not %s background sync", action)
	}
	return map[string]any{"active": syncActive()}, nil
}

func cmdStatus(store string) (map[string]any, *helperError) {
	prefs := loadPrefs()
	authenticated := false
	connected := false
	connectionState := ""
	if data, he := runWacliFn([]string{"doctor"}, wacliOpts{store: store, timeout: 10 * time.Second, readonly: true}); he == nil {
		authenticated, _ = data["authenticated"].(bool)
		connected, _ = data["connected"].(bool)
		connectionState, _ = data["connection_state"].(string)
	} else {
		authenticated = fileExists(dbPath(store))
	}
	chats := []map[string]any{}
	if fileExists(dbPath(store)) {
		if got, he := listChats(store, "", maxChats, false); he == nil {
			chats = got
		}
	}
	return map[string]any{
		"authenticated":   authenticated,
		"connected":       connected,
		"connectionState": connectionState,
		"storeDir":        store,
		"syncActive":      syncActive(),
		"receipts":        prefs.Receipts,
		"acks":            prefs.Acks,
		"unreadBadge":     badgeCount(chats, prefs),
		"chatCount":       len(chats),
	}, nil
}

// prefsMu serializes prefs.json read-modify-write: the daemon loop and the
// background mark-read goroutine both ack.
var prefsMu sync.Mutex

func cmdAck(jid string, ts int64) map[string]any {
	prefsMu.Lock()
	defer prefsMu.Unlock()
	prefs := loadPrefs()
	acks := map[string]int64{}
	for k, v := range prefs.Acks {
		acks[k] = v
	}
	acks[jid] = ts
	prefs.Acks = acks
	savePrefs(prefs)
	return map[string]any{"acks": acks}
}

func cmdReceipts(enabled bool) map[string]any {
	prefsMu.Lock()
	defer prefsMu.Unlock()
	prefs := loadPrefs()
	prefs.Receipts = enabled
	savePrefs(prefs)
	return map[string]any{"receipts": prefs.Receipts}
}

func cmdMarkRead(store, jid string, ts int64) (map[string]any, *helperError) {
	con, he := openDB(store)
	if he != nil {
		return nil, he
	}
	chat, he := requireChat(con, jid)
	con.Close()
	if he != nil {
		return nil, he
	}
	if ts < chat.lastMessageTs {
		ts = chat.lastMessageTs
	}
	prev := loadPrefs().Acks[jid]
	result := cmdAck(jid, ts) // persist locally first, before the wacli round-trip
	tell := func() *helperError {
		_, he := runWacliFn([]string{"chats", "mark-read", "--chat", jid},
			wacliOpts{store: store, timeout: 30 * time.Second, lockWait: "0s"})
		if he != nil {
			// Otherwise the ack sticks and the chat never retries. Only roll back
			// our own value: a newer ack for the same chat may have landed since.
			prefsMu.Lock()
			if loadPrefs().Acks[jid] == ts {
				prefsMu.Unlock()
				cmdAck(jid, prev)
			} else {
				prefsMu.Unlock()
			}
		}
		return he
	}
	if !pushable() {
		if he := tell(); he != nil {
			return nil, he
		}
		return result, nil
	}
	// Daemon: the WhatsApp round trip takes ~1.5s (30s when offline) and would
	// hold every chats/messages refresh behind it. Answer with the local ack now;
	// a failure pushes the rolled-back acks so the badge comes back.
	go func() {
		if tell() != nil {
			push("mark-read", map[string]any{"acks": loadPrefs().Acks})
		}
	}()
	return result, nil
}

func cmdVoicePath() map[string]any {
	folder := filepath.Join(stateDir(), "voice-drafts")
	_ = os.MkdirAll(folder, 0o700)
	_ = os.Chmod(folder, 0o700)
	path := filepath.Join(folder, randomHex(16)+".ogg")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		f.Close()
	}
	return map[string]any{"path": path, "fileUrl": fileURL(path)}
}

// --- avatars ---

func avatarDir() string {
	p := filepath.Join(stateDir(), "avatars")
	_ = os.MkdirAll(p, 0o700)
	_ = os.Chmod(p, 0o700)
	return p
}

func avatarFile(jid string) string {
	sum := sha256.Sum256([]byte(jid))
	return filepath.Join(avatarDir(), hex.EncodeToString(sum[:])+".jpg")
}

func loadAvatarIndex() map[string]any {
	data := readJSONMap(filepath.Join(stateDir(), "avatar-index.json"))
	if data == nil {
		return map[string]any{}
	}
	return data
}

func saveAvatarIndex(index map[string]any) {
	atomicWriteJSON(filepath.Join(stateDir(), "avatar-index.json"), index)
}

func cachedAvatarURL(jid string) string {
	path := avatarFile(jid)
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		return fileURL(path)
	}
	return ""
}

func pictureField(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := data[key].(string); ok && v != "" {
			return v
		}
		for actual, item := range data {
			if strings.EqualFold(actual, key) {
				if v, ok := item.(string); ok && v != "" {
					return v
				}
			}
		}
	}
	return ""
}

// avatarMu guards the on-disk index against the background fetchers below.
var (
	avatarMu       sync.Mutex
	avatarFetching = map[string]bool{}
)

// cmdAvatar answers from the on-disk cache and never blocks. A miss needs
// `profile picture-info`, which connects to WhatsApp and may take 20s; the
// daemon serves one request at a time, so doing that inline stalls every chat
// and message query queued behind it. Fetch in the background instead — the
// GUI does not cache an empty answer, so its next refresh picks the file up.
func cmdAvatar(store, jid string) (map[string]any, *helperError) {
	jid = strings.TrimSpace(jid)
	if !jidRe.MatchString(jid) {
		return nil, fail("chat target is not a valid JID")
	}
	if cached := cachedAvatarURL(jid); cached != "" {
		avatarMu.Lock()
		entry, _ := loadAvatarIndex()[jid].(map[string]any)
		avatarMu.Unlock()
		id, _ := entry["id"].(string)
		return map[string]any{"jid": jid, "localPath": avatarFile(jid), "fileUrl": cached, "id": id}, nil
	}
	avatarMu.Lock()
	if !avatarFetching[jid] {
		avatarFetching[jid] = true
		go func() {
			defer func() {
				avatarMu.Lock()
				delete(avatarFetching, jid)
				avatarMu.Unlock()
			}()
			fetchAvatar(store, jid)
		}()
	}
	avatarMu.Unlock()
	return map[string]any{"jid": jid, "localPath": "", "fileUrl": "", "id": ""}, nil
}

// fetchAvatar does the slow half: ask WhatsApp for the picture and cache it.
// Blocking, so only the background goroutine above and the daily refresh call it.
func fetchAvatar(store, jid string) {
	if cachedAvatarURL(jid) != "" {
		return // already on disk; the daily refresh only fills gaps
	}
	avatarMu.Lock()
	index := loadAvatarIndex()
	avatarMu.Unlock()
	entry, _ := index[jid].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	if failedAt, ok := entry["failedAt"].(float64); ok && failedAt > 0 && now()-int64(failedAt) < 600 {
		return
	}
	knownID, _ := entry["id"].(string)
	args := []string{"profile", "picture-info", "--jid", jid, "--preview"}
	if knownID != "" {
		args = append(args, "--existing-id", knownID)
	}
	data, he := runWacliFn(args, wacliOpts{store: store, timeout: 20 * time.Second, lockWait: "0s"})
	if he != nil {
		markAvatar(jid, map[string]any{"id": knownID, "failedAt": now()})
		return
	}
	url := pictureField(data, "url", "URL")
	picID := pictureField(data, "id", "ID")
	if url == "" {
		markAvatar(jid, map[string]any{"id": firstNonEmpty(picID, knownID), "failedAt": now()})
		return
	}
	rawData, _, _ := httpGet(url, browserUA, 1024*1024)
	if len(rawData) == 0 {
		markAvatar(jid, map[string]any{"id": firstNonEmpty(picID, knownID), "failedAt": now()})
		return
	}
	if err := os.WriteFile(avatarFile(jid), rawData, 0o600); err != nil {
		return
	}
	markAvatar(jid, map[string]any{"id": picID, "failedAt": 0})
}

func markAvatar(jid string, entry map[string]any) {
	avatarMu.Lock()
	defer avatarMu.Unlock()
	index := loadAvatarIndex()
	index[jid] = entry
	saveAvatarIndex(index)
}

// refreshBudget bounds one bulk pass so a cold cache cannot grind for an hour.
const refreshBudget = 30 * time.Second

func cmdRefreshAvatars(store string) (map[string]any, *helperError) {
	// picture-info needs the store lock, which a running wacli-sync daemon holds
	// permanently. Taking it means stopping sync — and every sync restart makes
	// WhatsApp replay the offline backlog, which is acked whether or not it gets
	// stored. That has already cost real messages. Avatars are cosmetic; the
	// message pipeline is not. So: fill the cache only when sync is already off,
	// never by stopping it.
	if syncActiveFn() {
		return map[string]any{"skipped": true, "reason": "sync running"}, nil
	}
	marker := filepath.Join(stateDir(), ".avatars-refresh")
	if st, err := os.Stat(marker); err == nil && now()-st.ModTime().Unix() < 86400 {
		return map[string]any{"skipped": true}, nil
	}
	// The GUI spawns this detached, so a relaunch can overlap the previous run.
	lock := filepath.Join(stateDir(), ".avatars-refresh.lock")
	if st, err := os.Stat(lock); err == nil && time.Since(st.ModTime()) < 10*time.Minute {
		return map[string]any{"skipped": true}, nil
	}
	if f, err := os.OpenFile(lock, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
		f.Close()
	}
	defer os.Remove(lock)

	avatarMu.Lock()
	index := loadAvatarIndex()
	for _, entry := range index {
		if e, ok := entry.(map[string]any); ok {
			e["failedAt"] = 0
		}
	}
	saveAvatarIndex(index)
	avatarMu.Unlock()

	chats, _ := listChats(store, "", maxChats, false)
	deadline := time.Now().Add(refreshBudget)
	done := 0
	for _, c := range chats {
		// Sync can come back mid-pass (the GUI has a toggle); yield the lock.
		if time.Now().After(deadline) || syncActiveFn() {
			break
		}
		jid, _ := c["jid"].(string)
		if jid != "" {
			fetchAvatar(store, jid)
			done++
		}
	}
	complete := done >= len(chats)
	if complete {
		// Incomplete runs leave the marker alone so the next launch continues.
		if f, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			f.Close()
		}
	}
	return map[string]any{"refreshed": done, "complete": complete}, nil
}

// --- media prune ---

// pruneMedia deletes downloaded media older than `days`. Marker-gated so it
// runs at most once per `every` regardless of how often it's called.
func pruneMedia(days int, every time.Duration) {
	marker := filepath.Join(stateDir(), ".media-pruned")
	nowTS := time.Now()
	if st, err := os.Stat(marker); err == nil && nowTS.Sub(st.ModTime()) < every {
		return
	}
	cutoff := nowTS.Add(-time.Duration(days) * 24 * time.Hour)
	folder := filepath.Join(stateDir(), "media")
	if entries, err := os.ReadDir(folder); err == nil {
		for _, e := range entries {
			p := filepath.Join(folder, e.Name())
			if st, err := os.Stat(p); err == nil && !st.IsDir() && st.ModTime().Before(cutoff) {
				_ = os.Remove(p)
			}
		}
	}
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		f.Close()
	}
}

var _ = json.Marshal
