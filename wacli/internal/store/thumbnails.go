package store

import (
	"fmt"
	"strings"
)

type MessageThumbnail struct {
	ChatJID string
	MsgID   string
	JPEG    []byte
}

func (d *DB) UpsertMessageThumbnail(thumb MessageThumbnail) error {
	if d == nil {
		return fmt.Errorf("nil db")
	}
	chatJID := strings.TrimSpace(thumb.ChatJID)
	msgID := strings.TrimSpace(thumb.MsgID)
	if chatJID == "" || msgID == "" {
		return fmt.Errorf("message thumbnail requires chat_jid and msg_id")
	}
	if len(thumb.JPEG) < 24 || thumb.JPEG[0] != 0xff || thumb.JPEG[1] != 0xd8 {
		return nil
	}
	if _, err := d.sql.Exec(`
		INSERT INTO message_thumbnails(chat_jid, msg_id, jpeg)
		SELECT ?, ?, ?
		WHERE NOT EXISTS (
			SELECT 1 FROM message_payload_purges p WHERE p.chat_jid = ? AND p.msg_id = ?
		)
		ON CONFLICT(chat_jid, msg_id) DO UPDATE SET jpeg = excluded.jpeg
	`, chatJID, msgID, thumb.JPEG, chatJID, msgID); err != nil {
		return fmt.Errorf("upsert message thumbnail: %w", err)
	}
	return nil
}

func (d *DB) GetMessageThumbnail(chatJID, msgID string) (MessageThumbnail, error) {
	if d == nil {
		return MessageThumbnail{}, fmt.Errorf("nil db")
	}
	var jpeg []byte
	err := d.sql.QueryRow(
		`SELECT jpeg FROM message_thumbnails WHERE chat_jid = ? AND msg_id = ?`,
		strings.TrimSpace(chatJID), strings.TrimSpace(msgID),
	).Scan(&jpeg)
	if err != nil {
		return MessageThumbnail{}, err
	}
	return MessageThumbnail{ChatJID: chatJID, MsgID: msgID, JPEG: jpeg}, nil
}
