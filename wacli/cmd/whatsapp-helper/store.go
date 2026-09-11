package main

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var mediaKinds = map[string]bool{
	"image": true, "video": true, "gif": true, "sticker": true,
	"voice": true, "audio": true, "document": true,
}

var albumMedia = map[string]bool{"image": true, "video": true, "gif": true}

// mediaKind maps stored media_type/mime to the kind the GUI renders.
func mediaKind(mediaType, mime string) string {
	kind := strings.ToLower(strings.TrimSpace(mediaType))
	mime = strings.ToLower(strings.TrimSpace(mime))
	switch {
	case kind == "image" || kind == "video" || kind == "gif" || kind == "sticker" || kind == "document" || kind == "location":
		return kind
	case kind == "audio":
		if strings.Contains(mime, "opus") || strings.Contains(mime, "ogg") {
			return "voice"
		}
		return "audio"
	case strings.HasPrefix(mime, "image/"):
		if strings.Contains(mime, "webp") {
			return "sticker"
		}
		return "image"
	case strings.HasPrefix(mime, "video/"):
		if strings.Contains(mime, "gif") {
			return "gif"
		}
		return "video"
	case strings.HasPrefix(mime, "audio/"):
		if strings.Contains(mime, "opus") || strings.Contains(mime, "ogg") {
			return "voice"
		}
		return "audio"
	}
	if kind == "" {
		return "text"
	}
	return "unknown"
}

func openDB(store string) (*sql.DB, *helperError) {
	path := dbPath(store)
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return nil, fail("wacli store is not initialized")
	}
	con, err := sql.Open("sqlite3", "file:"+path+"?mode=ro&_busy_timeout=2000")
	if err != nil {
		return nil, fail("could not open the local message store")
	}
	con.SetMaxOpenConns(4) // listChats iterates rows while running unread queries
	return con, nil
}

// --- lid <-> phone-number stitching ---

var lidMapCache map[string]string

// lidPNMap maps lid_jid <-> pn_jid, both directions, from whatsmeow's session
// store. WhatsApp splits one conversation across a phone-number JID and a LID
// JID; this map stitches them back into a single thread.
// ponytail: cached for process lifetime; the GUI restarts the daemon, and a
// mapping added mid-session just shows up on next launch.
func lidPNMap(store string) map[string]string {
	if lidMapCache != nil {
		return lidMapCache
	}
	out := map[string]string{}
	con, err := sql.Open("sqlite3", "file:"+store+"/session.db?mode=ro")
	if err == nil {
		defer con.Close()
		rows, err := con.Query("SELECT lid, pn FROM whatsmeow_lid_map")
		if err == nil {
			for rows.Next() {
				var lid, pn string
				if rows.Scan(&lid, &pn) == nil {
					lj, pj := lid+"@lid", pn+"@s.whatsapp.net"
					out[lj] = pj
					out[pj] = lj
				}
			}
			rows.Close()
		}
	}
	lidMapCache = out
	return out
}

var selfJIDCache map[string]bool

// selfJIDs is this account's own JIDs (phone number and LID, device suffix
// stripped) from whatsmeow's session store.
// ponytail: cached for process lifetime, same as lidPNMap.
func selfJIDs(store string) map[string]bool {
	if selfJIDCache != nil {
		return selfJIDCache
	}
	out := map[string]bool{}
	con, err := sql.Open("sqlite3", "file:"+store+"/session.db?mode=ro")
	if err == nil {
		defer con.Close()
		rows, err := con.Query("SELECT jid, lid FROM whatsmeow_device")
		if err == nil {
			for rows.Next() {
				var jid, lid sql.NullString
				if rows.Scan(&jid, &lid) == nil {
					for _, j := range []string{jid.String, lid.String} {
						if b := bareJID(j); b != "" {
							out[b] = true
						}
					}
				}
			}
			rows.Close()
		}
	}
	selfJIDCache = out
	return out
}

// bareJID drops the device suffix: 4915...:20@s.whatsapp.net -> 4915...@s.whatsapp.net
func bareJID(jid string) string {
	jid = strings.TrimSpace(jid)
	if i := strings.IndexByte(jid, ':'); i >= 0 {
		if at := strings.IndexByte(jid, '@'); at > i {
			return jid[:i] + jid[at:]
		}
	}
	return jid
}

func siblingJIDs(store, jid string) []string {
	if other, ok := lidPNMap(store)[jid]; ok {
		return []string{jid, other}
	}
	return []string{jid}
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// --- chats ---

type chatRow struct {
	jid, kind        string
	name             string
	lastMessageTs    int64
	archived, pinned bool
	mutedUntil       int64
	unread           bool
	unreadCount      int64
	isParent         bool
	linkedParent     string
}

func requireChat(con *sql.DB, jid string) (*chatRow, *helperError) {
	row := con.QueryRow(`
		SELECT c.jid, c.kind, c.name, c.last_message_ts, c.archived, c.pinned,
		       c.muted_until, c.unread, c.unread_count, g.is_parent, g.linked_parent_jid
		FROM chats c
		LEFT JOIN groups g ON g.jid = c.jid
		WHERE c.jid = ?`, jid)
	var r chatRow
	var kind, name sql.NullString
	var lastTS, archived, pinned, mutedUntil, unread, unreadCount, isParent sql.NullInt64
	var linked sql.NullString
	if err := row.Scan(&r.jid, &kind, &name, &lastTS, &archived, &pinned, &mutedUntil, &unread, &unreadCount, &isParent, &linked); err != nil {
		return nil, fail("that chat is not in the local store")
	}
	r.kind = kind.String
	r.name = name.String
	r.lastMessageTs = lastTS.Int64
	r.archived = archived.Int64 != 0
	r.pinned = pinned.Int64 != 0
	r.mutedUntil = mutedUntil.Int64
	r.unread = unread.Int64 != 0
	r.unreadCount = unreadCount.Int64
	r.isParent = isParent.Int64 != 0
	r.linkedParent = linked.String
	if r.kind != "dm" && r.kind != "group" && !(r.kind == "unknown" && strings.HasSuffix(jid, "@lid")) {
		return nil, fail("only direct chats and groups are supported")
	}
	if r.isParent {
		return nil, fail("community parents are not supported")
	}
	if r.linkedParent != "" {
		return nil, fail("community-linked groups are not supported")
	}
	return &r, nil
}

func unreadSince(con *sql.DB, jid string, ack int64) int64 {
	var count int64
	con.QueryRow(`
		SELECT COUNT(*) FROM messages
		WHERE chat_jid = ?
		  AND ts > ?
		  AND IFNULL(from_me, 0) = 0
		  AND IFNULL(deleted_for_me, 0) = 0
		  AND IFNULL(reaction_to_id, '') = ''
		  AND IFNULL(revoked, 0) = 0`, jid, ack).Scan(&count)
	return count
}

func listChats(store, query string, limit int, includeArchived bool) ([]map[string]any, *helperError) {
	if limit < 1 {
		limit = 1
	}
	if limit > maxChats {
		limit = maxChats
	}
	nowTS := now()
	needle := strings.ToLower(strings.TrimSpace(query))
	acks := loadPrefs().Acks

	con, he := openDB(store)
	if he != nil {
		return nil, he
	}
	defer con.Close()

	rows, err := con.Query(`
		SELECT c.jid, c.kind, c.name AS chat_name, c.last_message_ts, c.archived, c.pinned,
		       c.muted_until, c.unread, c.unread_count, g.is_parent, g.linked_parent_jid,
		       g.name AS group_name,
		       ct.full_name AS contact_full, ct.push_name AS contact_push,
		       ct.business_name AS contact_business, ct.first_name AS contact_first,
		       (
		         SELECT COALESCE(NULLIF(m.media_caption, ''), NULLIF(m.text, ''),
		                        NULLIF(m.display_text, ''), m.media_type)
		         FROM messages m
		         WHERE m.chat_jid = c.jid
		           AND IFNULL(m.deleted_for_me, 0) = 0
		           AND IFNULL(m.reaction_to_id, '') = ''
		           AND NOT (
		             IFNULL(m.text, '') = ''
		             AND IFNULL(m.media_caption, '') = ''
		             AND IFNULL(m.media_type, '') = ''
		             AND lower(IFNULL(m.display_text, '')) IN ('(message)', 'message')
		           )
		         ORDER BY m.ts DESC
		         LIMIT 1
		       ) AS preview
		FROM chats c
		LEFT JOIN groups g ON g.jid = c.jid
		LEFT JOIN contacts ct ON ct.jid = c.jid
		WHERE (
		        c.kind IN ('dm', 'group')
		        OR (c.kind = 'unknown' AND c.jid LIKE '%@lid'
		            AND c.last_message_ts IS NOT NULL AND IFNULL(c.name, '') <> '')
		      )
		  AND IFNULL(g.is_parent, 0) = 0
		  AND IFNULL(g.linked_parent_jid, '') = ''
		ORDER BY c.pinned DESC, c.last_message_ts DESC`)
	if err != nil {
		return nil, fail("could not read the local message store")
	}
	defer rows.Close()

	var out []map[string]any
	seen := map[string]bool{}
	for rows.Next() {
		var jid, kind, chatName sql.NullString
		var lastTS, archived, pinned, mutedUntil, unread, unreadCount, isParent sql.NullInt64
		var linked, groupName, cFull, cPush, cBusiness, cFirst, preview sql.NullString
		if err := rows.Scan(&jid, &kind, &chatName, &lastTS, &archived, &pinned, &mutedUntil,
			&unread, &unreadCount, &isParent, &linked, &groupName, &cFull, &cPush, &cBusiness,
			&cFirst, &preview); err != nil {
			continue
		}
		if includeArchived == false && archived.Int64 != 0 {
			continue
		}
		name := pickName(jid.String, groupName.String, cFull.String, cBusiness.String, cPush.String, cFirst.String, chatName.String)
		prev := humanPreview(preview.String)
		if needle != "" && !strings.Contains(strings.ToLower(name), needle) && !strings.Contains(strings.ToLower(prev), needle) {
			continue
		}
		lastTs := lastTS.Int64
		ack := acks[jid.String]
		unreadN := int64(0)
		if lastTs > ack && unread.Int64 != 0 {
			if ack > 0 {
				unreadN = unreadSince(con, jid.String, ack)
			} else {
				unreadN = unreadCount.Int64
			}
		}
		chatKind := kind.String
		if chatKind != "dm" && chatKind != "group" {
			chatKind = "dm" // @lid chats
		}
		out = append(out, map[string]any{
			"jid":           jid.String,
			"kind":          chatKind,
			"isGroup":       chatKind == "group",
			"name":          name,
			"lastMessageTs": lastTs,
			"archived":      archived.Int64 != 0,
			"pinned":        pinned.Int64 != 0,
			"muted":         isMuted(mutedUntil.Int64, nowTS),
			"unreadCount":   unreadN,
			"preview":       prev,
			"avatarUrl":     cachedAvatarURL(jid.String),
		})
		seen[jid.String] = true
		if len(out) >= limit {
			break
		}
	}

	// Stitch LID and phone-number chats: WhatsApp treats them as one thread.
	lmap := lidPNMap(store)
	var stitched []map[string]any
	for _, chat := range out {
		jid := chat["jid"].(string)
		if strings.HasSuffix(jid, "@lid") {
			if sib, ok := lmap[jid]; ok && seen[sib] {
				continue // folded into the phone-number chat
			}
		}
		stitched = append(stitched, chat)
	}
	for _, chat := range stitched {
		jid := chat["jid"].(string)
		lid, ok := lmap[jid]
		if !ok || !strings.HasSuffix(lid, "@lid") {
			continue
		}
		var ts int64
		var preview sql.NullString
		err := con.QueryRow(`
			SELECT ts, COALESCE(NULLIF(media_caption, ''), NULLIF(text, ''),
			       NULLIF(display_text, ''), media_type) AS preview
			FROM messages
			WHERE chat_jid = ? AND IFNULL(deleted_for_me, 0) = 0
			  AND IFNULL(reaction_to_id, '') = ''
			  AND NOT (
			    IFNULL(text, '') = ''
			    AND IFNULL(media_caption, '') = ''
			    AND IFNULL(media_type, '') = ''
			    AND lower(IFNULL(display_text, '')) IN ('(message)', 'message')
			  )
			ORDER BY ts DESC LIMIT 1`, lid).Scan(&ts, &preview)
		if err == nil && ts > chat["lastMessageTs"].(int64) {
			chat["lastMessageTs"] = ts
			if merged := humanPreview(preview.String); merged != "" {
				chat["preview"] = merged
			}
		}
	}

	// Mention names in previews.
	var mentionIDs []string
	for _, chat := range stitched {
		for id := range mentionIDsIn(chat["preview"].(string)) {
			mentionIDs = append(mentionIDs, id)
		}
	}
	if len(mentionIDs) > 0 {
		names := loadMentionNames(store, mentionIDs, con)
		for _, chat := range stitched {
			chat["preview"] = applyMentionNames(chat["preview"].(string), names)
		}
	}
	sort.SliceStable(stitched, func(i, j int) bool {
		pi, _ := stitched[i]["pinned"].(bool)
		pj, _ := stitched[j]["pinned"].(bool)
		if pi != pj {
			return pi
		}
		return stitched[i]["lastMessageTs"].(int64) > stitched[j]["lastMessageTs"].(int64)
	})
	if stitched == nil {
		stitched = []map[string]any{}
	}
	return stitched, nil
}

func badgeCount(chats []map[string]any, p prefs) int {
	total := 0
	for _, chat := range chats {
		if muted, _ := chat["muted"].(bool); muted {
			continue
		}
		if archived, _ := chat["archived"].(bool); archived {
			continue
		}
		jid, _ := chat["jid"].(string)
		last, _ := chat["lastMessageTs"].(int64)
		unread, _ := chat["unreadCount"].(int64)
		if last > p.Acks[jid] && unread > 0 {
			total++
		}
	}
	return total
}

// --- mention names ---

func loadMentionNames(store string, ids []string, con *sql.DB) map[string]string {
	idents := map[string]bool{}
	for _, id := range ids {
		if id != "" {
			idents[id] = true
		}
	}
	if len(idents) == 0 {
		return map[string]string{}
	}
	lmap := lidPNMap(store)
	var jids []string
	seenJIDs := map[string]bool{}
	for ident := range idents {
		for _, jid := range []string{ident + "@lid", ident + "@s.whatsapp.net"} {
			for _, candidate := range []string{jid, lmap[jid]} {
				if candidate != "" && !seenJIDs[candidate] {
					seenJIDs[candidate] = true
					jids = append(jids, candidate)
				}
			}
		}
	}
	own := con == nil
	if own {
		var he *helperError
		con, he = openDB(store)
		if he != nil {
			return map[string]string{}
		}
		defer con.Close()
	}
	where := fmt.Sprintf("phone IN (%s)", placeholders(len(idents)))
	args := keysOf(idents)
	if len(jids) > 0 {
		where = fmt.Sprintf("jid IN (%s) OR %s", placeholders(len(jids)), where)
		args = append(keysOfSet(seenJIDs), args...)
	}
	rows, err := con.Query(`
		SELECT jid, phone, full_name, business_name, push_name, first_name
		FROM contacts
		WHERE `+where, toAny(args)...)
	if err != nil {
		return map[string]string{}
	}
	defer rows.Close()
	byJID := map[string]string{}
	byPhone := map[string]string{}
	for rows.Next() {
		var jid, phone, full, business, push, first sql.NullString
		if rows.Scan(&jid, &phone, &full, &business, &push, &first) != nil {
			continue
		}
		name := realPersonName(jid.String, full.String, business.String, push.String, first.String)
		if name == "" {
			continue
		}
		byJID[jid.String] = name
		if p := strings.TrimSpace(phone.String); p != "" {
			byPhone[p] = name
		}
	}
	out := map[string]string{}
	for ident := range idents {
		if name, ok := byPhone[ident]; ok {
			out[ident] = name
			continue
		}
		for _, jid := range []string{ident + "@lid", ident + "@s.whatsapp.net"} {
			if name, ok := byJID[jid]; ok {
				out[ident] = name
				break
			}
			if sib := lmap[jid]; sib != "" {
				if name, ok := byJID[sib]; ok {
					out[ident] = name
					break
				}
			}
		}
	}
	return out
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func keysOfSet(m map[string]bool) []string { return keysOf(m) }

// --- messages ---

type msgRow struct {
	msgID, chatJID          string
	ts                      int64
	fromMe                  bool
	senderJID, senderName   string
	text, displayText       string
	quotedMsgID, quotedSnd  string
	isForwarded, edited     bool
	mediaType, mediaCaption string
	filename, mimeType      string
	fileLength              int64
	localPath               string
	downloadedAt            int64
	unavailableAt           int64
	revoked                 bool
	// joined columns
	quotedText, quotedDisplay, quotedSenderName, quotedMedia, quotedRealSender string
	locName, locAddress                                                        string
	locLat, locLng                                                             any // float64 or nil
	senderFull, senderPush, senderBusiness, senderFirst                        string
	quotedFull, quotedPush, quotedBusiness, quotedFirst                        string
	pollTarget                                                                 string
}

// pollVoteText is what the parser stores as the body of a PollUpdateMessage.
const pollVoteText = "Poll vote"

func listMessages(store, jid string, limit int, before int64) ([]map[string]any, *helperError) {
	if limit < 1 {
		limit = 1
	}
	if limit > maxMessage {
		limit = maxMessage
	}
	con, he := openDB(store)
	if he != nil {
		return nil, he
	}
	defer con.Close()

	chat, he := requireChat(con, jid)
	if he != nil {
		return nil, he
	}
	isGroup := chat.kind == "group"
	sibs := siblingJIDs(store, jid)
	self := selfJIDs(store)

	query := `
		SELECT m.msg_id, m.chat_jid, m.ts, m.from_me, m.sender_jid, m.sender_name,
		       m.text, m.display_text, m.quoted_msg_id, m.quoted_sender_jid,
		       m.is_forwarded, m.edited, m.media_type, m.media_caption, m.filename,
		       m.mime_type, m.file_length, m.local_path, m.downloaded_at,
		       m.media_unavailable_at, m.revoked,
		       q.text AS quoted_text, q.display_text AS quoted_display,
		       q.sender_name AS quoted_sender_name, q.media_type AS quoted_media,
		       q.sender_jid AS quoted_real_sender,
		       loc.name AS loc_name, loc.address AS loc_address,
		       loc.latitude AS loc_lat, loc.longitude AS loc_lng,
		       sc.full_name AS sender_full, sc.push_name AS sender_push,
		       sc.business_name AS sender_business, sc.first_name AS sender_first,
		       qc.full_name AS quoted_full, qc.push_name AS quoted_push,
		       qc.business_name AS quoted_business, qc.first_name AS quoted_first,
		       pv.poll_msg_id AS poll_target
		FROM messages m
		LEFT JOIN messages q
		  ON q.chat_jid = m.chat_jid AND q.msg_id = m.quoted_msg_id
		LEFT JOIN message_locations loc
		  ON loc.chat_jid = m.chat_jid AND loc.msg_id = m.msg_id
		LEFT JOIN contacts sc ON sc.jid = m.sender_jid
		LEFT JOIN contacts qc ON qc.jid = COALESCE(NULLIF(q.sender_jid, ''), m.quoted_sender_jid)
		LEFT JOIN poll_votes pv ON pv.chat_jid = m.chat_jid AND pv.vote_msg_id = m.msg_id
		WHERE m.chat_jid IN (` + placeholders(len(sibs)) + `)
		  AND IFNULL(m.deleted_for_me, 0) = 0
		  AND IFNULL(m.reaction_to_id, '') = ''
		  %CLAUSE%
		ORDER BY m.ts DESC, m.rowid DESC
		LIMIT ?`
	clause := ""
	args := append([]any{}, toAny(sibs)...)
	if before > 0 {
		clause = "AND m.ts < ?"
		args = append(args, before)
	}
	args = append(args, limit)
	query = strings.Replace(query, "%CLAUSE%", clause, 1)

	rows, err := con.Query(query, args...)
	if err != nil {
		return nil, fail("could not read the local message store")
	}
	var rs []msgRow
	for rows.Next() {
		var r msgRow
		var fromMe, isFwd, edited, revoked sql.NullInt64
		var msgID, chatJ, senderJ, senderN, text, display, quotedID, quotedSnd, mediaType,
			mediaCaption, filename, mimeType, localPath sql.NullString
		var ts, fileLen, downloadedAt, unavailableAt sql.NullInt64
		var quotedText, quotedDisplay, quotedSenderName, quotedMedia, quotedRealSender,
			locName, locAddress, senderFull, senderPush, senderBusiness, senderFirst,
			quotedFull, quotedPush, quotedBusiness, quotedFirst, pollTarget sql.NullString
		var locLat, locLng sql.NullFloat64
		err := rows.Scan(&msgID, &chatJ, &ts, &fromMe, &senderJ, &senderN, &text, &display,
			&quotedID, &quotedSnd, &isFwd, &edited, &mediaType, &mediaCaption, &filename,
			&mimeType, &fileLen, &localPath, &downloadedAt, &unavailableAt, &revoked,
			&quotedText, &quotedDisplay, &quotedSenderName, &quotedMedia, &quotedRealSender,
			&locName, &locAddress, &locLat, &locLng,
			&senderFull, &senderPush, &senderBusiness, &senderFirst,
			&quotedFull, &quotedPush, &quotedBusiness, &quotedFirst, &pollTarget)
		if err != nil {
			continue
		}
		r = msgRow{
			msgID: msgID.String, chatJID: chatJ.String, ts: ts.Int64, fromMe: fromMe.Int64 != 0,
			senderJID: senderJ.String, senderName: senderN.String, text: text.String,
			displayText: display.String, quotedMsgID: quotedID.String, quotedSnd: quotedSnd.String,
			isForwarded: isFwd.Int64 != 0, edited: edited.Int64 != 0, mediaType: mediaType.String,
			mediaCaption: mediaCaption.String, filename: filename.String, mimeType: mimeType.String,
			fileLength: fileLen.Int64, localPath: localPath.String, downloadedAt: downloadedAt.Int64,
			unavailableAt: unavailableAt.Int64, revoked: revoked.Int64 != 0,
			quotedText: quotedText.String, quotedDisplay: quotedDisplay.String,
			quotedSenderName: quotedSenderName.String, quotedMedia: quotedMedia.String,
			quotedRealSender: quotedRealSender.String, locName: locName.String,
			locAddress: locAddress.String, locLat: nullableFloat(locLat), locLng: nullableFloat(locLng),
			senderFull: senderFull.String, senderPush: senderPush.String,
			senderBusiness: senderBusiness.String, senderFirst: senderFirst.String,
			quotedFull: quotedFull.String, quotedPush: quotedPush.String,
			quotedBusiness: quotedBusiness.String, quotedFirst: quotedFirst.String,
			pollTarget: pollTarget.String,
		}
		rs = append(rs, r)
	}
	rows.Close()

	ids := make([]string, len(rs))
	for i := range rs {
		ids[i] = rs[i].msgID
	}
	reactions := collectReactions(con, sibs, ids)
	mentionSet := map[string]bool{}
	for i := range rs {
		r := &rs[i]
		for id := range mentionIDsIn(r.text, r.displayText, r.mediaCaption, r.quotedText, r.quotedDisplay) {
			mentionSet[id] = true
		}
	}
	mentionNames := loadMentionNames(store, keysOf(mentionSet), con)
	cache := loadLinkCache()

	items := []map[string]any{}
	var pendingReax []map[string]any
	pendingSender := ""
	pendingAlbum := false
	pendingAlbumID := ""
	// Album containers are stub rows immediately followed by their photos from
	// the same sender. Content-less stubs with no photo behind them are genuine
	// unsupported message types (e.g. PayPal templates): render a placeholder
	// bubble instead of dropping them, or the thread shows up empty.
	chrono := reverseRows(rs)
	albumStubs := map[string]bool{}
	for i, stub := range chrono {
		labeled := albumCount(firstNonEmpty(stub.text, stub.displayText))
		if labeled == 0 && (stub.mediaType != "" || stub.text != "" || stub.mediaCaption != "" ||
			stub.locName != "" || stub.revoked || stub.quotedMsgID != "") {
			continue
		}
		for _, later := range chrono[i+1:] {
			if later.senderJID != stub.senderJID {
				continue
			}
			if later.ts-stub.ts <= 120 && later.mediaType != "" {
				albumStubs[stub.msgID] = true
			}
			break
		}
	}
	for i := len(rs) - 1; i >= 0; i-- {
		row := &rs[i]
		kind := mediaKind(row.mediaType, row.mimeType)
		text, caption := visibleBody(row.displayText, row.text, row.mediaCaption, kind)
		text = applyMentionNames(text, mentionNames)
		caption = applyMentionNames(caption, mentionNames)
		if row.revoked {
			kind = "revoked"
			text = "This message was deleted"
			caption = ""
		}
		// A voter who changes their pick sends a second vote message; poll_votes
		// only keeps the live one, so older vote rows have no poll to point at.
		if strings.TrimSpace(row.text) == pollVoteText && row.pollTarget == "" {
			continue
		}
		local := strings.TrimSpace(row.localPath)
		if local == "" || !fileExists(local) {
			local = cachedMedia(row.chatJID, row.msgID)
		}
		quoted := strings.TrimSpace(firstNonEmpty(row.quotedDisplay, row.quotedText))
		quotedKind := "text"
		if row.quotedMedia != "" {
			quotedKind = mediaKind(row.quotedMedia, "")
		}
		quotedBody, _ := visibleBody(quoted, row.quotedText, "", quotedKind)
		quoted = firstNonEmpty(quotedBody, placeholderLabel(quoted), quoted)
		if quoted == "" && row.quotedMedia != "" {
			quoted = firstNonEmpty(placeholderLabel(row.quotedMedia), quotedKind)
		}
		quoted = applyMentionNames(quoted, mentionNames)
		senderJid := row.senderJID
		quotedJid := firstNonEmpty(row.quotedRealSender, row.quotedSnd)
		labeled := albumCount(firstNonEmpty(row.text, row.displayText))
		// WhatsApp album containers are stored as stub rows ([Album: N images]
		// or a content-less "(message)"); they'd render as blank/[Album...]
		// bubbles. Drop them and carry any reaction onto the first photo.
		// In groups the same empty stub is usually an edit envelope
		// (secretEncryptedMessage) or sender-key distribution — official
		// WhatsApp does not show a bubble. Keep a placeholder in DMs so
		// template-only threads (PayPal) are not blank.
		if labeled > 0 || (text == "" && caption == "" && row.mediaType == "" && row.locName == "" && !row.revoked && row.quotedMsgID == "") {
			if albumStubs[row.msgID] || len(reactions[row.msgID]) > 0 {
				pendingReax = append(pendingReax, reactions[row.msgID]...)
				pendingSender = senderJid
				if albumStubs[row.msgID] {
					pendingAlbum = true
					pendingAlbumID = row.msgID
				}
				continue
			}
			if labeled > 0 || isGroup {
				continue
			}
			text = "Unsupported message"
		}
		rowReax := reactions[row.msgID]
		if len(pendingReax) > 0 && senderJid == pendingSender {
			rowReax = coalesceReactions(append(append([]map[string]any{}, pendingReax...), rowReax...))
			pendingReax = nil
		}
		tagAlbum := false
		albumID := ""
		if pendingAlbum && senderJid == pendingSender {
			tagAlbum = albumMedia[kind]
			if tagAlbum {
				albumID = pendingAlbumID
			}
			pendingAlbum = false
			pendingAlbumID = ""
		}
		preview := attachLinkPreview(firstNonEmpty(text, caption), cache)
		extra := map[string]any{}
		if tagAlbum {
			extra["_album"] = true
			if albumID != "" {
				extra["albumId"] = albumID
			}
		}
		senderName := ""
		if senderJid != "" {
			senderName = pickName(senderJid, row.senderName, row.senderFull, row.senderBusiness, row.senderPush, row.senderFirst)
		} else {
			senderName = strings.TrimSpace(row.senderName)
		}
		quotedSender := ""
		if row.quotedSenderName != "" || quotedJid != "" {
			quotedSender = pickName(quotedJid, row.quotedSenderName, row.quotedFull, row.quotedBusiness, row.quotedPush, row.quotedFirst)
		}
		// A group row whose sender is another participant is not ours, whatever
		// from_me says: WhatsApp's revoke/edit keys are relative to the account
		// that sent them, so an older sync could have flipped the flag.
		fromMe := row.fromMe
		if fromMe && isGroup && len(self) > 0 && senderJid != "" &&
			senderJid != row.chatJID && !self[bareJID(senderJid)] {
			fromMe = false
		}
		item := map[string]any{
			"id":              row.msgID,
			"chatJid":         jid, // canonical: siblings (@lid) fold into the requested chat
			"ts":              row.ts,
			"fromMe":          fromMe,
			"senderJid":       senderJid,
			"senderName":      senderName,
			"text":            text,
			"caption":         caption,
			"quotedId":        row.quotedMsgID,
			"quotedSender":    quotedSender,
			"quotedText":      truncate(quoted, 280),
			"forwarded":       row.isForwarded,
			"edited":          row.edited,
			"filename":        strings.TrimSpace(row.filename),
			"mimeType":        strings.TrimSpace(row.mimeType),
			"fileLength":      row.fileLength,
			"localPath":       local,
			"fileUrl":         fileURLOk(local),
			"thumbUrl":        "",
			"downloaded":      local != "" && fileExists(local),
			"unavailable":     row.unavailableAt != 0,
			"reactions":       rowReaxOrEmpty(rowReax),
			"myReaction":      myReaction(rowReax),
			"locationName":    strings.TrimSpace(row.locName),
			"locationAddress": strings.TrimSpace(row.locAddress),
			"latitude":        row.locLat,
			"longitude":       row.locLng,
			"linkPreview":     preview,
		}
		if row.pollTarget != "" {
			item["pollId"] = row.pollTarget
		}
		if row.mediaType != "" || row.locName != "" || kind == "revoked" {
			item["kind"] = kind
		} else {
			item["kind"] = "text"
		}
		if kind == "video" {
			item["thumbUrl"] = fileURLOk(videoThumb(local))
		}
		for k, v := range extra {
			item[k] = v
		}
		items = append(items, item)
	}
	if len(pendingReax) > 0 && len(items) > 0 { // stub was the newest row: fall back to the prior message
		last := items[len(items)-1]
		lastReax, _ := last["reactions"].([]map[string]any)
		merged := coalesceReactions(append(append([]map[string]any{}, lastReax...), pendingReax...))
		last["reactions"] = merged
		last["myReaction"] = myReaction(merged)
	}
	fillAlbumQuotes(items)
	return foldAlbums(items), nil
}

func rowReaxOrEmpty(r []map[string]any) []map[string]any {
	if r == nil {
		return []map[string]any{}
	}
	return r
}

func myReaction(reax []map[string]any) string {
	for _, e := range reax {
		if mine, _ := e["mine"].(bool); mine {
			e, _ := e["emoji"].(string)
			return e
		}
	}
	return ""
}

func nullableFloat(v sql.NullFloat64) any {
	if !v.Valid {
		return nil
	}
	return v.Float64
}

func reverseRows(rs []msgRow) []msgRow {
	out := make([]msgRow, len(rs))
	for i := range rs {
		out[len(rs)-1-i] = rs[i]
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// collectReactions maps msg_id -> [{emoji, count, mine, who}], newest reaction
// per sender wins.
func collectReactions(con *sql.DB, jids, ids []string) map[string][]map[string]any {
	if len(ids) == 0 {
		return map[string][]map[string]any{}
	}
	args := append(toAny(jids), toAny(ids)...)
	rows, err := con.Query(`
		SELECT m.reaction_to_id AS tid, m.reaction_emoji AS emoji,
		       m.sender_jid AS sender, m.from_me AS mine,
		       COALESCE(NULLIF(c.full_name, ''), NULLIF(c.push_name, ''),
		                NULLIF(c.first_name, ''), NULLIF(m.sender_name, '')) AS name
		FROM messages m
		LEFT JOIN contacts c ON c.jid = m.sender_jid
		WHERE m.chat_jid IN (`+placeholders(len(jids))+`) AND m.reaction_to_id IN (`+placeholders(len(ids))+`)
		ORDER BY m.ts`, args...)
	if err != nil {
		return map[string][]map[string]any{}
	}
	defer rows.Close()
	type key struct{ tid, sender string }
	latest := map[key][3]any{}
	var order []key
	for rows.Next() {
		var tid, emoji, sender, name sql.NullString
		var mine sql.NullInt64
		if rows.Scan(&tid, &emoji, &sender, &mine, &name) != nil {
			continue
		}
		s := sender.String
		if s == "" && mine.Int64 != 0 {
			s = "me"
		}
		k := key{tid.String, s}
		if _, seen := latest[k]; !seen {
			order = append(order, k)
		}
		who := ""
		if mine.Int64 != 0 {
			who = "You"
		} else {
			who = strings.TrimSpace(name.String)
		}
		latest[k] = [3]any{strings.TrimSpace(emoji.String), mine.Int64 != 0, who}
	}
	out := map[string][]map[string]any{}
	for _, k := range order {
		v := latest[k]
		emoji, _ := v[0].(string)
		mine, _ := v[1].(bool)
		who, _ := v[2].(string)
		if emoji == "" {
			continue
		}
		bucket := out[k.tid]
		found := false
		for _, entry := range bucket {
			if entry["emoji"] == emoji {
				entry["count"] = entry["count"].(int) + 1
				if mine {
					entry["mine"] = true
				}
				if who != "" {
					entry["who"] = append(entry["who"].([]string), who)
				}
				found = true
				break
			}
		}
		if !found {
			whoList := []string{}
			if who != "" {
				whoList = append(whoList, who)
			}
			out[k.tid] = append(bucket, map[string]any{
				"emoji": emoji, "count": 1, "mine": mine, "who": whoList,
			})
		}
	}
	for tid, bucket := range out {
		sort.SliceStable(bucket, func(i, j int) bool {
			return bucket[i]["count"].(int) > bucket[j]["count"].(int)
		})
		out[tid] = bucket
	}
	return out
}

// coalesceReactions merges reaction buckets that share an emoji (album stub
// folded into a photo).
func coalesceReactions(entries []map[string]any) []map[string]any {
	out := map[string]map[string]any{}
	var order []string
	for _, e := range entries {
		emoji, _ := e["emoji"].(string)
		cur, ok := out[emoji]
		if ok {
			cur["count"] = cur["count"].(int) + intValue(e["count"])
			if mine, _ := e["mine"].(bool); mine {
				cur["mine"] = true
			}
			if who, ok := e["who"].([]string); ok {
				cur["who"] = append(cur["who"].([]string), who...)
			}
		} else {
			clone := map[string]any{
				"emoji": emoji, "count": intValue(e["count"]),
				"mine": e["mine"], "who": append([]string{}, e["who"].([]string)...),
			}
			out[emoji] = clone
			order = append(order, emoji)
		}
	}
	var sorted []map[string]any
	for _, emoji := range order {
		sorted = append(sorted, out[emoji])
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i]["count"].(int) > sorted[j]["count"].(int)
	})
	return sorted
}

func intValue(v any) int {
	if n, ok := v.(int); ok {
		return n
	}
	if n, ok := v.(int64); ok {
		return int(n)
	}
	return 0
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// --- album folding ---

type albumItem struct {
	id, kind    string
	ts          int64
	fileURL     string
	thumbURL    string
	localPath   string
	downloaded  bool
	unavailable bool
	filename    string
	mimeType    string
	fileLength  int64
}

func asAlbumItem(m map[string]any) albumItem {
	str := func(k string) string { s, _ := m[k].(string); return s }
	return albumItem{
		id: str("id"), kind: str("kind"),
		ts: int64Value(m["ts"]), fileURL: str("fileUrl"), thumbURL: str("thumbUrl"),
		localPath: str("localPath"), downloaded: boolValue(m["downloaded"]),
		unavailable: boolValue(m["unavailable"]), filename: str("filename"),
		mimeType: str("mimeType"), fileLength: int64Value(m["fileLength"]),
	}
}

func albumItemMap(a albumItem) map[string]any {
	return map[string]any{
		"id": a.id, "kind": a.kind, "ts": a.ts, "fileUrl": a.fileURL,
		"thumbUrl": a.thumbURL, "localPath": a.localPath, "downloaded": a.downloaded,
		"unavailable": a.unavailable, "filename": a.filename, "mimeType": a.mimeType,
		"fileLength": a.fileLength,
	}
}

func int64Value(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

func boolValue(v any) bool {
	b, _ := v.(bool)
	return b
}

// foldAlbums collapses consecutive album media into one bubble, WhatsApp-style.
//
// A photo tagged `_album` (it followed an album stub) starts a group; later
// tiles from the same sender within 3s join it. Untagged bursts stay separate
// so two photos sent one-after-another don't merge.
// ponytail: 3s is enough for a 4-image burst; widen if a real album splits.
func foldAlbums(items []map[string]any) []map[string]any {
	var out []map[string]any
	i := 0
	for i < len(items) {
		first := items[i]
		tagged, _ := first["_album"].(bool)
		delete(first, "_album")
		kind, _ := first["kind"].(string)
		if !tagged || !albumMedia[kind] {
			out = append(out, first)
			i++
			continue
		}
		members := []map[string]any{first}
		j := i + 1
		for j < len(items) {
			nxt := items[j]
			prev := members[len(members)-1]
			nk, _ := nxt["kind"].(string)
			if !albumMedia[nk] {
				break
			}
			if tag, _ := nxt["_album"].(bool); tag {
				break
			}
			if q, _ := nxt["quotedId"].(string); q != "" {
				break
			}
			ns, _ := nxt["senderJid"].(string)
			fs, _ := first["senderJid"].(string)
			if ns != fs {
				break
			}
			nfm, _ := nxt["fromMe"].(bool)
			ffm, _ := first["fromMe"].(bool)
			if nfm != ffm {
				break
			}
			if int64Value(nxt["ts"])-int64Value(prev["ts"]) > 3 {
				break
			}
			delete(nxt, "_album")
			members = append(members, nxt)
			j++
		}
		if len(members) == 1 {
			out = append(out, first)
			i = j
			continue
		}
		merged := first
		var album []map[string]any
		for _, m := range members {
			album = append(album, albumItemMap(asAlbumItem(m)))
		}
		capText := ""
		for _, m := range members {
			capText = strings.TrimSpace(firstNonEmpty(strOr(m["caption"]), strOr(m["text"])))
			if capText != "" {
				break
			}
		}
		var reax []map[string]any
		for _, m := range members {
			if r, ok := m["reactions"].([]map[string]any); ok {
				reax = append(reax, r...)
			}
		}
		merged["album"] = album
		merged["caption"] = capText
		merged["text"] = capText
		merged["reactions"] = coalesceReactions(reax)
		merged["myReaction"] = myReaction(merged["reactions"].([]map[string]any))
		all := true
		for _, m := range members {
			if !boolValue(m["downloaded"]) {
				all = false
			}
		}
		merged["downloaded"] = all
		out = append(out, merged)
		i = j
	}
	return out
}

func strOr(v any) string {
	s, _ := v.(string)
	return s
}

// fillAlbumQuotes: quoted album stubs otherwise preview as 'Photo'; use the
// album caption.
func fillAlbumQuotes(items []map[string]any) {
	byID := map[string]map[string]any{}
	for _, m := range items {
		id := strOr(m["id"])
		byID[id] = m
		if albumID := strOr(m["albumId"]); albumID != "" {
			byID[albumID] = m
		}
		if album, ok := m["album"].([]map[string]any); ok {
			for _, piece := range album {
				byID[strOr(piece["id"])] = m
			}
		}
	}
	for _, m := range items {
		target := byID[strOr(m["quotedId"])]
		if target == nil {
			continue
		}
		capText := strings.TrimSpace(firstNonEmpty(strOr(target["caption"]), strOr(target["text"])))
		if capText == "" {
			continue
		}
		qt := strOr(m["quotedText"])
		if qt == "" || qt == "Photo" || qt == "Video" || qt == "GIF" {
			m["quotedText"] = truncate(capText, 280)
		}
	}
}

// --- participants ---

func listParticipants(store, jid string) ([]map[string]any, *helperError) {
	con, he := openDB(store)
	if he != nil {
		return nil, he
	}
	defer con.Close()
	chat, he := requireChat(con, jid)
	if he != nil {
		return nil, he
	}
	if chat.kind != "group" {
		return []map[string]any{}, nil
	}
	rows, err := con.Query(`
		SELECT p.user_jid, p.role,
		       COALESCE(NULLIF(c.full_name, ''), NULLIF(c.push_name, ''),
		                NULLIF(c.first_name, ''), p.user_jid) AS name
		FROM group_participants p
		LEFT JOIN contacts c ON c.jid = p.user_jid
		WHERE p.group_jid = ?
		ORDER BY name COLLATE NOCASE
		LIMIT ?`, jid, maxPartic)
	if err != nil {
		return nil, fail("could not read the local message store")
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var userJID, role, name string
		if rows.Scan(&userJID, &role, &name) != nil {
			continue
		}
		out = append(out, map[string]any{
			"jid":  userJID,
			"name": pickName(userJID, name),
			"role": role,
		})
	}
	return out, nil
}

func validateMentions(store, chatJID string, mentions []string) ([]string, *helperError) {
	if len(mentions) == 0 {
		return nil, nil
	}
	participants, he := listParticipants(store, chatJID)
	if he != nil {
		return nil, he
	}
	allowed := map[string]bool{}
	for _, p := range participants {
		allowed[p["jid"].(string)] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, raw := range mentions {
		jid := strings.TrimSpace(raw)
		if !jidRe.MatchString(jid) {
			return nil, fail("chat target is not a valid JID")
		}
		if !allowed[jid] {
			return nil, fail("mention is not a member of this group")
		}
		if !seen[jid] {
			seen[jid] = true
			out = append(out, jid)
		}
	}
	return out, nil
}

var _ = time.Now
