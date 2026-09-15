package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// Edits made on another device arrive as a SecretEncryptedMessage envelope
// carrying a MESSAGE_EDIT protocol message that points at the original row.
// The store must upsert the original message, not keep an empty stub bubble.
func TestLiveSyncSecretEncryptedEditUpdatesOriginal(t *testing.T) {
	a := newTestApp(t)
	f := newFakeWA()
	a.wa = f

	chat := types.JID{User: "555", Server: types.DefaultUserServer}
	origTS := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	orig := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: chat, IsFromMe: true},
			ID:            "ORIG",
			Timestamp:     origTS,
		},
		Message: &waProto.Message{Conversation: proto.String("original text")},
	}

	var messagesStored atomic.Int64
	a.handleLiveSyncMessage(context.Background(), SyncOptions{}, orig, &messagesStored, func(string, string) {}, nil)

	f.decryptSecretFunc = func(evt *events.Message) (*waE2E.Message, error) {
		return &waE2E.Message{
			ProtocolMessage: &waProto.ProtocolMessage{
				Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
				Key: &waProto.MessageKey{
					RemoteJID: proto.String(chat.String()),
					FromMe:    proto.Bool(true),
					ID:        proto.String("ORIG"),
				},
				EditedMessage: &waProto.Message{Conversation: proto.String("edited text")},
			},
		}, nil
	}
	editTS := origTS.Add(time.Minute)
	edit := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: chat, IsFromMe: true},
			ID:            "ENVELOPE",
			Timestamp:     editTS,
		},
		Message: &waProto.Message{
			SecretEncryptedMessage: &waE2E.SecretEncryptedMessage{
				SecretEncType: waE2E.SecretEncryptedMessage_MESSAGE_EDIT.Enum(),
				TargetMessageKey: &waCommon.MessageKey{
					RemoteJID: proto.String(chat.String()),
					FromMe:    proto.Bool(true),
					ID:        proto.String("ORIG"),
				},
			},
		},
	}
	a.handleLiveSyncMessage(context.Background(), SyncOptions{}, edit, &messagesStored, func(string, string) {}, nil)

	msg, err := a.db.GetMessage(chat.String(), "ORIG")
	if err != nil {
		t.Fatalf("original row: %v", err)
	}
	if msg.Text != "edited text" {
		t.Fatalf("text = %q, want edited text", msg.Text)
	}
	if !msg.Edited {
		t.Fatalf("edited flag not set")
	}
	if !msg.FromMe {
		t.Fatalf("from_me lost")
	}
	if msg.Timestamp.Unix() != origTS.Unix() {
		t.Fatalf("ts = %d, want original %d", msg.Timestamp.Unix(), origTS.Unix())
	}

	if _, err := a.db.GetMessage(chat.String(), "ENVELOPE"); err == nil {
		t.Fatalf("edit envelope stored as separate stub row")
	}
}
