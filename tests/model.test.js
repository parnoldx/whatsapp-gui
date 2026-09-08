const test = require("node:test")
const assert = require("node:assert/strict")
const Model = require("../qml/Model.js")

test("initials and avatar index stay stable", () => {
  assert.equal(Model.initials("Ada Lovelace"), "AL")
  assert.equal(Model.initials("Ada"), "AD")
  assert.equal(Model.initials(""), "?")
  const a = Model.avatarColorIndex("111@s.whatsapp.net", 8)
  assert.equal(a, Model.avatarColorIndex("111@s.whatsapp.net", 8))
  assert.ok(a >= 0 && a < 8)
})

test("linkify escapes html and wraps urls", () => {
  const linked = Model.linkify('see <script> https://example.com/a?x=1')
  assert.equal(linked.hasLinks, true)
  assert.match(linked.html, /<a href="https:\/\/example.com\/a\?x=1">/)
  assert.equal(linked.html.indexOf("<script>"), -1)
  assert.match(linked.html, /&lt;script&gt;/)
})

test("filterChats matches name or preview", () => {
  const chats = [
    { name: "Ada", preview: "hello" },
    { name: "Crew", preview: "photo from Sam" }
  ]
  assert.equal(Model.filterChats(chats, "crew").length, 1)
  assert.equal(Model.filterChats(chats, "hello")[0].name, "Ada")
  assert.equal(Model.filterChats(chats, "").length, 2)
})

test("filterChats accepts QVariantList-shaped objects", () => {
  const like = { 0: { name: "Ada", preview: "hi" }, 1: { name: "Crew", preview: "yo" }, length: 2 }
  assert.equal(Array.isArray(like), false)
  assert.equal(Model.filterChats(like, "").length, 2)
  assert.equal(Model.filterChats(like, "crew")[0].name, "Crew")
})

test("badgeCount ignores muted chats and acknowledged timestamps", () => {
  const chats = [
    { jid: "a", lastMessageTs: 10, unreadCount: 2, muted: false },
    { jid: "b", lastMessageTs: 10, unreadCount: 4, muted: true }
  ]
  assert.equal(Model.badgeCount(chats, {}), 1)
  assert.equal(Model.badgeCount(chats, { a: 10 }), 0)
})

test("visibleUnread hides the count for the open chat and after ack", () => {
  const chat = { jid: "a", lastMessageTs: 10, unreadCount: 31 }
  assert.equal(Model.visibleUnread(chat, {}, ""), 31)
  assert.equal(Model.visibleUnread(chat, {}, "a"), 0)
  assert.equal(Model.visibleUnread(chat, { a: 10 }, ""), 0)
})

test("adoptThread switches chats immediately and keeps the same chat during refresh", () => {
  const shown = [{ id: "a1", chatJid: "a" }, { id: "a2", chatJid: "a" }]
  const incoming = shown
  assert.equal(Model.adoptThread([], incoming, "b").length, 0)
  assert.equal(Model.adoptThread(shown, incoming, "b").length, 0)
  assert.equal(Model.adoptThread(shown, incoming, "a")[0].id, "a1")
  const next = [{ id: "b1", chatJid: "b" }]
  assert.equal(Model.adoptThread(shown, next, "b")[0].id, "b1")
  assert.equal(Model.messageIds(shown), "a1\na2")
  assert.equal(Model.messageIds(shown), Model.messageIds(shown))
})

test("threadStamp changes when a patched field flips on the same id list", () => {
  const before = [{ id: "a1" }, { id: "a2", downloaded: false }]
  const afterDownload = [{ id: "a1" }, { id: "a2", downloaded: true }]
  const afterReact = [{ id: "a1", reactions: [{ emoji: "👍", count: 1 }], myReaction: "👍" }, { id: "a2" }]
  assert.equal(Model.messageIds(before), Model.messageIds(afterDownload))  // id list unchanged
  assert.notEqual(Model.threadStamp(before), Model.threadStamp(afterDownload))
  assert.notEqual(Model.threadStamp(before), Model.threadStamp(afterReact))
  assert.equal(Model.threadStamp(before), Model.threadStamp(before))
})

test("applyMyReaction adds, replaces, and clears the local reaction", () => {
  const start = [{ id: "a1", reactions: [{ emoji: "😂", count: 1, mine: false, who: ["Sam"] }] }]
  const added = Model.applyMyReaction(start, "a1", "❤️")
  assert.equal(added[0].myReaction, "❤️")
  assert.equal(added[0].reactions.length, 2)
  assert.deepEqual(added[0].reactions.find(e => e.emoji === "❤️"), { emoji: "❤️", count: 1, mine: true, who: ["You"] })
  assert.equal(start[0].myReaction, undefined)  // original untouched

  const stacked = Model.applyMyReaction(start, "a1", "😂")
  assert.equal(stacked[0].myReaction, "😂")
  assert.equal(stacked[0].reactions.length, 1)
  assert.equal(stacked[0].reactions[0].count, 2)
  assert.equal(stacked[0].reactions[0].mine, true)
  assert.deepEqual(stacked[0].reactions[0].who, ["Sam", "You"])

  const replaced = Model.applyMyReaction(added, "a1", "👍")
  assert.equal(replaced[0].myReaction, "👍")
  assert.equal(replaced[0].reactions.find(e => e.emoji === "❤️"), undefined)

  const cleared = Model.applyMyReaction(stacked, "a1", "")
  assert.equal(cleared[0].myReaction, "")
  assert.equal(cleared[0].reactions[0].count, 1)
  assert.equal(cleared[0].reactions[0].mine, false)
  assert.deepEqual(cleared[0].reactions[0].who, ["Sam"])

  const same = Model.applyMyReaction(start, "nope", "❤️")
  assert.equal(same[0], start[0])
})

test("overlayMyReactions keeps a pending react across a stale reload", () => {
  const pending = { a1: "❤️" }
  const stale = [{ id: "a1", reactions: [], myReaction: "" }]
  const over = Model.overlayMyReactions(stale, pending)
  assert.equal(over.messages[0].myReaction, "❤️")
  assert.equal(over.pending.a1, "❤️")

  const caught = [{ id: "a1", reactions: [{ emoji: "❤️", count: 1, mine: true, who: ["You"] }], myReaction: "❤️" }]
  const done = Model.overlayMyReactions(caught, pending)
  assert.equal(done.messages[0].myReaction, "❤️")
  assert.equal(done.pending.a1, undefined)
})

test("parseLink recognizes Instagram reels", () => {
  const preview = Model.parseLink("https://www.instagram.com/reel/Dc66-wqjore/?stkn=abc")
  assert.equal(preview.site, "Instagram")
  assert.equal(preview.label, "Reel")
  assert.equal(preview.host, "instagram.com")
  assert.equal(preview.embedUrl, "https://www.instagram.com/reel/Dc66-wqjore/embed/")
  assert.equal(Model.textIsOnlyUrl("https://www.instagram.com/reel/Dc66-wqjore/?stkn=abc", preview.url), true)
  assert.equal(Model.textIsOnlyUrl("look https://example.com", "https://example.com"), false)
})

test("parseLink builds official embed URLs", () => {
  assert.equal(
    Model.parseLink("https://www.instagram.com/p/Dc9x-rfASm0/?img_index=1").embedUrl,
    "https://www.instagram.com/p/Dc9x-rfASm0/embed/"
  )
  assert.equal(
    Model.parseLink("https://www.tiktok.com/@_omarreacts/video/7662159610396052757?_r=1").embedUrl,
    "https://www.tiktok.com/embed/v2/7662159610396052757"
  )
  assert.equal(Model.parseLink("https://vm.tiktok.com/ZGdxcYD6r/").embedUrl, "")
  assert.equal(
    Model.parseLink("https://www.tiktok.com/@x/photo/7662159610396052757").embedUrl,
    "https://www.tiktok.com/embed/v2/7662159610396052757"
  )
  assert.equal(Model.needsEmbedResolve(Model.parseLink("https://vm.tiktok.com/ZGdxcYD6r/")), true)
  assert.equal(Model.needsEmbedResolve(Model.parseLink("https://www.tiktok.com/t/ZPabcdef/")), true)
  assert.equal(
    Model.needsEmbedResolve(Model.parseLink("https://www.tiktok.com/@x/video/7662159610396052757")),
    false
  )
  assert.equal(Model.parseLink("https://www.facebook.com/share/r/18KkbJYRmm/").embedUrl, "")
  assert.equal(
    Model.needsEmbedResolve(Model.parseLink("https://www.facebook.com/share/r/18KkbJYRmm/")),
    true
  )
  assert.equal(
    Model.parseLink("https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=4").embedUrl,
    "https://www.youtube.com/embed/dQw4w9WgXcQ"
  )
  assert.equal(
    Model.parseLink("https://youtu.be/dQw4w9WgXcQ").embedUrl,
    "https://www.youtube.com/embed/dQw4w9WgXcQ"
  )
  assert.equal(
    Model.parseLink("https://www.facebook.com/reel/2257374628373907/?fs=e").embedUrl,
    "https://www.facebook.com/plugins/video.php?href=https%3A%2F%2Fwww.facebook.com%2Freel%2F2257374628373907%2F&show_text=false"
  )
  assert.equal(
    Model.parseLink("https://x.com/BarackObama/status/266031293945503744?s=20").embedUrl,
    "https://platform.twitter.com/embed/Tweet.html?id=266031293945503744&dnt=true&theme=dark"
  )
  assert.equal(Model.parseLink("https://example.com/x").embedUrl, "")
  const cached = { url: "https://vm.tiktok.com/ZGdxcYD6r/", host: "vm.tiktok.com" }
  assert.equal(Model.linkEmbed(cached), "")
  cached.embedUrl = "https://www.tiktok.com/embed/v3/7662159610396052757"
  assert.equal(Model.linkEmbed(cached), "https://www.tiktok.com/embed/v2/7662159610396052757")
})

test("fileKind guesses from the path", () => {
  assert.equal(Model.fileKind("/tmp/a.JPG"), "image")
  assert.equal(Model.fileKind("clip.mp4"), "video")
  assert.equal(Model.fileKind("note.ogg"), "audio")
  assert.equal(Model.fileKind("x.pdf"), "document")
})

test("overlayPendingSends shows a bubble until the real row lands", () => {
  const pending = [Model.pendingMessage({
    id: "pending:1", chatJid: "a", ts: 100, kind: "text", text: "hi"
  })]
  const empty = Model.overlayPendingSends([], pending, "a")
  assert.equal(empty.messages.length, 1)
  assert.equal(empty.messages[0].id, "pending:1")
  assert.equal(empty.pending.length, 1)

  const stale = [{ id: "old", chatJid: "a", fromMe: true, kind: "text", text: "hi", ts: 10 }]
  const still = Model.overlayPendingSends(stale, pending, "a")
  assert.equal(still.messages.length, 2)
  assert.equal(still.pending[0].id, "pending:1")

  const caught = [{ id: "real", chatJid: "a", fromMe: true, kind: "text", text: "hi", ts: 101 }]
  const done = Model.overlayPendingSends(caught, pending, "a")
  assert.equal(done.messages.length, 1)
  assert.equal(done.messages[0].id, "real")
  assert.equal(done.pending.length, 0)

  const other = Model.overlayPendingSends([{ id: "b1", chatJid: "b" }], pending, "b")
  assert.equal(other.messages.length, 1)
  assert.equal(other.pending.length, 1)

  const already = Model.overlayPendingSends(pending, pending, "a")
  assert.equal(already.messages.length, 1)
})

test("mentionToken finds @query at the cursor", () => {
  assert.deepEqual(Model.mentionToken("hi @sa", 6), { start: 3, query: "sa" })
  assert.equal(Model.mentionToken("hi sa", 5), null)
  assert.equal(Model.mentionToken("mail@x", 6), null)
  const applied = Model.applyMention("hi @sa", 6, "Sam Stone")
  assert.equal(applied.text, "hi @Sam Stone ")
})
