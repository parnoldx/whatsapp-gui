function initials(name) {
  var text = String(name || "").replace(/[^a-zA-Z0-9\s._-]/g, " ").trim()
  if (!text) return "?"
  var words = text.split(/[\s._-]+/).filter(Boolean)
  if (words.length === 0) return "?"
  if (words.length === 1) {
    var single = words[0]
    return single.length >= 2 ? (single[0] + single[1]).toUpperCase() : single[0].toUpperCase()
  }
  return (words[0][0] + words[1][0]).toUpperCase()
}

function avatarColorIndex(identifier, paletteLength) {
  if (!paletteLength || paletteLength <= 0) return 0
  var str = String(identifier || "").toLowerCase()
  var hash = 0
  for (var i = 0; i < str.length; i++) {
    hash = ((hash << 5) - hash) + str.charCodeAt(i)
    hash |= 0
  }
  return Math.abs(hash) % paletteLength
}

function pad(n) {
  return n < 10 ? "0" + n : String(n)
}

function formatTime(ts) {
  if (!ts) return ""
  var date = new Date(Number(ts) * 1000)
  if (isNaN(date.getTime())) return ""
  return pad(date.getHours()) + ":" + pad(date.getMinutes())
}

function formatDay(ts, nowTs) {
  if (!ts) return ""
  var date = new Date(Number(ts) * 1000)
  var now = nowTs ? new Date(Number(nowTs) * 1000) : new Date()
  if (isNaN(date.getTime())) return ""
  var startToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
  var startThat = new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime()
  var dayMs = 86400000
  if (startThat === startToday) return formatTime(ts)
  if (startThat === startToday - dayMs) return "Yesterday"
  if (now.getTime() - startThat < 6 * dayMs) {
    return ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"][date.getDay()]
  }
  return date.getDate() + " " + ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"][date.getMonth()]
}

function escapeHtml(value) {
  return String(value || "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
}

function linkify(text) {
  var raw = String(text || "")
  var escaped = escapeHtml(raw)
  var html = escaped.replace(/(https?:\/\/[^\s&<]+)/gi, function (url) {
    return '<a href="' + url + '">' + url + "</a>"
  })
  return { html: html.replace(/\n/g, "<br/>"), hasLinks: html.indexOf("<a href=") !== -1, plain: raw }
}

function copyableText(m) {
  if (!m) return ""
  var t = String(m.text || m.caption || "")
  if (t) return t
  if (String(m.kind || "") === "location") {
    var name = String(m.locationName || "Location")
    var addr = String(m.locationAddress || "")
    return addr ? (name + "\n" + addr) : name
  }
  return ""
}

function asList(value) {
  if (!value) return []
  if (Array.isArray(value)) return value
  var out = []
  var n = Number(value.length)
  if (!isFinite(n) || n < 0) return out
  for (var i = 0; i < n; i++) out.push(value[i])
  return out
}

function filterChats(chats, query) {
  var list = asList(chats)
  var needle = String(query || "").trim().toLowerCase()
  if (!needle) return list
  var out = []
  for (var i = 0; i < list.length; i++) {
    var chat = list[i]
    var hay = String((chat && chat.name) || "").toLowerCase() + " " + String((chat && chat.preview) || "").toLowerCase()
    if (hay.indexOf(needle) !== -1) out.push(chat)
  }
  return out
}

// The open chat is not exempt: messages that arrive while the window is
// inactive stay unread until the user clicks into the chat again.
function visibleUnread(chat, acks, selectedJid) {
  if (!chat) return 0
  var ack = Number((acks && acks[chat.jid]) || 0)
  if (Number(chat.lastMessageTs || 0) <= ack) return 0
  return Number(chat.unreadCount || 0)
}

function badgeCount(chats, acks) {
  var list = asList(chats)
  var map = acks || {}
  var total = 0
  for (var i = 0; i < list.length; i++) {
    var chat = list[i]
    if (!chat || chat.muted || chat.archived) continue
    if (visibleUnread(chat, map, "") > 0) total += 1
  }
  return total
}

function adoptThread(shown, incoming, selectedJid) {
  if (!selectedJid) return []
  var list = asList(incoming)
  if (list.length && list[0] && list[0].chatJid === selectedJid) return list
  var current = asList(shown)
  if (current.length && current[0] && current[0].chatJid === selectedJid) return current
  return []
}

function messageIds(messages) {
  var list = asList(messages)
  var out = []
  for (var i = 0; i < list.length; i++) out.push(String((list[i] && list[i].id) || ""))
  return out.join("\n")
}

function messageHasId(m, id) {
  var want = String(id || "")
  if (!m || !want) return false
  if (String(m.id || "") === want) return true
  if (String(m.albumId || "") === want) return true
  var album = asList(m.album)
  for (var i = 0; i < album.length; i++) {
    if (album[i] && String(album[i].id || "") === want) return true
  }
  return false
}

function chatByJid(chats, jid) {
  var list = asList(chats)
  for (var i = 0; i < list.length; i++) {
    if (list[i] && String(list[i].jid || "") === String(jid || "")) return list[i]
  }
  return null
}

function indexOfId(messages, id) {
  var list = asList(messages)
  for (var i = 0; i < list.length; i++) {
    if (messageHasId(list[i], id)) return i
  }
  return -1
}

// Where reading should resume: the first message newer than the last local ack.
// Without an ack (chat never opened here) fall back to WhatsApp's unread count.
function firstUnreadIndex(messages, unread, ack) {
  var list = asList(messages)
  if (!list.length) return -1
  var a = Number(ack || 0)
  if (a > 0) {
    for (var i = 0; i < list.length; i++) {
      if (Number((list[i] && list[i].ts) || 0) > a) return i
    }
    return -1
  }
  var n = Number(unread || 0)
  return (n > 0 && n < list.length) ? list.length - n : -1
}

// Like messageIds, but also folds in the fields that get patched in place on an
// otherwise-unchanged thread (download state, link-preview fetch, reactions), so
// the view re-adopts when one of those flips instead of showing stale data.
function threadStamp(messages) {
  var list = asList(messages)
  var out = []
  for (var i = 0; i < list.length; i++) {
    var m = list[i] || {}
    var rx = m.reactions && m.reactions.length ? m.reactions.length + ":" + String(m.myReaction || "") : ""
    var lp = m.linkPreview && (m.linkPreview.fetched || m.linkPreview.imageUrl) ? "p" : ""
    var album = asList(m.album)
    var al = ""
    for (var a = 0; a < album.length; a++)
      al += (album[a] && album[a].downloaded) ? "d" : "-"
    out.push(String(m.id || "") + "|" + (m.downloaded ? "d" : "") + "|" + rx + "|" + lp + "|" + al + "|" + String(m.quotedId || ""))
  }
  return out.join("\n")
}

function copyReaction(entry) {
  return {
    emoji: String((entry && entry.emoji) || ""),
    count: Number((entry && entry.count) || 0),
    mine: !!(entry && entry.mine),
    who: asList(entry && entry.who).slice()
  }
}

// Optimistic own-reaction: one emoji per person, empty string clears.
function applyMyReaction(messages, id, emoji) {
  var list = asList(messages)
  var want = String(emoji || "")
  var target = String(id || "")
  if (!target) return list
  var out = []
  for (var i = 0; i < list.length; i++) {
    var m = list[i]
    if (!m || String(m.id || "") !== target) {
      out.push(m)
      continue
    }
    var buckets = []
    var old = asList(m.reactions)
    for (var j = 0; j < old.length; j++) {
      var e = copyReaction(old[j])
      if (e.mine) {
        var idx = e.who.indexOf("You")
        if (idx >= 0) e.who.splice(idx, 1)
        e.count = Math.max(0, e.count - 1)
        e.mine = false
        if (e.count <= 0) continue
      }
      buckets.push(e)
    }
    if (want) {
      var found = false
      for (var k = 0; k < buckets.length; k++) {
        if (buckets[k].emoji === want) {
          buckets[k].count += 1
          buckets[k].mine = true
          if (buckets[k].who.indexOf("You") < 0) buckets[k].who.push("You")
          found = true
          break
        }
      }
      if (!found) buckets.push({ emoji: want, count: 1, mine: true, who: ["You"] })
    }
    var copy = {}
    for (var key in m) copy[key] = m[key]
    copy.reactions = buckets
    copy.myReaction = want
    out.push(copy)
  }
  return out
}

// Re-apply pending own-reactions after a stale reload; drop ids the DB caught up on.
function overlayMyReactions(messages, pending) {
  var list = asList(messages)
  var keep = {}
  if (!pending) return { messages: list, pending: keep }
  for (var id in pending) {
    if (!Object.prototype.hasOwnProperty.call(pending, id)) continue
    var want = String(pending[id] || "")
    var found = null
    for (var i = 0; i < list.length; i++) {
      if (list[i] && String(list[i].id || "") === String(id)) {
        found = list[i]
        break
      }
    }
    if (found && String(found.myReaction || "") === want) continue
    if (found) list = applyMyReaction(list, id, want)
    keep[id] = pending[id]
  }
  return { messages: list, pending: keep }
}

function canonicalHost(host) {
  var h = String(host || "").toLowerCase()
  if (h.indexOf("www.") === 0) h = h.slice(4)
  if (h === "m.instagram.com") return "instagram.com"
  if (h === "m.facebook.com" || h === "web.facebook.com") return "facebook.com"
  if (h === "m.tiktok.com") return "tiktok.com"
  if (h === "vt.tiktok.com") return "vm.tiktok.com"
  if (h === "mobile.twitter.com") return "twitter.com"
  return h
}

function embedUrlFor(host, url) {
  var raw = String(url || "")
  var h = canonicalHost(host)
  if (!h && raw) {
    h = canonicalHost(raw.replace(/^https?:\/\//i, "").split("/")[0])
  }
  var path = raw.replace(/^https?:\/\/[^/?#]+/i, "")
  var m
  if (h === "instagram.com") {
    m = path.match(/\/(reel|p|tv)\/([^/?#]+)/)
    return m ? ("https://www.instagram.com/" + m[1] + "/" + m[2] + "/embed/") : ""
  }
  if (h === "tiktok.com") {
    m = path.match(/\/(?:video|photo|v)\/(\d+)/) || path.match(/\/embed\/(?:v2|v3)\/(\d+)/)
    return m ? ("https://www.tiktok.com/embed/v2/" + m[1]) : ""
  }
  if (h === "youtube.com" || h === "youtu.be" || h === "m.youtube.com") {
    var id = ""
    if (h === "youtu.be") id = path.replace(/^\//, "").split(/[/?#]/)[0]
    if (!id) {
      m = path.match(/\/(?:embed|shorts|live)\/([^/?#]+)/)
      if (m) id = m[1]
    }
    if (!id) {
      m = raw.match(/[?&]v=([^&?#]+)/)
      if (m) id = m[1]
    }
    return id ? ("https://www.youtube.com/embed/" + id) : ""
  }
  if (h === "x.com" || h === "twitter.com") {
    m = path.match(/\/(?:i\/web\/)?status\/(\d+)/)
    if (!m) return ""
    if (path.indexOf("/video/") !== -1)
      return "https://twitter.com/i/videos/tweet/" + m[1]
    return "https://platform.twitter.com/embed/Tweet.html?id=" + m[1] + "&dnt=true&theme=dark"
  }
  if (h === "facebook.com" || h === "fb.watch") {
    if (h === "facebook.com" && /\/share\/[rv]\//.test(path))
      return ""
    var canonical = raw.replace(/[?#].*$/, "")
    if (h === "facebook.com") {
      var fbPath = canonical.replace(/^https?:\/\/[^/]+/i, "")
      if (fbPath.indexOf("/") !== 0) fbPath = "/" + fbPath
      canonical = "https://www.facebook.com" + fbPath
    }
    return canonical ? ("https://www.facebook.com/plugins/video.php?href=" + encodeURIComponent(canonical) + "&show_text=false") : ""
  }
  return ""
}

function linkEmbed(preview) {
  if (!preview) return ""
  var computed = embedUrlFor(preview.host || "", preview.url || "")
  var u = computed || (preview.embedUrl ? String(preview.embedUrl) : "")
  if (u.indexOf("plugins/video.php") !== -1 && u.indexOf("%2Fshare%2F") !== -1)
    return ""
  return u.replace("/embed/v3/", "/embed/v2/")
}

function needsEmbedResolve(preview) {
  if (!preview || !preview.url) return false
  if (linkEmbed(preview)) return false
  var h = canonicalHost(preview.host || "")
  if (!h) h = canonicalHost(String(preview.url).replace(/^https?:\/\//i, "").split("/")[0])
  if (h === "vm.tiktok.com" || h === "t.co") return true
  if (h === "tiktok.com") {
    var path = String(preview.url)
    return path.indexOf("/video/") === -1 && path.indexOf("/photo/") === -1 && path.indexOf("/embed/") === -1
  }
  if (h === "facebook.com")
    return /\/share\/[rv]\//.test(String(preview.url))
  return false
}

function parseLink(text) {
  var raw = String(text || "")
  var match = raw.match(/https?:\/\/[^\s<>"']+/i)
  if (!match) return null
  var url = match[0].replace(/[).,;:!?]+$/, "")
  var host = canonicalHost(url.replace(/^https?:\/\//i, "").split("/")[0])
  var path = url.replace(/^https?:\/\/[^/]+/i, "")
  var site = host
  var label = host
  if (host === "instagram.com") {
    site = "Instagram"
    label = path.indexOf("/reel/") !== -1 ? "Reel" : (path.indexOf("/p/") !== -1 ? "Post" : "Instagram")
  } else if (host === "tiktok.com" || host === "vm.tiktok.com") {
    site = "TikTok"
    label = "TikTok"
  } else if (host === "youtube.com" || host === "youtu.be" || host === "m.youtube.com") {
    site = "YouTube"
    label = "YouTube"
  } else if (host === "x.com" || host === "twitter.com" || host === "t.co") {
    site = "X"
    label = "Post"
  } else if (host === "open.spotify.com") {
    site = "Spotify"
    label = "Spotify"
  } else if (host === "facebook.com" || host === "fb.watch") {
    site = "Facebook"
    label = "Facebook"
  }
  return {
    url: url,
    host: host,
    site: site,
    label: label,
    title: label,
    description: "",
    imageUrl: "",
    embedUrl: embedUrlFor(host, url)
  }
}

function textIsOnlyUrl(text, url) {
  return String(text || "").trim() === String(url || "").trim()
}

function mentionToken(text, cursor) {
  var raw = String(text || "")
  var pos = Math.max(0, Math.min(Number(cursor || raw.length), raw.length))
  var i = pos - 1
  while (i >= 0) {
    var ch = raw.charAt(i)
    if (ch === "@") {
      if (i === 0 || /\s/.test(raw.charAt(i - 1))) {
        return { start: i, query: raw.substring(i + 1, pos) }
      }
      return null
    }
    if (/\s/.test(ch)) return null
    i--
  }
  return null
}

function applyMention(text, cursor, name) {
  var token = mentionToken(text, cursor)
  var raw = String(text || "")
  var insert = "@" + String(name || "").trim() + " "
  if (!token) {
    return { text: raw + insert, cursor: (raw + insert).length }
  }
  var next = raw.substring(0, token.start) + insert + raw.substring(cursor)
  return { text: next, cursor: token.start + insert.length }
}

function filterMembers(members, query) {
  var list = asList(members)
  var needle = String(query || "").trim().toLowerCase()
  if (!needle) return list.slice(0, 8)
  var out = []
  for (var i = 0; i < list.length; i++) {
    var member = list[i]
    if (String((member && member.name) || "").toLowerCase().indexOf(needle) !== -1) out.push(member)
    if (out.length >= 8) break
  }
  return out
}

function kindLabel(kind) {
  var map = {
    image: "Photo",
    video: "Video",
    gif: "GIF",
    sticker: "Sticker",
    voice: "Voice note",
    audio: "Audio",
    document: "Document",
    location: "Location",
    revoked: "Deleted"
  }
  return map[kind] || ""
}

function fileKind(path) {
  var name = String(path || "").split("/").pop().toLowerCase()
  var dot = name.lastIndexOf(".")
  var ext = dot >= 0 ? name.slice(dot + 1) : ""
  if (ext === "jpg" || ext === "jpeg" || ext === "png" || ext === "webp" || ext === "heic" || ext === "bmp") return "image"
  if (ext === "gif") return "gif"
  if (ext === "mp4" || ext === "mov" || ext === "webm" || ext === "mkv") return "video"
  if (ext === "ogg" || ext === "opus" || ext === "m4a" || ext === "mp3" || ext === "wav") return "audio"
  return "document"
}

// WhatsApp "GIFs" are MP4 with gifPlayback. AnimatedImage cannot decode those.
function playsAsVideo(m) {
  var kind = String((m && m.kind) || "")
  if (kind === "video") return true
  if (kind !== "gif") return false
  var mime = String((m && m.mimeType) || "").toLowerCase()
  if (mime.indexOf("image/") === 0) return false
  var name = String((m && (m.localPath || m.filename || m.fileUrl)) || "").split("?")[0].toLowerCase()
  if (/\.(gif|webp|png)$/.test(name)) return false
  return true
}

// WhatsApp photos/videos usually land as a media hash or IMG-…-WA0001.
function generatedFilename(name) {
  var base = String(name || "").split(/[\\/]/).pop().trim()
  if (!base) return true
  var stem = base.replace(/\.[^.]+$/, "")
  if (!stem) return true
  if (/^[0-9a-f]{32,}$/i.test(stem)) return true
  if (/^(IMG|VID|AUD|PTT|STK|DOC)-\d{8}-WA\d+/i.test(stem)) return true
  if (/^message-.+/i.test(stem)) return true
  if (/^(image|photo|picture|video|sticker|audio|voice|file|document)$/i.test(stem)) return true
  return false
}

function viewerTitle(viewer) {
  if (!viewer) return ""
  var kind = String(viewer.kind || "")
  var name = String(viewer.filename || "").trim()
  if (kind === "embed") return name || "Link"
  var real = name && !generatedFilename(name) ? name : ""
  if (kind === "image" || kind === "gif" || kind === "sticker" || kind === "video")
    return real
  return real || kindLabel(kind) || name
}

var pendingSeq = 0

function sendBody(m) {
  return String((m && (m.text || m.caption)) || "")
    .replace(/\r\n/g, "\n")
    .replace(/\r/g, "\n")
    .replace(/^\s+|\s+$/g, "")
}

function pendingMessage(fields) {
  var f = fields || {}
  pendingSeq += 1
  return {
    id: String(f.id || ("pending:" + pendingSeq)),
    chatJid: String(f.chatJid || ""),
    ts: Number(f.ts) || Math.floor(Date.now() / 1000),
    fromMe: true,
    senderJid: String(f.senderJid || ""),
    senderName: String(f.senderName || ""),
    text: sendBody({ text: f.text }),
    caption: sendBody({ text: f.caption }),
    kind: String(f.kind || "text"),
    quotedId: String(f.quotedId || ""),
    quotedSender: String(f.quotedSender || ""),
    quotedText: String(f.quotedText || ""),
    forwarded: false,
    edited: false,
    filename: String(f.filename || ""),
    mimeType: String(f.mimeType || ""),
    fileLength: Number(f.fileLength || 0),
    localPath: String(f.localPath || ""),
    fileUrl: String(f.fileUrl || ""),
    thumbUrl: String(f.thumbUrl || ""),
    downloaded: !!f.downloaded,
    unavailable: false,
    reactions: [],
    myReaction: "",
    pending: true
  }
}

function sameSend(real, pending) {
  if (!real || !pending || !real.fromMe || real.pending) return false
  if (String(real.chatJid || "") !== String(pending.chatJid || "")) return false
  if (String(real.kind || "text") !== String(pending.kind || "text")) return false
  if (Number(real.ts || 0) + 5 < Number(pending.ts || 0)) return false
  var realQuote = String(real.quotedId || "")
  var pendingQuote = String(pending.quotedId || "")
  // WhatsApp often stores a different quoted_msg_id than the UI row we replied
  // to. Only treat that as a different send when both quote previews exist and
  // disagree. First post-send reload often has no quoted_msg_id yet.
  if (realQuote && pendingQuote && realQuote !== pendingQuote) {
    var realQuotedText = String(real.quotedText || "").replace(/^\s+|\s+$/g, "")
    var pendingQuotedText = String(pending.quotedText || "").replace(/^\s+|\s+$/g, "")
    if (realQuotedText && pendingQuotedText && realQuotedText !== pendingQuotedText)
      return false
  }
  if (String(pending.kind || "text") === "text")
    return sendBody(real) === sendBody(pending)
  if (sendBody({ text: real.caption }) !== sendBody({ text: pending.caption })) return false
  if (pending.filename)
    return String(real.filename || "") === String(pending.filename)
  return true
}

function copyMessage(m) {
  var copy = {}
  if (!m) return copy
  for (var key in m) copy[key] = m[key]
  return copy
}

function adoptQuote(real, pending) {
  if (!real || !pending) return real
  if (String(real.quotedText || "")) return real
  if (!pending.quotedId && !pending.quotedText) return real
  var copy = copyMessage(real)
  if (!copy.quotedId) copy.quotedId = String(pending.quotedId || "")
  if (!copy.quotedSender) copy.quotedSender = String(pending.quotedSender || "")
  if (!copy.quotedText) copy.quotedText = String(pending.quotedText || "")
  return copy
}

// Keep unacked outgoing bubbles across the post-send DB reload; drop each
// when a matching from-me row shows up. Carry the pending quote onto that
// row when wacli stored the send without quoted_msg_id.
function overlayPendingSends(messages, pending, selectedJid) {
  var list = asList(messages).slice()
  var plist = asList(pending)
  if (!plist.length) return { messages: list, pending: [] }
  var have = {}
  for (var i = 0; i < list.length; i++) {
    if (list[i] && list[i].id) have[String(list[i].id)] = true
  }
  var keep = []
  var extra = []
  var used = {}
  for (var p = 0; p < plist.length; p++) {
    var item = plist[p]
    if (!item) continue
    var matched = false
    for (var j = 0; j < list.length; j++) {
      if (used[j]) continue
      if (sameSend(list[j], item)) {
        used[j] = true
        matched = true
        var hadQuote = !!String((list[j] && list[j].quotedText) || "")
        list[j] = adoptQuote(list[j], item)
        if (!hadQuote && (item.quotedId || item.quotedText))
          keep.push(item)
        break
      }
    }
    if (matched) continue
    keep.push(item)
    if (selectedJid && String(item.chatJid || "") === String(selectedJid) && !have[String(item.id)])
      extra.push(item)
  }
  if (!extra.length) return { messages: list, pending: keep }
  return { messages: list.concat(extra), pending: keep }
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = {
    asList: asList,
    initials: initials,
    avatarColorIndex: avatarColorIndex,
    formatTime: formatTime,
    formatDay: formatDay,
    escapeHtml: escapeHtml,
    linkify: linkify,
    copyableText: copyableText,
    filterChats: filterChats,
    visibleUnread: visibleUnread,
    badgeCount: badgeCount,
    adoptThread: adoptThread,
    messageIds: messageIds,
    messageHasId: messageHasId,
    chatByJid: chatByJid,
    indexOfId: indexOfId,
    firstUnreadIndex: firstUnreadIndex,
    threadStamp: threadStamp,
    applyMyReaction: applyMyReaction,
    overlayMyReactions: overlayMyReactions,
    parseLink: parseLink,
    canonicalHost: canonicalHost,
    embedUrlFor: embedUrlFor,
    linkEmbed: linkEmbed,
    needsEmbedResolve: needsEmbedResolve,
    textIsOnlyUrl: textIsOnlyUrl,
    mentionToken: mentionToken,
    applyMention: applyMention,
    filterMembers: filterMembers,
    kindLabel: kindLabel,
    fileKind: fileKind,
    playsAsVideo: playsAsVideo,
    generatedFilename: generatedFilename,
    viewerTitle: viewerTitle,
    pendingMessage: pendingMessage,
    sameSend: sameSend,
    overlayPendingSends: overlayPendingSends
  }
}
