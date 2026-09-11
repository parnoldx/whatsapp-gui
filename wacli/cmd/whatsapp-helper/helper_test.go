package main

// Port of tests/test_helper.py — the parity contract with the Python helper
// this binary replaces. Same schema fixture, same assertions.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const schema = `
CREATE TABLE chats (
  jid TEXT PRIMARY KEY, kind TEXT, name TEXT, last_message_ts INTEGER,
  archived INTEGER, pinned INTEGER, muted_until INTEGER, unread INTEGER, unread_count INTEGER
);
CREATE TABLE groups (
  jid TEXT PRIMARY KEY, name TEXT, owner_jid TEXT, created_ts INTEGER,
  is_parent INTEGER, linked_parent_jid TEXT, left_at INTEGER, updated_at INTEGER
);
CREATE TABLE group_participants (
  group_jid TEXT, user_jid TEXT, role TEXT, updated_at INTEGER,
  PRIMARY KEY (group_jid, user_jid)
);
CREATE TABLE contacts (
  jid TEXT PRIMARY KEY, phone TEXT, push_name TEXT, full_name TEXT,
  first_name TEXT, business_name TEXT, system_name TEXT, updated_at INTEGER
);
CREATE TABLE messages (
  rowid INTEGER PRIMARY KEY,
  chat_jid TEXT, chat_name TEXT, msg_id TEXT, sender_jid TEXT, sender_name TEXT,
  ts INTEGER, from_me INTEGER, text TEXT, display_text TEXT,
  quoted_msg_id TEXT, quoted_sender_jid TEXT, is_forwarded INTEGER,
  forwarding_score INTEGER, reaction_to_id TEXT, reaction_emoji TEXT,
  media_type TEXT, media_caption TEXT, filename TEXT, mime_type TEXT,
  direct_path TEXT, media_key BLOB, file_sha256 BLOB, file_enc_sha256 BLOB,
  file_length INTEGER, local_path TEXT, downloaded_at INTEGER,
  media_unavailable_at INTEGER, revoked INTEGER, deleted_for_me INTEGER,
  deleted_at INTEGER, deletion_reason TEXT, payload_purged_at INTEGER,
  edited INTEGER, edited_ts INTEGER, buttons TEXT,
  UNIQUE (chat_jid, msg_id)
);
CREATE TABLE poll_votes (
  chat_jid TEXT, poll_msg_id TEXT, voter_jid TEXT, vote_msg_id TEXT,
  selected_options_json TEXT, ts INTEGER,
  PRIMARY KEY (chat_jid, poll_msg_id, voter_jid)
);
CREATE TABLE message_locations (
  chat_jid TEXT, msg_id TEXT, latitude REAL, longitude REAL,
  name TEXT, address TEXT, is_live INTEGER
);
`

const insertMsg = `INSERT INTO messages (
     chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me,
     text, display_text, quoted_msg_id, quoted_sender_jid, is_forwarded,
     forwarding_score, reaction_to_id, reaction_emoji, media_type,
     media_caption, filename, mime_type, file_length, local_path,
     downloaded_at, media_unavailable_at, revoked, deleted_for_me,
     edited, buttons
   ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

type fixture struct {
	t     *testing.T
	store string
	state string
	db    string // wacli.db path
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{
		t:     t,
		store: filepath.Join(dir, "store"),
		state: filepath.Join(dir, "state"),
		db:    filepath.Join(dir, "store", "wacli.db"),
	}
	if err := os.MkdirAll(f.store, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.state, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WACLI_STORE_DIR", f.store)
	t.Setenv("PA_WHATSAPP_STATE", f.state)
	t.Setenv("WACLI_BIN", "/usr/bin/false")
	syncActiveFn = func() bool { return false } // keep tests off the host's systemd state
	t.Cleanup(func() { syncActiveFn = syncActive })
	con := openrw(t, f.db)
	execSQL(t, con, schema)
	execSQL(t, con, `INSERT INTO chats VALUES ('111@s.whatsapp.net','dm','Ada',100,0,1,0,1,2)`)
	execSQL(t, con, `INSERT INTO chats VALUES ('222@g.us','group','Crew',200,0,0,-1,1,3)`)
	execSQL(t, con, `INSERT INTO chats VALUES ('333@newsletter','unknown','News',300,0,0,0,0,9)`)
	execSQL(t, con, `INSERT INTO chats VALUES ('444@g.us','group','Archived',50,1,0,0,0,1)`)
	execSQL(t, con, `INSERT INTO groups VALUES ('222@g.us','Crew','111@s.whatsapp.net',1,0,'',0,1)`)
	execSQL(t, con, `INSERT INTO group_participants VALUES ('222@g.us','555@s.whatsapp.net','admin',1)`)
	execSQL(t, con, `INSERT INTO contacts VALUES ('555@s.whatsapp.net','555','Sam','Sam Stone','Sam','','',1)`)
	mustExec(t, con, insertMsg, []any{
		"111@s.whatsapp.net", "Ada", "m1", "111@s.whatsapp.net", "Ada", 100, 0,
		"hello https://example.com", "hello https://example.com", "", "", 0,
		0, "", "", "", "", "", "", 0, "", 0, 0, 0, 0, 0, "",
	})
	mustExec(t, con, insertMsg, []any{
		"111@s.whatsapp.net", "Ada", "m2", "me", "Me", 90, 1,
		"gone", "gone", "", "", 0, 0, "", "", "", "", "", "", 0, "",
		0, 0, 0, 1, 0, "",
	})
	con.Close()
	return f
}

// resetLidMap clears the process-wide lid map between fixtures.
func resetLidMap() { lidMapCache, selfJIDCache = nil, nil }

func runHelper(t *testing.T, argv []string) (map[string]any, *helperError) {
	t.Helper()
	resetLidMap()
	return dispatch(argv)
}

func chatsPayload(t *testing.T, data map[string]any) []map[string]any {
	t.Helper()
	raw, _ := json.Marshal(data["chats"])
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("chats: %v", err)
	}
	return out
}

func messagesPayload(t *testing.T, data map[string]any) []map[string]any {
	t.Helper()
	raw, _ := json.Marshal(data["messages"])
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("messages: %v", err)
	}
	return out
}

func find(t *testing.T, list []map[string]any, key string, id any) map[string]any {
	t.Helper()
	for _, item := range list {
		if item[key] == id {
			return item
		}
	}
	t.Fatalf("no %v == %v in list", key, id)
	return nil
}

// --- tests ---

func TestListsOnlyDmAndStandaloneGroups(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	_ = f
	data, err := runHelper(t, []string{"chats", "--limit", "50"})
	if err != nil {
		t.Fatal(err)
	}
	chats := chatsPayload(t, data)
	kinds, names := map[string]bool{}, map[string]bool{}
	var crew, ada map[string]any
	for _, c := range chats {
		kinds[c["kind"].(string)] = true
		names[c["name"].(string)] = true
		if c["kind"] == "group" {
			crew = c
		} else {
			ada = c
		}
	}
	if len(kinds) != 2 || !kinds["dm"] || !kinds["group"] {
		t.Errorf("kinds = %v", kinds)
	}
	if !names["Ada"] || !names["Crew"] || names["News"] || names["Archived"] {
		t.Errorf("names = %v", names)
	}
	if crew["muted"] != true {
		t.Errorf("crew muted = %v", crew["muted"])
	}
	if asF(ada["unreadCount"]) != 2 {
		t.Errorf("ada unread = %v", ada["unreadCount"])
	}
	if ada["preview"] != "hello https://example.com" {
		t.Errorf("ada preview = %v", ada["preview"])
	}
	if _, ok := data["syncActive"].(bool); !ok {
		t.Errorf("syncActive missing: %v", data["syncActive"])
	}
}

func TestSearchFiltersWithoutTouchingWacli(t *testing.T) {
	resetLidMap()
	newFixture(t)
	data, err := runHelper(t, []string{"chats", "--query", "crew"})
	if err != nil {
		t.Fatal(err)
	}
	chats := chatsPayload(t, data)
	if len(chats) != 1 || chats[0]["name"] != "Crew" {
		t.Errorf("chats = %v", chats)
	}
}

func TestUnsupportedStubShowsButAlbumStubDrops(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	con := openrw(t, f.db)
	mustExec(t, con, `INSERT INTO chats VALUES ('666@s.whatsapp.net','dm','PayPal',300,0,0,0,0,1)`, nil)
	msg := func(mid string, ts int64, display, media string) {
		mustExec(t, con, insertMsg, []any{
			"666@s.whatsapp.net", "PayPal", mid, "666@s.whatsapp.net", "PayPal",
			ts, 0, "", display, "", "", 0, 0, "", "", media, "", "", "", 0, "",
			0, 0, 0, 0, 0, "",
		})
	}
	msg("t1", 300, "(message)", "")
	msg("a1", 200, "(message)", "")
	msg("a2", 201, "", "image")
	con.Close()
	data, err := runHelper(t, []string{"messages", "--chat", "666@s.whatsapp.net"})
	if err != nil {
		t.Fatal(err)
	}
	messages := messagesPayload(t, data)
	var ids []string
	for _, m := range messages {
		ids = append(ids, m["id"].(string))
	}
	if !reflect.DeepEqual(ids, []string{"a2", "t1"}) {
		t.Errorf("ids = %v", ids)
	}
	stub := find(t, messages, "id", "t1")
	if stub["text"] != "Unsupported message" {
		t.Errorf("stub text = %v", stub["text"])
	}
	if _, ok := messages[0]["album"]; ok {
		t.Errorf("messages[0] has album")
	}
}

func TestGroupEmptyStubIsHidden(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	con := openrw(t, f.db)
	mustExec(t, con, insertMsg, []any{
		"222@g.us", "Crew", "real1", "555@s.whatsapp.net", "Sam", 200, 0,
		"hello crew", "hello crew", "", "", 0, 0, "", "", "", "", "", "", 0, "",
		0, 0, 0, 0, 0, "",
	})
	mustExec(t, con, insertMsg, []any{
		"222@g.us", "Crew", "edit-stub", "555@s.whatsapp.net", "Sam", 210, 0,
		"", "(message)", "", "", 0, 0, "", "", "", "", "", "", 0, "",
		0, 0, 0, 0, 0, "",
	})
	con.Close()
	messages, err := listMessages(f.store, "222@g.us", 80, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0]["id"] != "real1" {
		t.Errorf("messages = %v", messages)
	}
	chats, err := listChats(f.store, "", 80, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chats {
		if c["jid"] == "222@g.us" && c["preview"] != "hello crew" {
			t.Errorf("preview = %v", c["preview"])
		}
	}
}

func TestAlbumLabelFoldsPhotosIntoOneMessage(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	con := openrw(t, f.db)
	mustExec(t, con, `INSERT INTO chats VALUES ('777@s.whatsapp.net','dm','Fabian',400,0,0,0,0,1)`, nil)
	msg := func(mid string, ts int64, text, display, media, caption, quoted string) {
		mustExec(t, con, insertMsg, []any{
			"777@s.whatsapp.net", "Fabian", mid, "777@s.whatsapp.net", "Fabian",
			ts, 0, text, display, quoted, "", 0, 0, "", "", media, caption, "", "",
			0, "", 0, 0, 0, 0, 0, "",
		})
	}
	msg("s1", 400, "[Album: 2 images]", "[Album: 2 images]", "", "", "")
	msg("p1", 400, "", "Sent image", "image", "Haha liebe den Twist am Ende.", "")
	msg("p2", 400, "", "Sent image", "image", "", "")
	msg("r1", 410, "Welcher Twist", "> [Album: 2 images]\nWelcher Twist", "", "", "s1")
	msg("d1", 350, "", "Sent image", "image", "", "")
	msg("d2", 372, "", "Sent image", "image", "", "")
	con.Close()
	data, err := runHelper(t, []string{"messages", "--chat", "777@s.whatsapp.net"})
	if err != nil {
		t.Fatal(err)
	}
	messages := messagesPayload(t, data)
	var ids []string
	for _, m := range messages {
		ids = append(ids, m["id"].(string))
	}
	if !reflect.DeepEqual(ids, []string{"d1", "d2", "p1", "r1"}) {
		t.Fatalf("ids = %v", ids)
	}
	album := find(t, messages, "id", "p1")
	albumList, ok := album["album"].([]any)
	if !ok || len(albumList) != 2 {
		t.Fatalf("album = %v", album["album"])
	}
	piece0 := albumList[0].(map[string]any)
	piece1 := albumList[1].(map[string]any)
	if piece0["id"] != "p1" || piece1["id"] != "p2" {
		t.Errorf("album ids = %v %v", piece0["id"], piece1["id"])
	}
	if album["albumId"] != "s1" {
		t.Errorf("albumId = %v", album["albumId"])
	}
	if album["caption"] != "Haha liebe den Twist am Ende." || album["text"] != "Haha liebe den Twist am Ende." {
		t.Errorf("caption/text = %v / %v", album["caption"], album["text"])
	}
	if album["kind"] != "image" {
		t.Errorf("kind = %v", album["kind"])
	}
	if _, ok := album["_album"]; ok {
		t.Errorf("_album leaked")
	}
	if _, ok := messages[0]["album"]; ok {
		t.Errorf("messages[0] has album")
	}
	if _, ok := messages[1]["album"]; ok {
		t.Errorf("messages[1] has album")
	}
	reply := find(t, messages, "id", "r1")
	if reply["quotedId"] != "s1" {
		t.Errorf("quotedId = %v", reply["quotedId"])
	}
	if reply["quotedText"] != "Haha liebe den Twist am Ende." {
		t.Errorf("quotedText = %v", reply["quotedText"])
	}
}

func TestMessagesSkipDeletedAndExposeLinks(t *testing.T) {
	resetLidMap()
	newFixture(t)
	data, err := runHelper(t, []string{"messages", "--chat", "111@s.whatsapp.net"})
	if err != nil {
		t.Fatal(err)
	}
	messages := messagesPayload(t, data)
	if len(messages) != 1 {
		t.Fatalf("len = %d", len(messages))
	}
	if messages[0]["id"] != "m1" {
		t.Errorf("id = %v", messages[0]["id"])
	}
	if !strings.Contains(messages[0]["text"].(string), "https://example.com") {
		t.Errorf("text = %v", messages[0]["text"])
	}
	if messages[0]["kind"] != "text" {
		t.Errorf("kind = %v", messages[0]["kind"])
	}
}

func TestRejectsUnknownKindAndBadJid(t *testing.T) {
	resetLidMap()
	newFixture(t)
	if _, err := runHelper(t, []string{"messages", "--chat", "333@newsletter"}); err == nil {
		t.Error("newsletter chat accepted")
	}
	if _, err := runHelper(t, []string{"messages", "--chat", "not-a-jid"}); err == nil {
		t.Error("bad jid accepted")
	}
}

func TestDaemonSurvivesBadInputAndAnswersEachLine(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	input := `["chats","--limit","5"]` + "\n" +
		"not json\n" +
		`["bogus"]` + "\n" +
		`["chats","--limit","5"]` + "\n"
	out := runDaemonScript(t, f, input)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines: %q", len(lines), out)
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil || first["ok"] != true {
		t.Errorf("line0 = %v", lines[0])
	}
	var second, third map[string]any
	json.Unmarshal([]byte(lines[1]), &second)
	json.Unmarshal([]byte(lines[2]), &third)
	if second["ok"] != false || second["error"] != "bad command" {
		t.Errorf("line1 = %v", second)
	}
	if third["ok"] != false || third["error"] != "bad command" {
		t.Errorf("line2 = %v", third)
	}
	var fourth map[string]any
	if err := json.Unmarshal([]byte(lines[3]), &fourth); err != nil || fourth["ok"] != true {
		t.Errorf("line3 = %v", lines[3])
	}
}

// runDaemonScript feeds input lines through runDaemon with piped stdio.
func runDaemonScript(t *testing.T, f *fixture, input string) string {
	t.Helper()
	resetLidMap()
	inFile := filepath.Join(t.TempDir(), "in")
	if err := os.WriteFile(inFile, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(inFile)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	savedIn, savedOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = in, w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		done <- buf.String()
	}()
	code := runDaemon()
	os.Stdin, os.Stdout = savedIn, savedOut
	w.Close()
	if code != 0 {
		t.Errorf("daemon code = %d", code)
	}
	return <-done
}

func TestParticipantsResolveContactNames(t *testing.T) {
	resetLidMap()
	newFixture(t)
	data, err := runHelper(t, []string{"participants", "--chat", "222@g.us"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(data["participants"])
	var people []map[string]any
	json.Unmarshal(raw, &people)
	if len(people) == 0 || people[0]["name"] != "Sam Stone" || people[0]["jid"] != "555@s.whatsapp.net" {
		t.Errorf("people = %v", people)
	}
}

func TestMentionsMustBeGroupMembers(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	if _, err := validateMentions(f.store, "222@g.us", []string{"999@s.whatsapp.net"}); err == nil {
		t.Error("non-member mention accepted")
	}
	got, err := validateMentions(f.store, "222@g.us", []string{"555@s.whatsapp.net"})
	if err != nil || !reflect.DeepEqual(got, []string{"555@s.whatsapp.net"}) {
		t.Errorf("got = %v err = %v", got, err)
	}
}

func TestMentionsInTextShowContactName(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	scon := openrw(t, f.store+"/session.db")
	execSQL(t, scon, "CREATE TABLE whatsmeow_lid_map (lid TEXT PRIMARY KEY, pn TEXT UNIQUE NOT NULL)")
	mustExec(t, scon, "INSERT INTO whatsmeow_lid_map VALUES ('148515808395375','555')", nil)
	scon.Close()
	con := openrw(t, f.db)
	mustExec(t, con, "UPDATE contacts SET phone = '55512345678' WHERE jid = '555@s.whatsapp.net'", nil)
	mustExec(t, con, insertMsg, []any{
		"222@g.us", "Crew", "men-lid", "111@s.whatsapp.net", "Denis", 210, 0,
		"@148515808395375 haha", "@148515808395375 haha", "", "", 0,
		0, "", "", "", "", "", "", 0, "", 0, 0, 0, 0, 0, "",
	})
	mustExec(t, con, insertMsg, []any{
		"222@g.us", "Crew", "men-pn", "111@s.whatsapp.net", "Denis", 211, 0,
		"@55512345678 also", "@55512345678 also", "men-lid", "", 0,
		0, "", "", "", "", "", "", 0, "", 0, 0, 0, 0, 0, "",
	})
	mustExec(t, con, insertMsg, []any{
		"222@g.us", "Crew", "men-unknown", "111@s.whatsapp.net", "Denis", 209, 0,
		"@999999999 hello", "@999999999 hello", "", "", 0,
		0, "", "", "", "", "", "", 0, "", 0, 0, 0, 0, 0, "",
	})
	con.Close()

	if got := applyMentionNames("@148515808395375 haha", map[string]string{"148515808395375": "Sam Stone"}); got != "@Sam Stone haha" {
		t.Errorf("apply = %q", got)
	}
	msgs, err := listMessages(f.store, "222@g.us", 80, 0)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]any{}
	for _, m := range msgs {
		byID[m["id"].(string)] = m
	}
	if byID["men-lid"]["text"] != "@Sam Stone haha" {
		t.Errorf("men-lid text = %q", byID["men-lid"]["text"])
	}
	if byID["men-pn"]["text"] != "@Sam Stone also" {
		t.Errorf("men-pn text = %q", byID["men-pn"]["text"])
	}
	if byID["men-pn"]["quotedText"] != "@Sam Stone haha" {
		t.Errorf("men-pn quoted = %q", byID["men-pn"]["quotedText"])
	}
	if byID["men-unknown"]["text"] != "@999999999 hello" {
		t.Errorf("men-unknown text = %q", byID["men-unknown"]["text"])
	}
	chats, err := listChats(f.store, "", 80, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chats {
		if c["jid"] == "222@g.us" && c["preview"] != "@Sam Stone also" {
			t.Errorf("preview = %q", c["preview"])
		}
	}
}

func TestAckAndBadge(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	chats, err := listChats(f.store, "", 80, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := badgeCount(chats, prefs{Acks: map[string]int64{}}); got != 1 { // Ada unread, Crew muted
		t.Errorf("badge = %d", got)
	}
	if _, err := runHelper(t, []string{"ack", "--chat", "111@s.whatsapp.net", "--ts", "100"}); err != nil {
		t.Fatal(err)
	}
	p := loadPrefs()
	if p.Acks["111@s.whatsapp.net"] != 100 {
		t.Errorf("ack = %v", p.Acks)
	}
	if got := badgeCount(chats, p); got != 0 {
		t.Errorf("badge after ack = %d", got)
	}
}

func TestSendSkipsReceiptWaitWhenSyncActive(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	var calls [][]string
	saved := runWacliFn
	runWacliFn = func(args []string, opts wacliOpts) (map[string]any, *helperError) {
		calls = append(calls, args)
		return map[string]any{"id": "x"}, nil
	}
	defer func() { runWacliFn = saved }()
	skipped := func(args []string) bool {
		return indexOf("--post-send-wait", args) >= 0 && args[indexOf("--post-send-wait", args)+1] == "0"
	}
	syncActiveFn = func() bool { return true }
	if _, err := sendText(f.store, "111@s.whatsapp.net", "hi", "", nil); err != nil {
		t.Fatal(err)
	}
	if !skipped(calls[0]) {
		t.Errorf("post-send wait not skipped while sync daemon is active: %v", calls[0])
	}
	syncActiveFn = func() bool { return false }
	if _, err := sendText(f.store, "111@s.whatsapp.net", "hi", "", nil); err != nil {
		t.Fatal(err)
	}
	if skipped(calls[len(calls)-1]) {
		t.Error("post-send wait skipped with sync daemon off — retry receipts would be lost")
	}
}

func TestFileMustBeAbsoluteRegular(t *testing.T) {
	newFixture(t)
	if _, err := validateFile("relative.png"); err == nil {
		t.Error("relative accepted")
	}
	if _, err := validateFile("/no/such/file.bin"); err == nil {
		t.Error("missing accepted")
	}
}

func TestOggOpusDetection(t *testing.T) {
	newFixture(t)
	path := t.TempDir() + "/note.ogg"
	if err := os.WriteFile(path, append([]byte("OggS"), append(make([]byte, 20), []byte("OpusHead")...)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if !isOggOpus(path) {
		t.Error("valid ogg/opus rejected")
	}
	if err := os.WriteFile(path, []byte("not audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	if isOggOpus(path) {
		t.Error("junk accepted")
	}
}

func TestMediaKind(t *testing.T) {
	newFixture(t)
	if got := mediaKind("image", "image/jpeg"); got != "image" {
		t.Errorf("image = %q", got)
	}
	if got := mediaKind("audio", "audio/ogg; codecs=opus"); got != "voice" {
		t.Errorf("audio = %q", got)
	}
	if got := mediaKind("video", "video/mp4"); got != "video" {
		t.Errorf("video = %q", got)
	}
	if got := mediaKind("", ""); got != "text" {
		t.Errorf("empty = %q", got)
	}
}

func TestLockErrorIsHuman(t *testing.T) {
	newFixture(t)
	msg := humanizeWacliError("lock after 15s: store is locked (another wacli is running?)")
	if msg != "WhatsApp is busy syncing. Try again in a moment." {
		t.Errorf("msg = %q", msg)
	}
	if strings.Contains(strings.ToLower(msg), "success") {
		t.Errorf("leaks internals: %q", msg)
	}
}

func TestDownloadUsesReadonlyOutput(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	con := openrw(t, f.db)
	mustExec(t, con, insertMsg, []any{
		"111@s.whatsapp.net", "Ada", "img1", "111@s.whatsapp.net", "Ada", 110, 0,
		"", "", "", "", 0, 0, "", "", "image", "", "pic.jpg", "image/jpeg",
		12, "", 0, 0, 0, 0, 0, "",
	})
	con.Close()
	var calls [][]string
	var callOpts []wacliOpts
	saved := runWacliFn
	runWacliFn = func(args []string, opts wacliOpts) (map[string]any, *helperError) {
		calls = append(calls, args)
		callOpts = append(callOpts, opts)
		for i, a := range args {
			if a == "--output" {
				if err := os.WriteFile(args[i+1], []byte("\xff\xd8\xff\xd9"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		return map[string]any{}, nil
	}
	defer func() { runWacliFn = saved }()
	result, err := downloadMedia(f.store, "111@s.whatsapp.net", "img1")
	if err != nil {
		t.Fatal(err)
	}
	// --read-only flag set, --lock-wait absent
	if !callOpts[0].readonly {
		t.Error("download not readonly")
	}
	if strings.Contains(strings.Join(calls[0], " "), "--lock-wait") {
		t.Error("lock-wait present in download call")
	}
	if !strings.Contains(strings.Join(calls[0], " "), "--output") {
		t.Error("no --output")
	}
	if _, statErr := os.Stat(result["localPath"].(string)); statErr != nil {
		t.Errorf("localPath: %v", statErr)
	}
	if !strings.HasPrefix(result["fileUrl"].(string), "file:") {
		t.Errorf("fileUrl = %v", result["fileUrl"])
	}
}

func indexOf(needle string, hay []string) int {
	for i, s := range hay {
		if s == needle {
			return i
		}
	}
	return -1
}

func TestLinkifyJsContractViaModel(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	chats, err := listChats(f.store, "", 80, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chats {
		if c["name"] == "Ada" && !strings.Contains(c["preview"].(string), "https://example.com") {
			t.Errorf("preview = %v", c["preview"])
		}
	}
}

func TestNamesPreferContactsAndGroupTable(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	con := openrw(t, f.db)
	mustExec(t, con, `INSERT INTO chats VALUES ('777@s.whatsapp.net','dm','777@s.whatsapp.net',80,0,0,0,0,1)`, nil)
	mustExec(t, con, `INSERT INTO contacts VALUES ('777@s.whatsapp.net','777','Kev','Kevin Bernthaler','Kevin','','',1)`, nil)
	mustExec(t, con, `INSERT INTO chats VALUES ('666@g.us','group','666@g.us',70,0,0,0,0,0)`, nil)
	mustExec(t, con, `INSERT INTO groups VALUES ('666@g.us','Foodpornisten','',1,0,'',0,1)`, nil)
	con.Close()
	chats, err := listChats(f.store, "", 80, true)
	if err != nil {
		t.Fatal(err)
	}
	byJID := map[string]map[string]any{}
	for _, c := range chats {
		byJID[c["jid"].(string)] = c
	}
	if byJID["777@s.whatsapp.net"]["name"] != "Kevin Bernthaler" {
		t.Errorf("dm name = %v", byJID["777@s.whatsapp.net"]["name"])
	}
	if byJID["666@g.us"]["name"] != "Foodpornisten" {
		t.Errorf("group name = %v", byJID["666@g.us"]["name"])
	}
}

func TestSenderNameFallsBackToContacts(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	con := openrw(t, f.db)
	mustExec(t, con, insertMsg, []any{
		"222@g.us", "Crew", "m3", "555@s.whatsapp.net", "", 80, 0,
		"yo", "yo", "", "", 0, 0, "", "", "", "", "", "", 0, "",
		0, 0, 0, 0, 0, "",
	})
	con.Close()
	messages, err := listMessages(f.store, "222@g.us", 80, 0)
	if err != nil {
		t.Fatal(err)
	}
	if messages[0]["senderName"] != "Sam Stone" {
		t.Errorf("senderName = %v", messages[0]["senderName"])
	}
}

func TestSentImagePlaceholderIsNotACaption(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	con := openrw(t, f.db)
	mustExec(t, con, insertMsg, []any{
		"111@s.whatsapp.net", "Ada", "img2", "111@s.whatsapp.net", "Ada", 120, 0,
		"", "Sent image", "", "", 0, 0, "", "", "image", "", "pic.jpg", "image/jpeg",
		12, "", 0, 0, 0, 0, 0, "",
	})
	con.Close()
	messages, err := listMessages(f.store, "111@s.whatsapp.net", 80, 0)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]any{}
	for _, m := range messages {
		byID[m["id"].(string)] = m
	}
	if byID["img2"]["kind"] != "image" || byID["img2"]["text"] != "" || byID["img2"]["caption"] != "" {
		t.Errorf("img2 = %v", byID["img2"])
	}
	chats, err := listChats(f.store, "", 80, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chats {
		if c["jid"] == "111@s.whatsapp.net" && c["preview"] != "Photo" {
			t.Errorf("preview = %v", c["preview"])
		}
	}
}

func TestAckClearsListedUnread(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	if _, err := runHelper(t, []string{"ack", "--chat", "111@s.whatsapp.net", "--ts", "100"}); err != nil {
		t.Fatal(err)
	}
	chats, err := listChats(f.store, "", 80, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chats {
		if c["jid"] == "111@s.whatsapp.net" && asF(c["unreadCount"]) != 0 {
			t.Errorf("unread = %v", c["unreadCount"])
		}
	}
}

func TestReadOnOtherDeviceClearsUnread(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	con := openrw(t, f.db)
	mustExec(t, con, "UPDATE chats SET unread = 0, unread_count = 7 WHERE jid = '111@s.whatsapp.net'", nil)
	con.Close()
	chats, err := listChats(f.store, "", 80, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chats {
		if c["jid"] == "111@s.whatsapp.net" && asF(c["unreadCount"]) != 0 {
			t.Errorf("unread = %v", c["unreadCount"])
		}
	}
}

func TestUnreadCountsOnlyMessagesAfterAck(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	con := openrw(t, f.db)
	mustExec(t, con, "UPDATE chats SET unread_count = 36, last_message_ts = 100 WHERE jid = '111@s.whatsapp.net'", nil)
	con.Close()
	if _, err := runHelper(t, []string{"ack", "--chat", "111@s.whatsapp.net", "--ts", "90"}); err != nil {
		t.Fatal(err)
	}
	chats, err := listChats(f.store, "", 80, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chats {
		if c["jid"] == "111@s.whatsapp.net" && asF(c["unreadCount"]) != 1 {
			t.Errorf("unread = %v", c["unreadCount"])
		}
	}
}

func TestMarkReadUsesChatFlag(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	var calls [][]string
	lockWaits = nil
	saved := runWacliFn
	runWacliFn = func(args []string, opts wacliOpts) (map[string]any, *helperError) {
		calls = append(calls, args)
		lockWaits = append(lockWaits, opts.lockWait)
		return map[string]any{}, nil
	}
	defer func() { runWacliFn = saved }()
	if _, err := cmdMarkRead(f.store, "111@s.whatsapp.net", 0); err != nil {
		t.Fatal(err)
	}
	args := calls[0]
	if args[0] != "chats" || args[1] != "mark-read" {
		t.Errorf("args = %v", args)
	}
	if indexOf("--chat", args) < 0 {
		t.Error("no --chat")
	}
	if indexOf("--jid", args) >= 0 {
		t.Error("used --jid")
	}
	if len(lockWaits) != 1 || lockWaits[0] != "0s" {
		t.Errorf("lockWaits = %v", lockWaits)
	}
}

var lockWaits []string

func TestInstagramReelGetsALinkPreview(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	con := openrw(t, f.db)
	mustExec(t, con, insertMsg, []any{
		"111@s.whatsapp.net", "Ada", "reel1", "111@s.whatsapp.net", "Ada", 130, 0,
		"https://www.instagram.com/reel/Dc66-wqjore/?stkn=abc",
		"https://www.instagram.com/reel/Dc66-wqjore/?stkn=abc",
		"", "", 0, 0, "", "", "", "", "", "", 0, "",
		0, 0, 0, 0, 0, "",
	})
	con.Close()
	messages, err := listMessages(f.store, "111@s.whatsapp.net", 80, 0)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]any{}
	for _, m := range messages {
		byID[m["id"].(string)] = m
	}
	preview := byID["reel1"]["linkPreview"].(map[string]any)
	if preview["site"] != "Instagram" || preview["label"] != "Reel" {
		t.Errorf("preview = %v", preview)
	}
	if !strings.Contains(preview["url"].(string), "instagram.com/reel/") {
		t.Errorf("url = %v", preview["url"])
	}
	if preview["embedUrl"] != "https://www.instagram.com/reel/Dc66-wqjore/embed/" {
		t.Errorf("embedUrl = %v", preview["embedUrl"])
	}
}

func TestDescribeLinkBuildsOfficialEmbedUrls(t *testing.T) {
	newFixture(t)
	ig := describeLink("https://www.instagram.com/p/Dc9x-rfASm0/?img_index=1")
	if ig.embedURL != "https://www.instagram.com/p/Dc9x-rfASm0/embed/" {
		t.Errorf("ig = %q", ig.embedURL)
	}
	tt := describeLink("https://www.tiktok.com/@_omarreacts/video/7662159610396052757?_r=1")
	if tt.embedURL != "https://www.tiktok.com/embed/v2/7662159610396052757" {
		t.Errorf("tt = %q", tt.embedURL)
	}
	if got := describeLink("https://vm.tiktok.com/ZGdxcYD6r/").embedURL; got != "" {
		t.Errorf("vm.tt = %q", got)
	}
	if got := describeLink("https://www.tiktok.com/@x/photo/7662159610396052757").embedURL; got != "https://www.tiktok.com/embed/v2/7662159610396052757" {
		t.Errorf("photo = %q", got)
	}
	yt := describeLink("https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=4")
	if yt.embedURL != "https://www.youtube.com/embed/dQw4w9WgXcQ" {
		t.Errorf("yt = %q", yt.embedURL)
	}
	fb := describeLink("https://www.facebook.com/share/r/18KkbJYRmm/")
	if fb.embedURL != "" {
		t.Errorf("fb share = %q", fb.embedURL)
	}
	reel := describeLink("https://www.facebook.com/reel/2257374628373907/?fs=e")
	if reel.embedURL != "https://www.facebook.com/plugins/video.php?href=https%3A%2F%2Fwww.facebook.com%2Freel%2F2257374628373907%2F&show_text=false" {
		t.Errorf("reel = %q", reel.embedURL)
	}
	tweet := describeLink("https://x.com/BarackObama/status/266031293945503744?s=20")
	if tweet.embedURL != "https://platform.twitter.com/embed/Tweet.html?id=266031293945503744&dnt=true&theme=dark" {
		t.Errorf("tweet = %q", tweet.embedURL)
	}
	video := describeLink("https://x.com/TheCinesthetic/status/2096346129726345259/video/1?s=48")
	if video.embedURL != "https://twitter.com/i/videos/tweet/2096346129726345259" {
		t.Errorf("video = %q", video.embedURL)
	}
	if got := describeLink("https://example.com/x").embedURL; got != "" {
		t.Errorf("example = %q", got)
	}
}

func TestPruneMediaDropsOnlyOldFiles(t *testing.T) {
	newFixture(t)
	media := filepath.Join(stateDir(), "media")
	if err := os.MkdirAll(media, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(media, "old.jpg")
	new := filepath.Join(media, "new.jpg")
	os.WriteFile(old, []byte("x"), 0o600)
	os.WriteFile(new, []byte("x"), 0o600)
	stale := time.Now().Add(-8 * 24 * time.Hour)
	os.Chtimes(old, stale, stale)
	pruneMedia(7, 0)
	if fileExists(old) {
		t.Error("old file kept")
	}
	if !fileExists(new) {
		t.Error("new file dropped")
	}
	// marker gate: a second call within `every` is a no-op even for old files
	os.Chtimes(new, stale, stale)
	pruneMedia(7, 6*time.Hour)
	if !fileExists(new) {
		t.Error("marker gate failed")
	}
}

func TestReactionsAggregateLatestPerSender(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	con := openrw(t, f.db)
	rows := []([]any){
		{"r1", "555@s.whatsapp.net", "Sam", 0, 200, "m1", "😂"},
		{"r2", "666@s.whatsapp.net", "Kim", 0, 201, "m1", "😂"},
		{"r3", "me", "", 1, 202, "m1", "❤️"},
		{"r4", "555@s.whatsapp.net", "Sam", 0, 210, "m1", "❤️"},
		{"r5", "666@s.whatsapp.net", "Kim", 0, 220, "m1", ""},
	}
	for _, row := range rows {
		mustExec(t, con, insertMsg, []any{
			"111@s.whatsapp.net", "Ada", row[0], row[1], row[2], row[4], row[3],
			"", "", "", "", 0, 0, row[5], row[6], "", "", "", "", 0, "",
			0, 0, 0, 0, 0, "",
		})
	}
	con.Close()
	messages, err := listMessages(f.store, "111@s.whatsapp.net", 80, 0)
	if err != nil {
		t.Fatal(err)
	}
	m1 := find(t, messages, "id", "m1")
	raw, _ := json.Marshal(m1["reactions"])
	var reactions []map[string]any
	json.Unmarshal(raw, &reactions)
	byEmoji := map[string]map[string]any{}
	for _, e := range reactions {
		byEmoji[e["emoji"].(string)] = e
	}
	heart := byEmoji["❤️"]
	if heart == nil {
		t.Fatalf("no ❤️ in %v", reactions)
	}
	if heart["count"].(float64) != 2 { // me + Sam (changed)
		t.Errorf("count = %v", heart["count"])
	}
	if heart["mine"] != true {
		t.Errorf("mine = %v", heart["mine"])
	}
	who, _ := json.Marshal(heart["who"])
	var whoList []string
	json.Unmarshal(who, &whoList)
	sortStrings(whoList)
	if !reflect.DeepEqual(whoList, []string{"Sam Stone", "You"}) {
		t.Errorf("who = %v", whoList)
	}
	if _, ok := byEmoji["😂"]; ok {
		t.Error("😂 should be gone (Sam moved off it, other sender cleared)")
	}
	if m1["myReaction"] != "❤️" {
		t.Errorf("myReaction = %v", m1["myReaction"])
	}
}

// asF normalizes numeric fields from either JSON (float64) or direct Go maps (int64).
func asF(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	}
	return -1e300
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func TestReactBuildsWacliArgs(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	var calls [][]string
	lockWaits = nil
	saved := runWacliFn
	runWacliFn = func(args []string, opts wacliOpts) (map[string]any, *helperError) {
		calls = append(calls, args)
		lockWaits = append(lockWaits, opts.lockWait)
		return map[string]any{}, nil
	}
	defer func() { runWacliFn = saved }()
	// unknown message id -> nothing sent
	if _, err := sendReact(f.store, "222@g.us", "nope", "", "🔥"); err == nil {
		t.Error("unknown id accepted")
	}
	con := openrw(t, f.db)
	mustExec(t, con, insertMsg, []any{
		"222@g.us", "Crew", "gm1", "555@s.whatsapp.net", "Sam", 80, 0,
		"hi", "hi", "", "", 0, 0, "", "", "", "", "", "", 0, "",
		0, 0, 0, 0, 0, "",
	})
	con.Close()
	if _, err := sendReact(f.store, "222@g.us", "gm1", "", ""); err != nil {
		t.Fatal(err)
	}
	args := calls[len(calls)-1]
	if args[0] != "send" || args[1] != "react" {
		t.Errorf("args = %v", args)
	}
	if args[indexOf("--reaction", args)+1] != "" { // empty clears
		t.Errorf("reaction = %v", args)
	}
	if args[indexOf("--sender", args)+1] != "555@s.whatsapp.net" {
		t.Errorf("sender = %v", args)
	}
}

func TestGroupReplyPassesQuotedSender(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	var calls [][]string
	saved := runWacliFn
	runWacliFn = func(args []string, opts wacliOpts) (map[string]any, *helperError) {
		calls = append(calls, args)
		if args[0] == "send" && args[1] == "text" && args[indexOf("--message", args)+1] == "mach" {
			con := openrw(t, f.db)
			mustExec(t, con, insertMsg, []any{
				"222@g.us", "Crew", "sent1", "", "me", 300, 1,
				"mach", "mach", "", "", 0, 0, "", "", "", "", "", "", 0, "",
				0, 0, 0, 0, 0, "",
			})
			con.Close()
			return map[string]any{"id": "sent1", "sent": true}, nil
		}
		return map[string]any{}, nil
	}
	defer func() { runWacliFn = saved }()
	con := openrw(t, f.db)
	mustExec(t, con, insertMsg, []any{
		"222@g.us", "Crew", "gm1", "555@s.whatsapp.net", "Sam", 80, 0,
		"hi", "hi", "", "", 0, 0, "", "", "", "", "", "", 0, "",
		0, 0, 0, 0, 0, "",
	})
	con.Close()
	if _, err := sendText(f.store, "222@g.us", "mach", "gm1", nil); err != nil {
		t.Fatal(err)
	}
	args := calls[len(calls)-1]
	if args[0] != "send" || args[1] != "text" {
		t.Errorf("args = %v", args)
	}
	if args[indexOf("--reply-to", args)+1] != "gm1" {
		t.Errorf("reply-to = %v", args)
	}
	if args[indexOf("--reply-to-sender", args)+1] != "555@s.whatsapp.net" {
		t.Errorf("reply-to-sender = %v", args)
	}
	messages, err := listMessages(f.store, "222@g.us", 80, 0)
	if err != nil {
		t.Fatal(err)
	}
	sent := find(t, messages, "id", "sent1")
	if sent["quotedId"] != "gm1" || sent["quotedText"] != "hi" || sent["quotedSender"] != "Sam" {
		t.Errorf("sent = %v", sent)
	}
	if _, err := sendText(f.store, "111@s.whatsapp.net", "ok", "m1", nil); err != nil {
		t.Fatal(err)
	}
	dm := calls[len(calls)-1]
	if dm[indexOf("--reply-to", dm)+1] != "m1" {
		t.Errorf("dm reply-to = %v", dm)
	}
	if dm[indexOf("--reply-to-sender", dm)+1] != "111@s.whatsapp.net" {
		t.Errorf("dm sender = %v", dm)
	}
}

func TestSendFileReplyPassesQuotedSender(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	pic := t.TempDir() + "/pic.jpg"
	if err := os.WriteFile(pic, []byte("\xff\xd8\xff"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	saved := runWacliFn
	runWacliFn = func(args []string, opts wacliOpts) (map[string]any, *helperError) {
		calls = append(calls, args)
		if args[0] == "send" && args[1] == "file" {
			con := openrw(t, f.db)
			mustExec(t, con, insertMsg, []any{
				"222@g.us", "Crew", "sent-pic", "", "me", 300, 1,
				"look", "look", "", "", 0, 0, "", "", "image", "look",
				"pic.jpg", "image/jpeg", 3, pic, 300, 0, 0, 0, 0, "",
			})
			con.Close()
			return map[string]any{"id": "sent-pic", "sent": true}, nil
		}
		return map[string]any{}, nil
	}
	defer func() { runWacliFn = saved }()
	con := openrw(t, f.db)
	mustExec(t, con, insertMsg, []any{
		"222@g.us", "Crew", "gm1", "555@s.whatsapp.net", "Sam", 80, 0,
		"hi", "hi", "", "", 0, 0, "", "", "", "", "", "", 0, "",
		0, 0, 0, 0, 0, "",
	})
	con.Close()
	if _, err := sendFile(f.store, "222@g.us", pic, "look", "auto", "gm1"); err != nil {
		t.Fatal(err)
	}
	args := calls[len(calls)-1]
	if args[0] != "send" || args[1] != "file" {
		t.Errorf("args = %v", args)
	}
	if args[indexOf("--reply-to", args)+1] != "gm1" {
		t.Errorf("reply-to = %v", args)
	}
	if args[indexOf("--reply-to-sender", args)+1] != "555@s.whatsapp.net" {
		t.Errorf("reply-to-sender = %v", args)
	}
	if args[indexOf("--caption", args)+1] != "look" {
		t.Errorf("caption = %v", args)
	}
	messages, err := listMessages(f.store, "222@g.us", 80, 0)
	if err != nil {
		t.Fatal(err)
	}
	sent := find(t, messages, "id", "sent-pic")
	if sent["quotedId"] != "gm1" || sent["quotedText"] != "hi" || sent["quotedSender"] != "Sam" {
		t.Errorf("sent = %v", sent)
	}
}

func TestReactionFromClipboardRejectsJunk(t *testing.T) {
	newFixture(t)
	if got := reactionFromClipboard("🔥\nmore"); got != "" {
		t.Errorf("newline = %q", got)
	}
	if got := reactionFromClipboard(strings.Repeat("x", 33)); got != "" {
		t.Errorf("too long = %q", got)
	}
	if got := reactionFromClipboard("  ❤️  "); got != "❤️" {
		t.Errorf("trimmed = %q", got)
	}
}

func TestPickEmojiReturnsClipboardSelection(t *testing.T) {
	newFixture(t)
	state := map[string]any{"open": false, "clip": "keep-me"}
	savedClip, savedSet, savedOverlay, savedSummon := clipboardTextFn, setClipboardFn, emojiOverlayOpenFn, summonEmojiPickerFn
	defer func() {
		clipboardTextFn, setClipboardFn, emojiOverlayOpenFn, summonEmojiPickerFn = savedClip, savedSet, savedOverlay, savedSummon
	}()
	clipboardTextFn = func() string { return state["clip"].(string) }
	setClipboardFn = func(text string) { state["clip"] = text }
	emojiOverlayOpenFn = func() bool { return state["open"].(bool) }
	summonEmojiPickerFn = func() *helperError {
		state["open"] = true
		state["clip"] = "🔥"
		return nil
	}
	got, err := cmdPickEmoji(false)
	if err != nil || got["emoji"] != "🔥" {
		t.Errorf("got = %v err = %v", got, err)
	}
}

func TestPickEmojiCancelRestoresClipboard(t *testing.T) {
	newFixture(t)
	var restored []string
	n := 0
	savedClip, savedSet, savedOverlay, savedSummon := clipboardTextFn, setClipboardFn, emojiOverlayOpenFn, summonEmojiPickerFn
	defer func() {
		clipboardTextFn, setClipboardFn, emojiOverlayOpenFn, summonEmojiPickerFn = savedClip, savedSet, savedOverlay, savedSummon
	}()
	summonEmojiPickerFn = func() *helperError { return nil }
	emojiOverlayOpenFn = func() bool { n++; return n < 3 }
	clipboardTextFn = func() string {
		if n == 0 {
			return "keep-me"
		}
		return ""
	}
	setClipboardFn = func(text string) { restored = append(restored, text) }
	got, err := cmdPickEmoji(false)
	if err != nil || got["emoji"] != "" {
		t.Errorf("got = %v err = %v", got, err)
	}
	if !strings.Contains(strings.Join(restored, ","), "keep-me") {
		t.Errorf("restored = %v", restored)
	}
}

func TestPickEmojiWatchOnlyDoesNotSummon(t *testing.T) {
	newFixture(t)
	var summoned int
	savedClip, savedSet, savedOverlay, savedSummon := clipboardTextFn, setClipboardFn, emojiOverlayOpenFn, summonEmojiPickerFn
	defer func() {
		clipboardTextFn, setClipboardFn, emojiOverlayOpenFn, summonEmojiPickerFn = savedClip, savedSet, savedOverlay, savedSummon
	}()
	summonEmojiPickerFn = func() *helperError { summoned++; return nil }
	emojiOverlayOpenFn = func() bool { return true }
	clipboardTextFn = func() string { return "🔥" }
	setClipboardFn = func(text string) {}
	got, err := cmdPickEmoji(true)
	if err != nil || got["emoji"] != "🔥" {
		t.Errorf("got = %v err = %v", got, err)
	}
	if summoned != 0 {
		t.Error("summoned in watch-only mode")
	}
}

func TestPickEmojiWatchOnlyReturnsPickAfterOverlayClosed(t *testing.T) {
	newFixture(t)
	var summoned int
	savedClip, savedSet, savedOverlay, savedSummon := clipboardTextFn, setClipboardFn, emojiOverlayOpenFn, summonEmojiPickerFn
	defer func() {
		clipboardTextFn, setClipboardFn, emojiOverlayOpenFn, summonEmojiPickerFn = savedClip, savedSet, savedOverlay, savedSummon
	}()
	summonEmojiPickerFn = func() *helperError { summoned++; return nil }
	emojiOverlayOpenFn = func() bool { return false }
	clipboardTextFn = func() string { return "🎉" }
	setClipboardFn = func(text string) {}
	got, err := cmdPickEmoji(true)
	if err != nil || got["emoji"] != "🎉" {
		t.Errorf("got = %v err = %v", got, err)
	}
	if summoned != 0 {
		t.Error("summoned in watch-only mode")
	}
}

func TestLidChatSurfacesAsDm(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	con := openrw(t, f.db)
	mustExec(t, con, `INSERT INTO chats VALUES ('999@lid','unknown','Christa',400,0,0,0,1,1)`, nil)
	mustExec(t, con, `INSERT INTO chats VALUES ('888@lid','unknown','',NULL,0,0,0,0,0)`, nil) // nameless empty @lid stays hidden
	mustExec(t, con, insertMsg, []any{
		"999@lid", "Christa", "lm1", "", "", 400, 1,
		"", "", "", "", 0, 0, "", "", "image", "", "p.jpg", "image/jpeg",
		9, "", 0, 0, 0, 0, 0, "",
	})
	con.Close()
	chats, err := listChats(f.store, "", 80, false)
	if err != nil {
		t.Fatal(err)
	}
	byJID := map[string]map[string]any{}
	for _, c := range chats {
		byJID[c["jid"].(string)] = c
	}
	if byJID["999@lid"] == nil {
		t.Fatal("999@lid missing")
	}
	if byJID["999@lid"]["kind"] != "dm" || byJID["999@lid"]["isGroup"] != false {
		t.Errorf("chat = %v", byJID["999@lid"])
	}
	if byJID["888@lid"] != nil {
		t.Error("nameless @lid chat surfaced")
	}
	msgs, err := listMessages(f.store, "999@lid", 80, 0)
	if err != nil {
		t.Fatal(err)
	}
	if msgs[0]["id"] != "lm1" || msgs[0]["kind"] != "image" {
		t.Errorf("msgs[0] = %v", msgs[0])
	}
}

func TestLidAndPhoneChatsStitchIntoOneThread(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	scon := openrw(t, f.store+"/session.db")
	execSQL(t, scon, "CREATE TABLE whatsmeow_lid_map (lid TEXT PRIMARY KEY, pn TEXT UNIQUE NOT NULL)")
	mustExec(t, scon, "INSERT INTO whatsmeow_lid_map VALUES ('700700','111')", nil) // 111 == Ada's dm
	scon.Close()
	con := openrw(t, f.db)
	mustExec(t, con, `INSERT INTO chats VALUES ('700700@lid','unknown','Ada',130,0,0,0,0,0)`, nil)
	for _, e := range [][3]any{{"lidpic", int64(125), "image"}, {"lidpic2", int64(128), "image"}} {
		mustExec(t, con, insertMsg, []any{
			"700700@lid", "Ada", e[0], "", "", e[1], 1,
			"", "", "", "", 0, 0, "", "", e[2], "", "p.jpg", "image/jpeg",
			9, "", 0, 0, 0, 0, 0, "",
		})
	}
	con.Close()

	chats, err := listChats(f.store, "", 80, false)
	if err != nil {
		t.Fatal(err)
	}
	byJID := map[string]map[string]any{}
	for _, c := range chats {
		byJID[c["jid"].(string)] = c
	}
	if byJID["111@s.whatsapp.net"] == nil {
		t.Fatal("phone chat missing")
	}
	if byJID["700700@lid"] != nil {
		t.Error("lid chat not folded into the phone-number chat")
	}
	if asF(byJID["111@s.whatsapp.net"]["lastMessageTs"]) != 128 { // from the LID side
		t.Errorf("lastMessageTs = %v", byJID["111@s.whatsapp.net"]["lastMessageTs"])
	}

	msgs, err := listMessages(f.store, "111@s.whatsapp.net", 80, 0)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range msgs {
		ids = append(ids, m["id"].(string))
		if m["chatJid"] != "111@s.whatsapp.net" {
			t.Errorf("chatJid = %v", m["chatJid"])
		}
	}
	if !reflect.DeepEqual(ids, []string{"m1", "lidpic", "lidpic2"}) { // interleaved by ts
		t.Errorf("ids = %v", ids)
	}

	var callArgs [][]string
	saved := runWacliFn
	runWacliFn = func(args []string, opts wacliOpts) (map[string]any, *helperError) {
		callArgs = append(callArgs, args)
		for i, a := range args {
			if a == "--output" {
				if err := os.WriteFile(args[i+1], []byte("\xff\xd8\xff\xd9"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		return map[string]any{}, nil
	}
	defer func() { runWacliFn = saved }()
	got, err := downloadMedia(f.store, "111@s.whatsapp.net", "lidpic")
	if err != nil {
		t.Fatal(err)
	}
	if callArgs[0][indexOf("--chat", callArgs[0])+1] != "700700@lid" {
		t.Errorf("chat arg = %v", callArgs[0])
	}
	if _, statErr := os.Stat(got["localPath"].(string)); statErr != nil {
		t.Errorf("localPath: %v", statErr)
	}
}

func TestVideoThumbIsFirstFrame(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	if got := videoThumb("/no/such.mp4"); got != "" {
		t.Errorf("missing file = %q", got)
	}
	mp4 := t.TempDir() + "/clip.mp4"
	if err := tinyMP4(t, mp4); err != nil {
		t.Skip("ffmpeg cannot encode a test clip")
	}
	con := openrw(t, f.db)
	mustExec(t, con, insertMsg, []any{
		"111@s.whatsapp.net", "Ada", "vid1", "111@s.whatsapp.net", "Ada", 140, 0,
		"", "Sent video", "", "", 0, 0, "", "", "video", "", "clip.mp4", "video/mp4",
		fileSize(t, mp4), mp4, 1, 0, 0, 0, 0, "",
	})
	con.Close()
	messages, err := listMessages(f.store, "111@s.whatsapp.net", 80, 0)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]any{}
	for _, m := range messages {
		byID[m["id"].(string)] = m
	}
	if byID["vid1"]["kind"] != "video" || byID["vid1"]["downloaded"] != true {
		t.Errorf("vid1 = %v", byID["vid1"])
	}
	if !strings.HasPrefix(byID["vid1"]["thumbUrl"].(string), "file:") {
		t.Errorf("thumbUrl = %v", byID["vid1"]["thumbUrl"])
	}
	thumb := strings.TrimSuffix(mp4, ".mp4") + ".jpg"
	if data, err := os.ReadFile(thumb); err != nil || !bytes.HasPrefix(data, []byte("\xff\xd8\xff")) {
		t.Errorf("thumb: %v", err)
	}

	blob := readBytes(t, mp4)
	saved := runWacliFn
	runWacliFn = func(args []string, opts wacliOpts) (map[string]any, *helperError) {
		for i, a := range args {
			if a == "--output" {
				if err := os.WriteFile(args[i+1], blob, 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		return map[string]any{}, nil
	}
	defer func() { runWacliFn = saved }()
	con = openrw(t, f.db)
	mustExec(t, con, insertMsg, []any{
		"111@s.whatsapp.net", "Ada", "vid2", "111@s.whatsapp.net", "Ada", 141, 0,
		"", "Sent video", "", "", 0, 0, "", "", "video", "", "clip.mp4", "video/mp4",
		len(blob), "", 0, 0, 0, 0, 0, "",
	})
	con.Close()
	result, err := downloadMedia(f.store, "111@s.whatsapp.net", "vid2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result["thumbUrl"].(string), "file:") {
		t.Errorf("thumbUrl = %v", result["thumbUrl"])
	}
	local := result["localPath"].(string)
	if _, err := os.Stat(strings.TrimSuffix(local, filepath.Ext(local)) + ".jpg"); err != nil {
		t.Errorf("thumb: %v", err)
	}
}

func TestPickNameAndPlaceholders(t *testing.T) {
	newFixture(t)
	if got := pickName("777@s.whatsapp.net", "777@s.whatsapp.net", "Kevin Bernthaler"); got != "Kevin Bernthaler" {
		t.Errorf("pickName = %q", got)
	}
	if got := pickName("666@g.us", "666@g.us"); got != "Unknown group" {
		t.Errorf("group = %q", got)
	}
	if got := humanPreview("Sent image"); got != "Photo" {
		t.Errorf("preview = %q", got)
	}
	if got := placeholderLabel("sent video"); got != "Video" {
		t.Errorf("video = %q", got)
	}
	if got := humanPreview("[Album: 2 images]"); got != "Photo" {
		t.Errorf("album = %q", got)
	}
	if got := albumCount("[Album: 3 images]"); got != 3 {
		t.Errorf("count = %d", got)
	}
	if got := albumCount("Wer ist das?"); got != 0 {
		t.Errorf("no-album = %d", got)
	}
}

// --- tiny helpers over database/sql ---

type sqlDB struct {
	con *sql.DB
}

func sqlOpen(path string) (*sqlDB, error) {
	con, err := sql.Open("sqlite3", "file:"+path+"?_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	return &sqlDB{con: con}, nil
}

func (d *sqlDB) exec(query string) error {
	_, err := d.con.Exec(query)
	return err
}

func (d *sqlDB) stmtExec(query string, args []any) (sql.Result, error) {
	return d.con.Exec(query, args...)
}

func (d *sqlDB) Close() error { return d.con.Close() }

func openrw(t *testing.T, path string) *sqlDB {
	t.Helper()
	con, err := sqlOpen(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	return con
}

func execSQL(t *testing.T, con *sqlDB, query string) {
	t.Helper()
	if err := con.exec(query); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

func TestPollVoteLinksToPollAndDropsSupersededVotes(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	con := openrw(t, f.db)
	for _, id := range []string{"v1", "v2"} {
		mustExec(t, con, insertMsg, []any{
			"222@g.us", "Crew", id, "555@s.whatsapp.net", "Sam", 300, 0,
			"Poll vote", "Poll vote", "", "", 0, 0, "", "", "", "", "", "", 0, "",
			0, 0, 0, 0, 0, "",
		})
	}
	// Sam changed his pick: only the latest vote message survives in poll_votes.
	mustExec(t, con, `INSERT INTO poll_votes VALUES (?,?,?,?,?,?)`, []any{
		"222@g.us", "poll1", "555@s.whatsapp.net", "v2", `["yes"]`, 300,
	})
	con.Close()

	data, err := runHelper(t, []string{"messages", "--chat", "222@g.us"})
	if err != nil {
		t.Fatal(err)
	}
	votes := 0
	for _, m := range messagesPayload(t, data) {
		if m["text"] != "Poll vote" {
			continue
		}
		votes++
		if m["id"] != "v2" || m["pollId"] != "poll1" {
			t.Errorf("vote row = %v/%v, want v2/poll1", m["id"], m["pollId"])
		}
	}
	if votes != 1 {
		t.Errorf("poll vote rows = %d, want 1", votes)
	}
}

func TestGroupMessageFromAnotherSenderIsNotFromMe(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	scon := openrw(t, f.store+"/session.db")
	execSQL(t, scon, "CREATE TABLE whatsmeow_device (jid TEXT PRIMARY KEY, lid TEXT)")
	mustExec(t, scon, "INSERT INTO whatsmeow_device VALUES ('999:20@s.whatsapp.net','888:20@lid')", nil)
	scon.Close()
	con := openrw(t, f.db)
	// A revoke sent by Sam used to flip his own message to from_me.
	mustExec(t, con, insertMsg, []any{
		"222@g.us", "Crew", "gone", "555@s.whatsapp.net", "Sam", 300, 1,
		"", "This message was deleted", "", "", 0, 0, "", "", "", "", "", "", 0, "",
		0, 0, 1, 0, 0, "",
	})
	mustExec(t, con, insertMsg, []any{
		"222@g.us", "Crew", "mine", "999@s.whatsapp.net", "Me", 301, 1,
		"hi", "hi", "", "", 0, 0, "", "", "", "", "", "", 0, "",
		0, 0, 0, 0, 0, "",
	})
	con.Close()

	data, err := runHelper(t, []string{"messages", "--chat", "222@g.us"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range messagesPayload(t, data) {
		want := m["id"] == "mine"
		if m["id"] == "gone" || m["id"] == "mine" {
			if m["fromMe"] != want {
				t.Errorf("%v fromMe = %v, want %v", m["id"], m["fromMe"], want)
			}
		}
	}
}

func mustExec(t *testing.T, con *sqlDB, query string, args []any) {
	t.Helper()
	if _, err := con.stmtExec(query, args); err != nil {
		t.Fatalf("exec %q: %v", query[:40], err)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func tinyMP4(t *testing.T, path string) error {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "color=c=red:s=16x16:d=0.2",
		"-pix_fmt", "yuv420p", path)
	if err := cmd.Run(); err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no clip: %w", err)
	}
	return nil
}

func TestMarkReadRollsAckBackWhenWacliFails(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	saved := runWacliFn
	runWacliFn = func(args []string, opts wacliOpts) (map[string]any, *helperError) {
		return nil, fail("store is locked")
	}
	defer func() { runWacliFn = saved }()
	if _, err := cmdMarkRead(f.store, "111@s.whatsapp.net", 0); err == nil {
		t.Fatal("expected error")
	}
	// A stuck ack would make every later mark-read a no-op for this chat.
	if got := loadPrefs().Acks["111@s.whatsapp.net"]; got != 0 {
		t.Errorf("ack = %d, want 0", got)
	}
}
