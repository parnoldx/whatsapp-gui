package store

import (
	"bytes"
	"database/sql"
	"errors"
	"testing"
)

func testJPEG() []byte {
	b := bytes.Repeat([]byte{0x00}, 32)
	b[0], b[1] = 0xff, 0xd8
	return b
}

func TestUpsertAndGetMessageThumbnail(t *testing.T) {
	db := openTestDB(t)
	chat := "15551112222@s.whatsapp.net"
	jpeg := testJPEG()
	if err := db.UpsertMessageThumbnail(MessageThumbnail{ChatJID: chat, MsgID: "VID-1", JPEG: jpeg}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := db.GetMessageThumbnail(chat, "VID-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(got.JPEG, jpeg) {
		t.Fatalf("jpeg mismatch len=%d", len(got.JPEG))
	}
}

func TestUpsertMessageThumbnailSkipsJunk(t *testing.T) {
	db := openTestDB(t)
	chat := "15551112222@s.whatsapp.net"
	if err := db.UpsertMessageThumbnail(MessageThumbnail{ChatJID: chat, MsgID: "VID-1", JPEG: []byte("nope")}); err != nil {
		t.Fatalf("upsert junk: %v", err)
	}
	if _, err := db.GetMessageThumbnail(chat, "VID-1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get = %v, want no rows", err)
	}
}

func TestUpsertMessageThumbnailReplaces(t *testing.T) {
	db := openTestDB(t)
	chat := "15551112222@s.whatsapp.net"
	first := testJPEG()
	second := testJPEG()
	second[2] = 0xdb
	if err := db.UpsertMessageThumbnail(MessageThumbnail{ChatJID: chat, MsgID: "VID-1", JPEG: first}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := db.UpsertMessageThumbnail(MessageThumbnail{ChatJID: chat, MsgID: "VID-1", JPEG: second}); err != nil {
		t.Fatalf("second: %v", err)
	}
	got, err := db.GetMessageThumbnail(chat, "VID-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(got.JPEG, second) {
		t.Fatalf("did not replace")
	}
}
