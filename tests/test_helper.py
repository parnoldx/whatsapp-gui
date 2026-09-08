#!/usr/bin/env python3
import importlib.machinery
import importlib.util
import json
import os
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
HELPER = ROOT / "helper" / "whatsapp"


def load_helper():
    loader = importlib.machinery.SourceFileLoader("pa_whatsapp_helper", str(HELPER))
    spec = importlib.util.spec_from_loader(loader.name, loader)
    mod = importlib.util.module_from_spec(spec)
    loader.exec_module(mod)
    return mod


def run_helper(args, env, check=False):
    proc = subprocess.run(
        [sys.executable, "-B", str(HELPER), *args],
        capture_output=True,
        text=True,
        env=env,
        check=False,
    )
    payload = json.loads(proc.stdout)
    if check and proc.returncode != 0:
        raise AssertionError(payload)
    return proc.returncode, payload


SCHEMA = """
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
CREATE TABLE message_locations (
  chat_jid TEXT, msg_id TEXT, latitude REAL, longitude REAL,
  name TEXT, address TEXT, is_live INTEGER
);
"""


class HelperTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.store = Path(self.tmp.name) / "store"
        self.state = Path(self.tmp.name) / "state"
        self.store.mkdir()
        self.state.mkdir()
        self.env = os.environ.copy()
        self.env["WACLI_STORE_DIR"] = str(self.store)
        self.env["PA_WHATSAPP_STATE"] = str(self.state)
        self.env["WACLI_BIN"] = "/usr/bin/false"
        os.environ["WACLI_STORE_DIR"] = str(self.store)
        os.environ["PA_WHATSAPP_STATE"] = str(self.state)
        self.db = self.store / "wacli.db"
        con = sqlite3.connect(self.db)
        con.executescript(SCHEMA)
        con.execute(
            "INSERT INTO chats VALUES (?,?,?,?,?,?,?,?,?)",
            ("111@s.whatsapp.net", "dm", "Ada", 100, 0, 1, 0, 1, 2),
        )
        con.execute(
            "INSERT INTO chats VALUES (?,?,?,?,?,?,?,?,?)",
            ("222@g.us", "group", "Crew", 200, 0, 0, -1, 1, 3),
        )
        con.execute(
            "INSERT INTO chats VALUES (?,?,?,?,?,?,?,?,?)",
            ("333@newsletter", "unknown", "News", 300, 0, 0, 0, 0, 9),
        )
        con.execute(
            "INSERT INTO chats VALUES (?,?,?,?,?,?,?,?,?)",
            ("444@g.us", "group", "Archived", 50, 1, 0, 0, 0, 1),
        )
        con.execute(
            "INSERT INTO groups VALUES (?,?,?,?,?,?,?,?)",
            ("222@g.us", "Crew", "111@s.whatsapp.net", 1, 0, "", 0, 1),
        )
        con.execute(
            "INSERT INTO group_participants VALUES (?,?,?,?)",
            ("222@g.us", "555@s.whatsapp.net", "admin", 1),
        )
        con.execute(
            "INSERT INTO contacts VALUES (?,?,?,?,?,?,?,?)",
            ("555@s.whatsapp.net", "555", "Sam", "Sam Stone", "Sam", "", "", 1),
        )
        con.execute(
            """INSERT INTO messages (
                 chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me,
                 text, display_text, quoted_msg_id, quoted_sender_jid, is_forwarded,
                 forwarding_score, reaction_to_id, reaction_emoji, media_type,
                 media_caption, filename, mime_type, file_length, local_path,
                 downloaded_at, media_unavailable_at, revoked, deleted_for_me,
                 edited, buttons
               ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
            (
                "111@s.whatsapp.net", "Ada", "m1", "111@s.whatsapp.net", "Ada", 100, 0,
                "hello https://example.com", "hello https://example.com", "", "", 0,
                0, "", "", "", "", "", "", 0, "", 0, 0, 0, 0, 0, "",
            ),
        )
        con.execute(
            """INSERT INTO messages (
                 chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me,
                 text, display_text, quoted_msg_id, quoted_sender_jid, is_forwarded,
                 forwarding_score, reaction_to_id, reaction_emoji, media_type,
                 media_caption, filename, mime_type, file_length, local_path,
                 downloaded_at, media_unavailable_at, revoked, deleted_for_me,
                 edited, buttons
               ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
            (
                "111@s.whatsapp.net", "Ada", "m2", "me", "Me", 90, 1,
                "gone", "gone", "", "", 0, 0, "", "", "", "", "", "", 0, "",
                0, 0, 0, 1, 0, "",
            ),
        )
        con.commit()
        con.close()
        self.mod = load_helper()

    def tearDown(self):
        self.tmp.cleanup()
        for key in ("WACLI_STORE_DIR", "PA_WHATSAPP_STATE"):
            if key in self.env:
                pass

    def test_lists_only_dm_and_standalone_groups(self):
        code, payload = run_helper(["chats", "--limit", "50"], self.env)
        self.assertEqual(code, 0)
        self.assertTrue(payload["ok"])
        chats = payload["data"]["chats"]
        kinds = {c["kind"] for c in chats}
        names = {c["name"] for c in chats}
        self.assertEqual(kinds, {"dm", "group"})
        self.assertIn("Ada", names)
        self.assertIn("Crew", names)
        self.assertNotIn("News", names)
        self.assertNotIn("Archived", names)
        crew = next(c for c in chats if c["isGroup"])
        self.assertTrue(crew["muted"])
        ada = next(c for c in chats if not c["isGroup"])
        self.assertEqual(ada["unreadCount"], 2)
        self.assertEqual(ada["preview"], "hello https://example.com")
        self.assertIn("syncActive", payload["data"])
        self.assertIsInstance(payload["data"]["syncActive"], bool)

    def test_search_filters_without_touching_wacli(self):
        code, payload = run_helper(["chats", "--query", "crew"], self.env)
        self.assertEqual(code, 0)
        chats = payload["data"]["chats"]
        self.assertEqual(len(chats), 1)
        self.assertEqual(chats[0]["name"], "Crew")

    def test_messages_skip_deleted_and_expose_links(self):
        code, payload = run_helper(["messages", "--chat", "111@s.whatsapp.net"], self.env)
        self.assertEqual(code, 0)
        messages = payload["data"]["messages"]
        self.assertEqual(len(messages), 1)
        self.assertEqual(messages[0]["id"], "m1")
        self.assertIn("https://example.com", messages[0]["text"])
        self.assertEqual(messages[0]["kind"], "text")

    def test_rejects_unknown_kind_and_bad_jid(self):
        code, payload = run_helper(["messages", "--chat", "333@newsletter"], self.env)
        self.assertNotEqual(code, 0)
        self.assertFalse(payload["ok"])
        code, payload = run_helper(["messages", "--chat", "not-a-jid"], self.env)
        self.assertNotEqual(code, 0)

    def test_daemon_survives_bad_input_and_answers_each_line(self):
        proc = subprocess.run(
            [sys.executable, "-B", str(HELPER), "--daemon"],
            input='["chats","--limit","5"]\nnot json\n["bogus"]\n["chats","--limit","5"]\n',
            capture_output=True,
            text=True,
            env=self.env,
            check=False,
        )
        lines = [json.loads(x) for x in proc.stdout.splitlines() if x.strip()]
        self.assertEqual(len(lines), 4)
        self.assertTrue(lines[0]["ok"])
        self.assertEqual(lines[1], {"ok": False, "error": "bad command"})
        self.assertEqual(lines[2], {"ok": False, "error": "bad command"})
        self.assertTrue(lines[3]["ok"])  # loop still healthy after two bad lines

    def test_participants_resolve_contact_names(self):
        code, payload = run_helper(["participants", "--chat", "222@g.us"], self.env)
        self.assertEqual(code, 0)
        people = payload["data"]["participants"]
        self.assertEqual(people[0]["name"], "Sam Stone")
        self.assertEqual(people[0]["jid"], "555@s.whatsapp.net")

    def test_mentions_must_be_group_members(self):
        with self.assertRaises(self.mod.HelperError):
            self.mod.validate_mentions(self.store, "222@g.us", ["999@s.whatsapp.net"])
        got = self.mod.validate_mentions(self.store, "222@g.us", ["555@s.whatsapp.net"])
        self.assertEqual(got, ["555@s.whatsapp.net"])

    def test_ack_and_badge(self):
        chats = self.mod.list_chats(self.store)
        prefs = {"acks": {}, "receipts": False}
        self.assertEqual(self.mod.badge_count(chats, prefs), 1)  # Ada unread, Crew muted
        run_helper(["ack", "--chat", "111@s.whatsapp.net", "--ts", "100"], self.env)
        prefs = self.mod.load_prefs()
        self.assertEqual(prefs["acks"]["111@s.whatsapp.net"], 100)
        self.assertEqual(self.mod.badge_count(chats, prefs), 0)

    def test_file_must_be_absolute_regular(self):
        with self.assertRaises(self.mod.HelperError):
            self.mod.validate_file("relative.png")
        with self.assertRaises(self.mod.HelperError):
            self.mod.validate_file("/no/such/file.bin")

    def test_ogg_opus_detection(self):
        path = Path(self.tmp.name) / "note.ogg"
        path.write_bytes(b"OggS" + b"\x00" * 20 + b"OpusHead" + b"\x00" * 8)
        self.assertTrue(self.mod.is_ogg_opus(path))
        path.write_bytes(b"not audio")
        self.assertFalse(self.mod.is_ogg_opus(path))

    def test_media_kind(self):
        self.assertEqual(self.mod.media_kind("image", "image/jpeg"), "image")
        self.assertEqual(self.mod.media_kind("audio", "audio/ogg; codecs=opus"), "voice")
        self.assertEqual(self.mod.media_kind("video", "video/mp4"), "video")
        self.assertEqual(self.mod.media_kind("", ""), "text")

    def test_lock_error_is_human(self):
        msg = self.mod.humanize_wacli_error(
            "lock after 15s: store is locked (another wacli is running?)"
        )
        self.assertEqual(msg, "WhatsApp is busy syncing. Try again in a moment.")
        self.assertNotIn("success", msg.lower())

    def test_download_uses_readonly_output_while_unlocked_copy_missing(self):
        import sqlite3
        con = sqlite3.connect(self.db)
        con.execute(
            """INSERT INTO messages (
                 chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me,
                 text, display_text, quoted_msg_id, quoted_sender_jid, is_forwarded,
                 forwarding_score, reaction_to_id, reaction_emoji, media_type,
                 media_caption, filename, mime_type, file_length, local_path,
                 downloaded_at, media_unavailable_at, revoked, deleted_for_me,
                 edited, buttons
               ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
            (
                "111@s.whatsapp.net", "Ada", "img1", "111@s.whatsapp.net", "Ada", 110, 0,
                "", "", "", "", 0, 0, "", "", "image", "", "pic.jpg", "image/jpeg",
                12, "", 0, 0, 0, 0, 0, "",
            ),
        )
        con.commit()
        con.close()
        calls = []

        def fake_run(args, store=None, timeout=60, readonly=False):
            calls.append({"args": args, "readonly": readonly})
            dest = Path(args[args.index("--output") + 1])
            dest.write_bytes(b"\xff\xd8\xff\xd9")
            return {}

        self.mod.run_wacli = fake_run
        result = self.mod.download_media(self.store, "111@s.whatsapp.net", "img1")
        self.assertTrue(calls[0]["readonly"])
        self.assertIn("--output", calls[0]["args"])
        self.assertNotIn("lock-wait", " ".join(calls[0]["args"]))
        self.assertTrue(Path(result["localPath"]).is_file())
        self.assertTrue(result["fileUrl"].startswith("file:"))

    def test_linkify_js_contract_via_model(self):
        # sanity that helper preview does not strip URLs
        chats = self.mod.list_chats(self.store)
        ada = next(c for c in chats if c["name"] == "Ada")
        self.assertIn("https://example.com", ada["preview"])

    def test_names_prefer_contacts_and_group_table(self):
        con = sqlite3.connect(self.db)
        con.execute(
            "INSERT INTO chats VALUES (?,?,?,?,?,?,?,?,?)",
            ("777@s.whatsapp.net", "dm", "777@s.whatsapp.net", 80, 0, 0, 0, 0, 1),
        )
        con.execute(
            "INSERT INTO contacts VALUES (?,?,?,?,?,?,?,?)",
            ("777@s.whatsapp.net", "777", "Kev", "Kevin Bernthaler", "Kevin", "", "", 1),
        )
        con.execute(
            "INSERT INTO chats VALUES (?,?,?,?,?,?,?,?,?)",
            ("666@g.us", "group", "666@g.us", 70, 0, 0, 0, 0, 0),
        )
        con.execute(
            "INSERT INTO groups VALUES (?,?,?,?,?,?,?,?)",
            ("666@g.us", "Foodpornisten", "", 1, 0, "", 0, 1),
        )
        con.commit()
        con.close()
        chats = {c["jid"]: c for c in self.mod.list_chats(self.store, include_archived=True)}
        self.assertEqual(chats["777@s.whatsapp.net"]["name"], "Kevin Bernthaler")
        self.assertEqual(chats["666@g.us"]["name"], "Foodpornisten")

    def test_sender_name_falls_back_to_contacts(self):
        con = sqlite3.connect(self.db)
        con.execute(
            """INSERT INTO messages (
                 chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me,
                 text, display_text, quoted_msg_id, quoted_sender_jid, is_forwarded,
                 forwarding_score, reaction_to_id, reaction_emoji, media_type,
                 media_caption, filename, mime_type, file_length, local_path,
                 downloaded_at, media_unavailable_at, revoked, deleted_for_me,
                 edited, buttons
               ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
            (
                "222@g.us", "Crew", "m3", "555@s.whatsapp.net", "", 80, 0,
                "yo", "yo", "", "", 0, 0, "", "", "", "", "", "", 0, "",
                0, 0, 0, 0, 0, "",
            ),
        )
        con.commit()
        con.close()
        messages = self.mod.list_messages(self.store, "222@g.us")
        self.assertEqual(messages[0]["senderName"], "Sam Stone")

    def test_sent_image_placeholder_is_not_a_caption(self):
        con = sqlite3.connect(self.db)
        con.execute(
            """INSERT INTO messages (
                 chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me,
                 text, display_text, quoted_msg_id, quoted_sender_jid, is_forwarded,
                 forwarding_score, reaction_to_id, reaction_emoji, media_type,
                 media_caption, filename, mime_type, file_length, local_path,
                 downloaded_at, media_unavailable_at, revoked, deleted_for_me,
                 edited, buttons
               ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
            (
                "111@s.whatsapp.net", "Ada", "img2", "111@s.whatsapp.net", "Ada", 120, 0,
                "", "Sent image", "", "", 0, 0, "", "", "image", "", "pic.jpg", "image/jpeg",
                12, "", 0, 0, 0, 0, 0, "",
            ),
        )
        con.commit()
        con.close()
        messages = {m["id"]: m for m in self.mod.list_messages(self.store, "111@s.whatsapp.net")}
        self.assertEqual(messages["img2"]["kind"], "image")
        self.assertEqual(messages["img2"]["text"], "")
        self.assertEqual(messages["img2"]["caption"], "")
        chats = {c["jid"]: c for c in self.mod.list_chats(self.store)}
        self.assertEqual(chats["111@s.whatsapp.net"]["preview"], "Photo")

    def test_ack_clears_listed_unread(self):
        run_helper(["ack", "--chat", "111@s.whatsapp.net", "--ts", "100"], self.env)
        chats = {c["jid"]: c for c in self.mod.list_chats(self.store)}
        self.assertEqual(chats["111@s.whatsapp.net"]["unreadCount"], 0)

    def test_read_on_other_device_clears_unread(self):
        # wacli's `unread` flag went to 0 (read on the phone) but the running
        # count is stale and the GUI never opened this chat.
        con = sqlite3.connect(self.db)
        con.execute(
            "UPDATE chats SET unread = 0, unread_count = 7 WHERE jid = ?",
            ("111@s.whatsapp.net",),
        )
        con.commit()
        con.close()
        chats = {c["jid"]: c for c in self.mod.list_chats(self.store)}
        self.assertEqual(chats["111@s.whatsapp.net"]["unreadCount"], 0)

    def test_unread_counts_only_messages_after_ack(self):
        con = sqlite3.connect(self.db)
        con.execute(
            "UPDATE chats SET unread_count = 36, last_message_ts = 100 WHERE jid = ?",
            ("111@s.whatsapp.net",),
        )
        con.commit()
        con.close()
        run_helper(["ack", "--chat", "111@s.whatsapp.net", "--ts", "90"], self.env)
        chats = {c["jid"]: c for c in self.mod.list_chats(self.store)}
        self.assertEqual(chats["111@s.whatsapp.net"]["unreadCount"], 1)

    def test_mark_read_uses_chat_flag(self):
        calls = []

        def fake_run(args, store=None, timeout=60, readonly=False, lock_wait="15s"):
            calls.append({"args": args, "lock_wait": lock_wait})
            return {}

        self.mod.run_wacli = fake_run
        self.mod.cmd_mark_read(self.store, "111@s.whatsapp.net")
        self.assertEqual(calls[0]["args"][:2], ["chats", "mark-read"])
        self.assertIn("--chat", calls[0]["args"])
        self.assertNotIn("--jid", calls[0]["args"])
        self.assertEqual(calls[0]["lock_wait"], "0s")

    def test_instagram_reel_gets_a_link_preview(self):
        con = sqlite3.connect(self.db)
        con.execute(
            """INSERT INTO messages (
                 chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me,
                 text, display_text, quoted_msg_id, quoted_sender_jid, is_forwarded,
                 forwarding_score, reaction_to_id, reaction_emoji, media_type,
                 media_caption, filename, mime_type, file_length, local_path,
                 downloaded_at, media_unavailable_at, revoked, deleted_for_me,
                 edited, buttons
               ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
            (
                "111@s.whatsapp.net", "Ada", "reel1", "111@s.whatsapp.net", "Ada", 130, 0,
                "https://www.instagram.com/reel/Dc66-wqjore/?stkn=abc",
                "https://www.instagram.com/reel/Dc66-wqjore/?stkn=abc",
                "", "", 0, 0, "", "", "", "", "", "", 0, "",
                0, 0, 0, 0, 0, "",
            ),
        )
        con.commit()
        con.close()
        messages = {m["id"]: m for m in self.mod.list_messages(self.store, "111@s.whatsapp.net")}
        preview = messages["reel1"]["linkPreview"]
        self.assertEqual(preview["site"], "Instagram")
        self.assertEqual(preview["label"], "Reel")
        self.assertIn("instagram.com/reel/", preview["url"])

    def test_prune_media_drops_only_old_files(self):
        import time
        media = self.state / "media"
        media.mkdir()
        old = media / "old.jpg"
        new = media / "new.jpg"
        old.write_bytes(b"x")
        new.write_bytes(b"x")
        stale = time.time() - 8 * 86400
        os.utime(old, (stale, stale))
        self.mod.prune_media(days=7)
        self.assertFalse(old.exists())
        self.assertTrue(new.exists())
        # marker gate: a second call within `every` is a no-op even for old files
        os.utime(new, (stale, stale))
        self.mod.prune_media(days=7)
        self.assertTrue(new.exists())

    def test_reactions_aggregate_latest_per_sender(self):
        con = sqlite3.connect(self.db)
        rows = [
            # (msg_id, sender_jid, sender_name, from_me, ts, reaction_to_id, reaction_emoji)
            ("r1", "555@s.whatsapp.net", "Sam", 0, 200, "m1", "😂"),
            ("r2", "666@s.whatsapp.net", "Kim", 0, 201, "m1", "😂"),
            ("r3", "me", "", 1, 202, "m1", "❤️"),
            # Sam changed his mind: latest wins, no double count.
            ("r4", "555@s.whatsapp.net", "Sam", 0, 210, "m1", "❤️"),
            # A cleared reaction drops out entirely.
            ("r5", "666@s.whatsapp.net", "Kim", 0, 220, "m1", ""),
        ]
        for mid, sj, sn, fm, ts, tid, emo in rows:
            con.execute(
                """INSERT INTO messages (
                     chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me,
                     text, display_text, quoted_msg_id, quoted_sender_jid, is_forwarded,
                     forwarding_score, reaction_to_id, reaction_emoji, media_type,
                     media_caption, filename, mime_type, file_length, local_path,
                     downloaded_at, media_unavailable_at, revoked, deleted_for_me,
                     edited, buttons
                   ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
                ("111@s.whatsapp.net", "Ada", mid, sj, sn, ts, fm, "", "", "", "", 0,
                 0, tid, emo, "", "", "", "", 0, "", 0, 0, 0, 0, 0, ""),
            )
        con.commit()
        con.close()
        m1 = next(m for m in self.mod.list_messages(self.store, "111@s.whatsapp.net") if m["id"] == "m1")
        by_emoji = {e["emoji"]: e for e in m1["reactions"]}
        self.assertEqual(by_emoji["❤️"]["count"], 2)  # me + Sam (changed)
        self.assertTrue(by_emoji["❤️"]["mine"])
        self.assertEqual(sorted(by_emoji["❤️"]["who"]), ["Sam Stone", "You"])  # tooltip names, contact-resolved
        self.assertNotIn("😂", by_emoji)  # Sam moved off it, other sender cleared
        self.assertEqual(m1["myReaction"], "❤️")

    def test_react_builds_wacli_args(self):
        calls = []

        def fake_run(args, store=None, timeout=60, readonly=False, lock_wait="15s"):
            calls.append(args)
            return {}

        self.mod.run_wacli = fake_run
        # unknown message id -> nothing sent
        with self.assertRaises(self.mod.HelperError):
            self.mod.send_react(self.store, "222@g.us", "nope", "", "🔥")
        con = sqlite3.connect(self.db)
        con.execute(
            """INSERT INTO messages (
                 chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me,
                 text, display_text, quoted_msg_id, quoted_sender_jid, is_forwarded,
                 forwarding_score, reaction_to_id, reaction_emoji, media_type,
                 media_caption, filename, mime_type, file_length, local_path,
                 downloaded_at, media_unavailable_at, revoked, deleted_for_me,
                 edited, buttons
               ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
            ("222@g.us", "Crew", "gm1", "555@s.whatsapp.net", "Sam", 80, 0,
             "hi", "hi", "", "", 0, 0, "", "", "", "", "", "", 0, "",
             0, 0, 0, 0, 0, ""),
        )
        con.commit()
        con.close()
        self.mod.send_react(self.store, "222@g.us", "gm1", "", "")
        args = calls[-1]
        self.assertEqual(args[:2], ["send", "react"])
        self.assertEqual(args[args.index("--reaction") + 1], "")  # empty clears
        self.assertEqual(args[args.index("--sender") + 1], "555@s.whatsapp.net")

    def test_reaction_from_clipboard_rejects_junk(self):
        self.assertEqual(self.mod.reaction_from_clipboard("🔥\nmore"), "")
        self.assertEqual(self.mod.reaction_from_clipboard("x" * 33), "")
        self.assertEqual(self.mod.reaction_from_clipboard("  ❤️  "), "❤️")

    def test_pick_emoji_returns_clipboard_selection(self):
        state = {"open": False, "clip": "keep-me"}

        def summon():
            state["open"] = True
            state["clip"] = "🔥"

        self.mod._summon_emoji_picker = summon
        self.mod._emoji_overlay_open = lambda: state["open"]
        self.mod._clipboard_text = lambda: state["clip"]
        self.mod._set_clipboard = lambda t: state.update(clip=t)
        self.mod._sleep = lambda s: None
        self.assertEqual(self.mod.cmd_pick_emoji()["emoji"], "🔥")

    def test_pick_emoji_cancel_restores_clipboard(self):
        restored = []
        n = {"i": 0}

        def overlay():
            n["i"] += 1
            return n["i"] < 3

        self.mod._summon_emoji_picker = lambda: None
        self.mod._emoji_overlay_open = overlay
        self.mod._clipboard_text = lambda: "keep-me" if n["i"] == 0 else ""
        self.mod._set_clipboard = lambda t: restored.append(t)
        self.mod._sleep = lambda s: None
        self.assertEqual(self.mod.cmd_pick_emoji()["emoji"], "")
        self.assertIn("keep-me", restored)

    def test_pick_emoji_watch_only_does_not_summon(self):
        summoned = []
        self.mod._summon_emoji_picker = lambda: summoned.append(1)
        self.mod._emoji_overlay_open = lambda: True
        self.mod._clipboard_text = lambda: "🔥"
        self.mod._set_clipboard = lambda t: None
        self.mod._sleep = lambda s: None
        self.assertEqual(self.mod.cmd_pick_emoji(watch_only=True)["emoji"], "🔥")
        self.assertEqual(summoned, [])

    def test_pick_emoji_watch_only_returns_pick_after_overlay_closed(self):
        summoned = []
        self.mod._summon_emoji_picker = lambda: summoned.append(1)
        self.mod._emoji_overlay_open = lambda: False
        self.mod._clipboard_text = lambda: "🎉"
        self.mod._set_clipboard = lambda t: None
        self.mod._sleep = lambda s: None
        self.assertEqual(self.mod.cmd_pick_emoji(watch_only=True)["emoji"], "🎉")
        self.assertEqual(summoned, [])

    def test_lid_chat_surfaces_as_dm(self):
        con = sqlite3.connect(self.db)
        con.execute(
            "INSERT INTO chats VALUES (?,?,?,?,?,?,?,?,?)",
            ("999@lid", "unknown", "Christa", 400, 0, 0, 0, 1, 1),
        )
        con.execute(
            # a nameless empty @lid chat must stay hidden
            "INSERT INTO chats VALUES (?,?,?,?,?,?,?,?,?)",
            ("888@lid", "unknown", "", None, 0, 0, 0, 0, 0),
        )
        con.execute(
            """INSERT INTO messages (
                 chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me,
                 text, display_text, quoted_msg_id, quoted_sender_jid, is_forwarded,
                 forwarding_score, reaction_to_id, reaction_emoji, media_type,
                 media_caption, filename, mime_type, file_length, local_path,
                 downloaded_at, media_unavailable_at, revoked, deleted_for_me,
                 edited, buttons
               ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
            ("999@lid", "Christa", "lm1", "", "", 400, 1,
             "", "", "", "", 0, 0, "", "", "image", "", "p.jpg", "image/jpeg",
             9, "", 0, 0, 0, 0, 0, ""),
        )
        con.commit()
        con.close()
        chats = {c["jid"]: c for c in self.mod.list_chats(self.store)}
        self.assertIn("999@lid", chats)
        self.assertEqual(chats["999@lid"]["kind"], "dm")
        self.assertFalse(chats["999@lid"]["isGroup"])
        self.assertNotIn("888@lid", chats)  # nameless + no messages stays hidden
        msgs = self.mod.list_messages(self.store, "999@lid")
        self.assertEqual(msgs[0]["id"], "lm1")
        self.assertEqual(msgs[0]["kind"], "image")

    def test_lid_and_phone_chats_stitch_into_one_thread(self):
        session = self.store / "session.db"
        scon = sqlite3.connect(session)
        scon.executescript(
            "CREATE TABLE whatsmeow_lid_map (lid TEXT PRIMARY KEY, pn TEXT UNIQUE NOT NULL);"
        )
        scon.execute("INSERT INTO whatsmeow_lid_map VALUES ('700700', '111')")  # 111 == Ada's dm
        scon.commit()
        scon.close()
        con = sqlite3.connect(self.db)
        con.execute(
            "INSERT INTO chats VALUES (?,?,?,?,?,?,?,?,?)",
            ("700700@lid", "unknown", "Ada", 130, 0, 0, 0, 0, 0),
        )
        for mid, ts, kindval in (("lidpic", 125, "image"), ("lidpic2", 128, "image")):
            con.execute(
                """INSERT INTO messages (
                     chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me,
                     text, display_text, quoted_msg_id, quoted_sender_jid, is_forwarded,
                     forwarding_score, reaction_to_id, reaction_emoji, media_type,
                     media_caption, filename, mime_type, file_length, local_path,
                     downloaded_at, media_unavailable_at, revoked, deleted_for_me,
                     edited, buttons
                   ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
                ("700700@lid", "Ada", mid, "", "", ts, 1,
                 "", "", "", "", 0, 0, "", "", kindval, "", "p.jpg", "image/jpeg",
                 9, "", 0, 0, 0, 0, 0, ""),
            )
        con.commit()
        con.close()

        chats = {c["jid"]: c for c in self.mod.list_chats(self.store)}
        self.assertIn("111@s.whatsapp.net", chats)
        self.assertNotIn("700700@lid", chats)  # folded into the phone-number chat
        self.assertEqual(chats["111@s.whatsapp.net"]["lastMessageTs"], 128)  # from the LID side

        msgs = self.mod.list_messages(self.store, "111@s.whatsapp.net")
        ids = [m["id"] for m in msgs]
        self.assertEqual(ids, ["m1", "lidpic", "lidpic2"])  # interleaved by ts
        self.assertTrue(all(m["chatJid"] == "111@s.whatsapp.net" for m in msgs))

        calls = []

        def fake_run(args, store=None, timeout=60, readonly=False, lock_wait="15s"):
            calls.append(args)
            Path(args[args.index("--output") + 1]).write_bytes(b"\xff\xd8\xff\xd9")
            return {}

        self.mod.run_wacli = fake_run
        got = self.mod.download_media(self.store, "111@s.whatsapp.net", "lidpic")
        self.assertEqual(calls[0][calls[0].index("--chat") + 1], "700700@lid")
        self.assertTrue(Path(got["localPath"]).is_file())

    def _tiny_mp4(self, path):
        proc = subprocess.run(
            [
                "ffmpeg", "-y", "-f", "lavfi", "-i", "color=c=red:s=16x16:d=0.2",
                "-pix_fmt", "yuv420p", str(path),
            ],
            capture_output=True,
            check=False,
        )
        if proc.returncode != 0 or not path.is_file():
            self.skipTest("ffmpeg cannot encode a test clip")

    def test_video_thumb_is_first_frame(self):
        self.assertEqual(self.mod.video_thumb("/no/such.mp4"), "")
        mp4 = Path(self.tmp.name) / "clip.mp4"
        self._tiny_mp4(mp4)
        con = sqlite3.connect(self.db)
        con.execute(
            """INSERT INTO messages (
                 chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me,
                 text, display_text, quoted_msg_id, quoted_sender_jid, is_forwarded,
                 forwarding_score, reaction_to_id, reaction_emoji, media_type,
                 media_caption, filename, mime_type, file_length, local_path,
                 downloaded_at, media_unavailable_at, revoked, deleted_for_me,
                 edited, buttons
               ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
            (
                "111@s.whatsapp.net", "Ada", "vid1", "111@s.whatsapp.net", "Ada", 140, 0,
                "", "Sent video", "", "", 0, 0, "", "", "video", "", "clip.mp4", "video/mp4",
                mp4.stat().st_size, str(mp4), 1, 0, 0, 0, 0, "",
            ),
        )
        con.commit()
        con.close()
        messages = {m["id"]: m for m in self.mod.list_messages(self.store, "111@s.whatsapp.net")}
        self.assertEqual(messages["vid1"]["kind"], "video")
        self.assertTrue(messages["vid1"]["downloaded"])
        self.assertTrue(messages["vid1"]["thumbUrl"].startswith("file:"))
        thumb = mp4.with_suffix(".jpg")
        self.assertTrue(thumb.is_file())
        self.assertEqual(thumb.read_bytes()[:3], b"\xff\xd8\xff")

        blob = mp4.read_bytes()

        def fake_run(args, store=None, timeout=60, readonly=False):
            dest = Path(args[args.index("--output") + 1])
            dest.write_bytes(blob)
            return {}

        con = sqlite3.connect(self.db)
        con.execute(
            """INSERT INTO messages (
                 chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me,
                 text, display_text, quoted_msg_id, quoted_sender_jid, is_forwarded,
                 forwarding_score, reaction_to_id, reaction_emoji, media_type,
                 media_caption, filename, mime_type, file_length, local_path,
                 downloaded_at, media_unavailable_at, revoked, deleted_for_me,
                 edited, buttons
               ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
            (
                "111@s.whatsapp.net", "Ada", "vid2", "111@s.whatsapp.net", "Ada", 141, 0,
                "", "Sent video", "", "", 0, 0, "", "", "video", "", "clip.mp4", "video/mp4",
                len(blob), "", 0, 0, 0, 0, 0, "",
            ),
        )
        con.commit()
        con.close()
        self.mod.run_wacli = fake_run
        result = self.mod.download_media(self.store, "111@s.whatsapp.net", "vid2")
        self.assertTrue(result["thumbUrl"].startswith("file:"))
        self.assertTrue(Path(result["localPath"]).with_suffix(".jpg").is_file())

    def test_pick_name_and_placeholders(self):
        self.assertEqual(self.mod.pick_name("777@s.whatsapp.net", "Kevin Bernthaler", jid="777@s.whatsapp.net"), "Kevin Bernthaler")
        self.assertEqual(self.mod.pick_name("666@g.us", jid="666@g.us"), "Unknown group")
        self.assertEqual(self.mod.human_preview("Sent image"), "Photo")
        self.assertEqual(self.mod.placeholder_label("sent video"), "Video")


if __name__ == "__main__":
    unittest.main()
