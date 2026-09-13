import QtQuick
import QtQuick.Controls.Basic
import QtQuick.Layouts
import QtMultimedia
import QtQuick.Window
import QtWebEngine
import "Model.js" as Model

ApplicationWindow {
    id: win
    width: 560
    height: 900
    minimumWidth: 360
    minimumHeight: 400
    visible: true
    title: "WhatsApp"
    color: Theme.windowBg

    Behavior on color { ColorAnimation { duration: Theme.anim } }

    readonly property bool compact: width < 860
    property string query: ""
    property var viewer: null
    property var embedLogins: ({ })
    property bool stickToEnd: true
    property bool pinning: false
    property real anchorY: 0
    property var shownMessages: []
    property string shownJid: ""
    property string shownIds: ""
    property var pendingReactions: ({})
    property var pendingSends: []
    property var pendingReactMsg: null
    property string highlightId: ""
    property bool unreadPending: false
    property real unreadAck: 0
    property int unreadCount: 0
    property string unreadId: ""
    property string unreadMarkId: ""
    readonly property var chats: Model.filterChats(WhatsApp.chats, query)
    readonly property var chat: WhatsApp.selectedChat
    readonly property var threadMessages: shownMessages

    function adoptMessages() {
        var next = Model.adoptThread(shownMessages, WhatsApp.messages, WhatsApp.selectedJid)
        var over = Model.overlayMyReactions(next, win.pendingReactions)
        next = over.messages
        win.pendingReactions = over.pending
        var sends = Model.overlayPendingSends(next, win.pendingSends, WhatsApp.selectedJid)
        next = sends.messages
        win.pendingSends = sends.pending
        var ids = Model.threadStamp(next)
        var jid = ""
        if (next.length && next[0] && next[0].chatJid)
            jid = next[0].chatJid
        if (jid === shownJid && ids === shownIds)
            return false
        shownJid = jid
        win.replaceThread(next)
        return true
    }

    function replaceThread(next) {
        win.captureAnchor()
        win.pinning = true
        win.shownMessages = next
        win.shownIds = Model.threadStamp(next)
        Qt.callLater(win.restoreAnchor)
    }

    function goToNewest() {
        win.stickToEnd = true
        if (!thread.count) {
            win.pinning = false
            return
        }
        win.pinning = true
        thread.cancelFlick()
        thread.positionViewAtIndex(thread.count - 1, ListView.End)
        Qt.callLater(function() {
            // reposition once layout (async image heights) has settled
            if (win.stickToEnd && thread.count)
                thread.positionViewAtIndex(thread.count - 1, ListView.End)
            win.pinning = false
        })
    }

    function queueSend(msg) {
        if (!msg || !msg.chatJid)
            return
        win.dropUnreadAnchor()
        win.stickToEnd = true
        var next = (win.pendingSends || []).slice()
        next.push(msg)
        win.pendingSends = next
        if (win.shownJid && win.shownJid !== msg.chatJid)
            return
        var list = []
        var cur = win.shownMessages
        for (var i = 0; i < cur.length; i++) list.push(cur[i])
        list.push(msg)
        if (!win.shownJid)
            win.shownJid = msg.chatJid
        win.replaceThread(list)
    }

    function reactNow(msg, emoji) {
        if (!msg || !msg.id || msg.pending)
            return
        var pending = {}
        var old = win.pendingReactions
        for (var key in old) pending[key] = old[key]
        pending[msg.id] = emoji
        win.pendingReactions = pending
        win.replaceThread(Model.applyMyReaction(win.shownMessages, msg.id, emoji))
        WhatsApp.react(msg, emoji)
    }

    function finishPick(emoji) {
        var target = win.pendingReactMsg
        win.pendingReactMsg = null
        emojiSink.text = ""
        thread.forceActiveFocus()
        if (!emoji || !target)
            return
        win.reactNow(target, emoji)
    }

    function pickReact(msg) {
        win.pendingReactMsg = msg
        emojiSink.text = ""
        emojiSink.forceActiveFocus()
        // Overlay IPC is instant; the helper only watches the clipboard after.
        WhatsApp.openEmojiPicker()
        WhatsApp.pickEmoji(function(err, data) {
            if (err)
                return
            var emoji = data && data.emoji ? String(data.emoji) : ""
            win.finishPick(emoji)
        })
    }

    function avatarSource(jid, fallback) {
        if (!jid)
            return fallback || ""
        return (WhatsApp.avatars && WhatsApp.avatars[jid]) || fallback || ""
    }

    function captureAnchor() {
        if (pinning || stickToEnd || !thread.count)
            return
        anchorY = thread.contentY
    }

    function dropUnreadAnchor() {
        win.unreadPending = false
        win.unreadId = ""
    }

    // Entering a chat with unread messages starts at the first one instead of
    // the very bottom, and keeps it pinned while the thread reloads.
    function anchorUnread() {
        if (!thread.count || (!win.unreadPending && !win.unreadId))
            return false
        var idx = win.unreadId ? Model.indexOfId(win.shownMessages, win.unreadId) : -1
        if (idx < 0 && win.unreadPending) {
            idx = Model.firstUnreadIndex(win.shownMessages, win.unreadCount, win.unreadAck)
            if (idx >= 0) {
                win.unreadPending = false
                win.unreadId = String((win.shownMessages[idx] && win.shownMessages[idx].id) || "")
                win.unreadMarkId = win.unreadId
            }
        }
        if (idx < 0)
            return false
        thread.cancelFlick()
        win.stickToEnd = false
        win.pinning = true
        thread.positionViewAtIndex(idx, ListView.Beginning)
        win.anchorY = thread.contentY
        restoreTimer.tries = 0
        restoreTimer.restart()
        return true
    }

    function restoreAnchor() {
        if (shownJid !== WhatsApp.selectedJid) {
            win.pinning = false
            return
        }
        if (win.anchorUnread())
            return
        if (stickToEnd) {
            win.goToNewest()
            return
        }
        win.pinning = true
        thread.contentY = anchorY
        // ponytail: re-assert for 500ms since async image heights settle after the model swap;
        // replace with a proper item anchor if content keeps shifting
        restoreTimer.tries = 0
        restoreTimer.restart()
    }

    Timer {
        id: restoreTimer
        interval: 33
        repeat: true
        property int tries: 0
        onTriggered: {
            tries += 1
            if (thread.moving) { // the user took over: stop fighting them
                stop()
                win.pinning = false
                win.dropUnreadAnchor()
                return
            }
            // async image heights settle slowly in media-heavy threads, so hold
            // the unread anchor longer than a plain scroll-position restore
            if (tries > (win.unreadId ? 45 : 15)) {
                stop()
                win.pinning = false
                return
            }
            var idx = win.unreadId ? Model.indexOfId(win.shownMessages, win.unreadId) : -1
            if (idx >= 0)
                thread.positionViewAtIndex(idx, ListView.Beginning)
            else
                thread.contentY = win.anchorY
        }
    }

    function closeViewer() {
        player.stop()
        win.viewer = null
        // A login inside the embed viewer commits to the cookie DB lazily.
        WhatsApp.refreshEmbedLogins()
        embedLoginRecheck.restart()
    }

    Timer {
        id: embedLoginRecheck
        interval: 20000
        onTriggered: WhatsApp.refreshEmbedLogins()
    }

    WebEngineProfile {
        id: embedProfile
        storageName: "whatsapp-link-embed"
        offTheRecord: false
        persistentCookiesPolicy: WebEngineProfile.ForcePersistentCookies
        Component.onCompleted: {
            WhatsApp.refreshEmbedLogins()
            var prep = WebEngine.script()
            prep.name = "tiktok-embed-prep"
            prep.injectionPoint = WebEngineScript.DocumentCreation
            prep.worldId = WebEngineScript.MainWorld
            prep.runsOnSubFrames = true
            prep.sourceCode = win.tiktokPrepScript()
            userScripts.insert(prep)
            var ready = WebEngine.script()
            ready.name = "tiktok-embed-ready"
            ready.injectionPoint = WebEngineScript.DocumentReady
            ready.worldId = WebEngineScript.MainWorld
            ready.runsOnSubFrames = true
            ready.sourceCode = win.tiktokReadyScript()
            userScripts.insert(ready)
        }
    }

    function embedWatchUrl(url) {
        var u = String(url || "").replace("/embed/v3/", "/embed/v2/")
        if (!u)
            return u
        if (u.indexOf("youtube.com/embed/") !== -1 && u.indexOf("autoplay=") === -1)
            return u + (u.indexOf("?") >= 0 ? "&" : "?") + "autoplay=1"
        if (u.indexOf("tiktok.com/embed/") !== -1 && u.indexOf("autoplay=") === -1)
            return u + (u.indexOf("?") >= 0 ? "&" : "?") + "autoplay=1"
        if (u.indexOf("/i/videos/") !== -1 && u.indexOf("autoplay=") === -1)
            return u + (u.indexOf("?") >= 0 ? "&" : "?") + "autoplay=1"
        if (u.indexOf("facebook.com/plugins/video.php") !== -1) {
            if (u.indexOf("autoplay=") === -1)
                u += "&autoplay=true"
            if (u.indexOf("mute=") === -1)
                u += "&mute=0"
            if (u.indexOf("width=") === -1)
                u += "&width=500"
            return u
        }
        return u
    }

    function isEmbedPlayer(url) {
        var u = String(url || "")
        return u.indexOf("/embed") !== -1
            || u.indexOf("plugins/video.php") !== -1
            || u.indexOf("Tweet.html") !== -1
            || u.indexOf("/i/videos/") !== -1
            || u.indexOf("youtube.com/embed/") !== -1
    }

    function embedFrameOrigin(url) {
        var u = String(url || "")
        if (u.indexOf("instagram.com") !== -1)
            return "https://www.instagram.com/"
        if (u.indexOf("Tweet.html") !== -1 || u.indexOf("platform.twitter.com") !== -1)
            return "https://platform.twitter.com/"
        return ""
    }

    function embedFrameHtml(url) {
        var src = String(url || "").replace(/&/g, "&amp;").replace(/"/g, "&quot;")
        var fill = url.indexOf("instagram.com") !== -1
        var style = fill
            ? "html,body,iframe{margin:0;padding:0;width:100%;height:100%;border:0;background:#000;overflow:hidden}"
            : "html,body{margin:0;width:100%;height:100%;background:#000;display:flex;align-items:center;justify-content:center;overflow:auto}"
              + "iframe{width:min(550px,100%);height:100%;border:0;background:#000}"
        return "<!DOCTYPE html><html><head><meta charset='utf-8'><style>" + style
            + "</style></head><body><iframe src='" + src
            + "' allow='autoplay; fullscreen; encrypted-media; picture-in-picture' allowfullscreen></iframe></body></html>"
    }

    function tiktokPrepScript() {
        return "(function(){if(String(location.hostname).indexOf('tiktok')<0)return;"
            + "try{document.cookie='cookie-consent=essential;domain=.tiktok.com;path=/;max-age=31536000;SameSite=Lax';"
            + "document.cookie='tt_cookie_policy=essential;domain=.tiktok.com;path=/;max-age=31536000;SameSite=Lax'}catch(e){}"
            + "var s=document.createElement('style');s.id='wa-tt-prep';"
            + "s.textContent='html,body,#root{margin:0!important;width:100%!important;height:100%!important;overflow:hidden!important;background:#000!important}"
            + "tiktok-cookie-banner,[class*=\"CookieBanner\"],[id*=\"cookie-banner\"],[class*=\"cookie-banner\"]{display:none!important;height:0!important;visibility:hidden!important}"
            + "[aria-label=\"Play\"],[aria-label=\"Play video\"],[data-e2e*=\"play-icon\"],[class*=\"PlayIcon\"],[class*=\"BigPlay\"]{display:none!important}"
            + "[class*=\"PlayIconContainer\"],[class*=\"DivPlayIcon\"],[class*=\"VideoOverlay\"],[class*=\"play-overlay\"]{background:transparent!important;background-color:transparent!important}"
            + "[class*=\"PlayIconContainer\"]::before,[class*=\"PlayIconContainer\"]::after,[class*=\"DivPlayIcon\"]::before,[class*=\"DivPlayIcon\"]::after{display:none!important;background:none!important}';"
            + "(document.documentElement||document.head).appendChild(s)})()"
    }

    function tiktokReadyScript() {
        return "(function(){if(String(location.hostname).indexOf('tiktok')<0)return;"
            + "function fill(){var v=document.querySelector('video');if(!v)return;"
            + "var el=v;while(el&&el!==document.documentElement){el.style.setProperty('width','100%','important');"
            + "el.style.setProperty('height','100%','important');el.style.setProperty('max-width','none','important');"
            + "el.style.setProperty('max-height','none','important');el=el.parentElement}}"
            + "function hidePlay(){var v=document.querySelector('video');if(!v||v.paused)return;"
            + "var vr=v.getBoundingClientRect();var cx=vr.left+vr.width/2,cy=vr.top+vr.height/2;"
            + "var nodes=document.querySelectorAll('div,span,button,section,a,svg');"
            + "for(var i=0;i<nodes.length;i++){var el=nodes[i];if(el===v||(el.querySelector&&el.querySelector('video')))continue;"
            + "var a=((el.getAttribute&&el.getAttribute('aria-label'))||'').toLowerCase();"
            + "if(a==='play'||a==='play video'){el.style.setProperty('display','none','important');continue}"
            + "var r=el.getBoundingClientRect();if(r.width<8||r.height<8)continue;"
            + "if(r.width>=48&&r.width<=140&&r.height>=48&&r.height<=140&&Math.abs(r.left+r.width/2-cx)<50&&Math.abs(r.top+r.height/2-cy)<50)"
            + "{el.style.setProperty('display','none','important');continue}"
            + "if(r.width<vr.width*0.8||r.height<vr.height*0.8)continue;"
            + "if(Math.abs(r.left+r.width/2-cx)>vr.width*0.15||Math.abs(r.top+r.height/2-cy)>vr.height*0.15)continue;"
            + "if((el.innerText||'').trim().length>24)continue;"
            + "var st=getComputedStyle(el);var bg=st.backgroundColor||'';"
            + "var m=bg.match(/rgba?\\(\\s*(\\d+)\\s*,\\s*(\\d+)\\s*,\\s*(\\d+)(?:\\s*,\\s*([0-9.]+))?\\s*\\)/);"
            + "var alpha=m&&m[4]!=null?parseFloat(m[4]):1;"
            + "var dark=m&&+m[1]<70&&+m[2]<70&&+m[3]<70;"
            + "var pos=st.position;var overlay=pos==='absolute'||pos==='fixed'||(dark&&alpha>0&&alpha<1);"
            + "if(!overlay&&st.backgroundImage==='none')continue;"
            + "el.style.setProperty('background','transparent','important');"
            + "el.style.setProperty('background-color','transparent','important');"
            + "el.style.setProperty('background-image','none','important');"
            + "el.style.setProperty('box-shadow','none','important');"
            + "el.style.setProperty('backdrop-filter','none','important')}}"
            + "function nuke(){var nodes=document.querySelectorAll('tiktok-cookie-banner,[class*=\"CookieBanner\"],[id*=\"cookie-banner\"]');"
            + "for(var i=0;i<nodes.length;i++)nodes[i].remove();"
            + "var all=document.querySelectorAll('div,section,aside,dialog');"
            + "for(var j=0;j<all.length;j++){var el=all[j];if(el.querySelector&&el.querySelector('video'))continue;"
            + "if((el.textContent||'').toLowerCase().indexOf('allow cookies from tiktok')!==-1)el.remove()}"
            + "var btns=document.querySelectorAll('button,[role=button]');"
            + "for(var k=0;k<btns.length;k++){var x=(btns[k].textContent||'').replace(/\\s+/g,' ').trim().toLowerCase();"
            + "if(x==='decline optional cookies'||x==='allow all')btns[k].click()}}"
            + "function bind(){var vs=document.querySelectorAll('video');for(var i=0;i<vs.length;i++){"
            + "if(vs[i].dataset.waPlay)continue;vs[i].dataset.waPlay='1';"
            + "vs[i].addEventListener('play',hidePlay);vs[i].addEventListener('playing',hidePlay)}}"
            + "var busy=false;function go(){if(busy)return;busy=true;try{nuke();fill();bind();hidePlay()}finally{busy=false}}"
            + "go();new MutationObserver(go).observe(document.documentElement,{childList:true,subtree:true})})()"
    }

    function embedKickScript() {
        return "(function(){function play(r){var ok=false;if(!r||!r.querySelectorAll)return ok;"
            + "var vs=r.querySelectorAll('video');"
            + "for(var k=0;k<vs.length;k++){var v=vs[k];v.playsInline=true;v.defaultMuted=false;v.muted=false;v.volume=1;"
            + "var p=v.play();if(p&&p.catch)p.catch(function(){});if(!v.paused&&!v.muted)ok=true}"
            + "var b=r.querySelector('[aria-label=\"Play\"],[aria-label=\"Play video\"]');if(b)b.click();return ok}"
            + "var playing=play(document);"
            + "try{var f=document.querySelectorAll('iframe');for(var j=0;j<f.length;j++){var d=f[j].contentDocument;if(d&&play(d))playing=true}}catch(e){}"
            + "return playing})()"
    }

    function showEmbed(preview, embed) {
        player.stop()
        win.viewer = {
            kind: "embed",
            embedUrl: embed || "",
            pageUrl: preview.url,
            filename: preview.title || preview.site || preview.label || "Link",
            failed: false
        }
        if (embed)
            embedWait.stop()
        else
            embedWait.restart()
    }

    Timer {
        id: embedWait
        interval: 20000
        onTriggered: {
            if (win.viewer && win.viewer.kind === "embed" && !win.viewer.embedUrl)
                win.viewer = {
                    kind: "embed",
                    embedUrl: "",
                    pageUrl: win.viewer.pageUrl,
                    filename: win.viewer.filename,
                    failed: true
                }
        }
    }

    function markEmbedLogin(host) {
        if (win.embedLogins[host])
            return
        var m = Object.assign({}, win.embedLogins)
        m[host] = true
        win.embedLogins = m
        WhatsApp.refreshEmbedLogins()
    }

    function embedHost(url) {
        var m = String(url || "").match(/https?:\/\/([^\/#?]+)/)
        return m ? m[1].replace(/^www\./, "") : ""
    }

    function svgIcon(body) {
        return "data:image/svg+xml;utf8," + encodeURIComponent(
            "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24'>" + body + "</svg>")
    }

    function embedBrandIcon(url) {
        var h = win.embedHost(url)
        if (h === "instagram.com")
            return win.svgIcon("<rect x='2.6' y='2.6' width='18.8' height='18.8' rx='5.4' fill='none' stroke='#E4405F' stroke-width='2.2'/>"
                + "<circle cx='12' cy='12' r='4.3' fill='none' stroke='#E4405F' stroke-width='2.2'/>"
                + "<circle cx='17.3' cy='6.7' r='1.4' fill='#E4405F'/>")
        if (h === "tiktok.com" || h === "vm.tiktok.com")
            return win.svgIcon("<path fill='#ffffff' d='M12.525.02c1.31-.02 2.61-.01 3.91-.02.08 1.53.63 3.09 1.75 4.17 1.12 1.11 2.7 1.62 4.24 1.79v4.03c-1.44-.05-2.89-.35-4.2-.97-.57-.26-1.1-.59-1.62-.93-.01 2.92.01 5.84-.02 8.75-.08 1.4-.54 2.79-1.35 3.94-1.31 1.92-3.58 3.17-5.91 3.21-1.43.08-2.86-.31-4.08-1.03-2.02-1.19-3.44-3.37-3.65-5.71-.02-.5-.03-1-.01-1.49.18-1.9 1.12-3.72 2.58-4.96 1.66-1.44 3.98-2.13 6.15-1.72.02 1.48-.04 2.96-.04 4.44-.99-.32-2.15-.23-3.02.37-.63.41-1.11 1.04-1.36 1.75-.21.51-.15 1.07-.14 1.61.24 1.64 1.82 3.02 3.5 2.87 1.12-.01 2.19-.66 2.77-1.61.19-.33.4-.67.41-1.06.1-1.79.06-3.57.07-5.36.01-4.03-.01-8.05.02-12.07z'/>")
        if (h === "youtube.com" || h === "youtu.be")
            return win.svgIcon("<path fill='#FF0000' d='M23.498 6.186a3.016 3.016 0 0 0-2.122-2.136C19.505 3.545 12 3.545 12 3.545s-7.505 0-9.377.505A3.017 3.017 0 0 0 .502 6.186C0 8.07 0 12 0 12s0 3.93.502 5.814a3.016 3.016 0 0 0 2.122 2.136c1.871.505 9.376.505 9.376.505s7.505 0 9.377-.505a3.015 3.015 0 0 0 2.122-2.136C24 15.93 24 12 24 12s0-3.93-.502-5.814zM9.545 15.568V8.432L15.818 12l-6.273 3.568z'/>")
        if (h === "facebook.com")
            return win.svgIcon("<path fill='#1877F2' d='M24 12.073c0-6.627-5.373-12-12-12s-12 5.373-12 12c0 5.99 4.388 10.954 10.125 11.854v-8.385H7.078v-3.47h3.047V9.43c0-3.007 1.792-4.669 4.533-4.669 1.312 0 2.686.235 2.686.235v2.953H15.83c-1.491 0-1.956.925-1.956 1.874v2.25h3.328l-.532 3.47h-2.796v8.385C19.612 23.027 24 18.062 24 12.073z'/>")
        if (h === "x.com" || h === "twitter.com")
            return win.svgIcon("<path fill='#ffffff' d='M18.901 1.153h3.68l-8.04 9.19L24 22.846h-7.406l-5.8-7.584-6.638 7.584H.474l8.6-9.83L0 1.154h7.594l5.243 6.932ZM17.61 20.644h2.039L6.486 3.24H4.298Z'/>")
        return ""
    }

    function openEmbedPage(preview) {
        var p = preview || win.viewer
        var target = p ? (p.url || p.pageUrl) : ""
        if (!target)
            return
        player.stop()
        win.viewer = {
            kind: "embed",
            embedUrl: target,
            pageUrl: target,
            filename: p.title || p.site || p.label || "Link",
            direct: true,
            failed: false
        }
        if (embedView.item)
            embedView.item.loadDirect(target)
    }

    function openLink(preview, external) {
        if (!preview || !preview.url)
            return
        if (external) {
            Qt.openUrlExternally(preview.url)
            return
        }
        var embed = Model.linkEmbed(preview)
        var host = win.embedHost(preview.url)
        // TikTok embeds are unreliable (age walls, login walls) and the real
        // page plays fine logged out, so always open it directly.
        if (host === "tiktok.com" || host === "vm.tiktok.com") {
            win.openEmbedPage(preview)
            return
        }
        if (embed) {
            win.showEmbed(preview, embed)
            return
        }
        if (Model.needsEmbedResolve(preview)) {
            win.showEmbed(preview, "")
            WhatsApp.fetchLinkPreview(preview.url)
            return
        }
        Qt.openUrlExternally(preview.url)
    }

    function focusMessage(id) {
        win.stickToEnd = false
        win.dropUnreadAnchor()
        for (var i = 0; i < thread.count; i++) {
            if (Model.messageHasId(thread.model[i], id)) {
                thread.positionViewAtIndex(i, ListView.Contain)
                win.highlightId = String((thread.model[i] && thread.model[i].id) || id)
                highlightTimer.restart()
                return
            }
        }
    }

    Timer {
        id: highlightTimer
        interval: 2000
        onTriggered: win.highlightId = ""
    }

    function showAndRaise() {
        win.show()
        win.raise()
        win.requestActivate()
    }

    onClosing: function(event) {
        event.accepted = false
        win.hide()
    }

    Shortcut {
        sequences: [StandardKey.Quit]
        onActivated: Shell.quit()
    }

    Shortcut {
        sequences: ["Ctrl+E"]
        enabled: !!win.chat
        onActivated: { win.dropUnreadAnchor(); win.goToNewest() }
    }

    Shortcut {
        sequences: ["Escape"]
        enabled: win.viewer === null
        onActivated: {
            if (WhatsApp.selectedJid) WhatsApp.selectedJid = ""
            else win.hide()
        }
    }

    Connections {
        target: Shell
        function onToggleRequested() {
            if (win.visible) win.hide()
            else win.showAndRaise()
        }
        function onOpenChatRequested(jid) {
            win.showAndRaise()
            if (jid) WhatsApp.selectChat(jid)
        }
    }

    Connections {
        target: WhatsApp
        function onSelectedJidChanged() {
            // Runs before the C++ side acks the chat, so acks still hold the
            // timestamp we last read up to.
            var chat = Model.chatByJid(WhatsApp.chats, WhatsApp.selectedJid)
            win.unreadAck = Number((WhatsApp.acks && WhatsApp.acks[WhatsApp.selectedJid]) || 0)
            win.unreadCount = chat ? Number(chat.unreadCount || 0) : 0
            win.unreadPending = !!chat && win.unreadCount > 0
                && Number(chat.lastMessageTs || 0) > win.unreadAck
            win.unreadId = ""
            win.unreadMarkId = ""
            win.stickToEnd = true
            win.anchorY = 0
            win.shownMessages = []
            win.shownJid = ""
            win.shownIds = ""
            win.adoptMessages()
            if (WhatsApp.selectedJid && !win.avatarSource(WhatsApp.selectedJid, WhatsApp.selectedChat ? WhatsApp.selectedChat.avatarUrl : ""))
                WhatsApp.fetchAvatar(WhatsApp.selectedJid)
        }
        function onMessagesChanged() {
            win.adoptMessages()
        }
        function onLinkPreviewsChanged() {
            if (!win.viewer || win.viewer.kind !== "embed" || win.viewer.embedUrl)
                return
            var page = win.viewer.pageUrl
            var fetched = (page && WhatsApp.linkPreviews) ? WhatsApp.linkPreviews[page] : null
            var embed = Model.linkEmbed({
                url: page,
                host: fetched && fetched.host ? fetched.host : "",
                embedUrl: fetched && fetched.embedUrl ? fetched.embedUrl : ""
            })
            if (embed) {
                embedWait.stop()
                win.showEmbed({ url: page, title: win.viewer.filename }, embed)
            }
        }
    }

    VoiceRecorder { id: voice }

    MediaPlayer {
        id: player
        audioOutput: AudioOutput {}
        videoOutput: videoOut
        source: win.viewer && win.viewer.fileUrl ? win.viewer.fileUrl : ""
        onMediaStatusChanged: if (mediaStatus === MediaPlayer.EndOfMedia) win.closeViewer()
    }

    ColumnLayout {
        anchors.fill: parent
        spacing: 0

        RowLayout {
            Layout.fillWidth: true
            Layout.fillHeight: true
            spacing: 0

            Rectangle {
                visible: !win.compact || !chat
                Layout.preferredWidth: win.compact ? parent.width : 320
                Layout.fillWidth: win.compact
                Layout.fillHeight: true
                color: Theme.railBg

                ColumnLayout {
                    anchors.fill: parent
                    anchors.margins: 10
                    spacing: 8
                    RowLayout {
                        Layout.fillWidth: true
                        spacing: 6
                        ControlsSearch {
                            id: search
                            Layout.fillWidth: true
                        }
                        AppButton {
                            text: "\u2713"
                            iconOnly: true
                            onClicked: WhatsApp.markAllRead()
                        }
                    }
                    Text {
                        visible: win.chats.length === 0
                        Layout.fillWidth: true
                        text: WhatsApp.lastError ? WhatsApp.lastError
                            : (WhatsApp.refreshing ? "Loading chats…" : "No chats yet")
                        textFormat: Text.PlainText
                        wrapMode: Text.Wrap
                        color: Theme.textDim
                        font.pixelSize: 12
                    }
                    ListView {
                        id: chatList
                        Layout.fillWidth: true
                        Layout.fillHeight: true
                        clip: true
                        model: win.chats
                        spacing: 2
                        delegate: Rectangle {
                            required property var modelData
                            width: chatList.width
                            height: 52
                            radius: Theme.radiusSmall
                            color: WhatsApp.selectedJid === modelData.jid ? Theme.selection : "transparent"
                            RowLayout {
                                anchors.fill: parent
                                anchors.margins: 8
                                spacing: 8
                                Avatar {
                                    jid: modelData.jid
                                    initials: Model.initials(modelData.name)
                                    source: win.avatarSource(modelData.jid, modelData.avatarUrl)
                                    Component.onCompleted: if (modelData.jid && !win.avatarSource(modelData.jid, modelData.avatarUrl))
                                        WhatsApp.fetchAvatar(modelData.jid)
                                }
                                ColumnLayout {
                                    Layout.fillWidth: true
                                    spacing: 0
                                    Text {
                                        Layout.fillWidth: true
                                        text: modelData.name
                                        textFormat: Text.PlainText
                                        elide: Text.ElideRight
                                        color: Theme.textPrimary
                                        font.bold: Model.visibleUnread(modelData, WhatsApp.acks, WhatsApp.selectedJid) > 0
                                        font.pixelSize: 13
                                    }
                                    Text {
                                        Layout.fillWidth: true
                                        text: modelData.preview
                                        textFormat: Text.PlainText
                                        elide: Text.ElideRight
                                        color: Theme.textDim
                                        font.pixelSize: 11
                                    }
                                }
                                ColumnLayout {
                                    Text {
                                        text: Model.formatDay(modelData.lastMessageTs)
                                        textFormat: Text.PlainText
                                        color: Theme.textDim
                                        font.pixelSize: 11
                                    }
                                    Rectangle {
                                        visible: Model.visibleUnread(modelData, WhatsApp.acks, WhatsApp.selectedJid) > 0
                                        Layout.alignment: Qt.AlignRight
                                        width: Math.max(16, unreadLabel.implicitWidth + 8)
                                        height: 16
                                        radius: 8
                                        color: Theme.accent
                                        Text {
                                            id: unreadLabel
                                            anchors.centerIn: parent
                                            text: String(Model.visibleUnread(modelData, WhatsApp.acks, WhatsApp.selectedJid))
                                            color: Theme.onAccent
                                            font.pixelSize: 10
                                            textFormat: Text.PlainText
                                        }
                                    }
                                }
                            }
                            MouseArea {
                                anchors.fill: parent
                                onClicked: WhatsApp.selectChat(modelData.jid)
                            }
                        }
                    }
                }
            }

            Rectangle {
                visible: !win.compact || !!chat
                Layout.fillWidth: true
                Layout.fillHeight: true
                color: Theme.windowBg

                ColumnLayout {
                    anchors.fill: parent
                    spacing: 0
                    Rectangle {
                        Layout.fillWidth: true
                        height: 44
                        color: Theme.railBg
                        RowLayout {
                            anchors.fill: parent
                            anchors.margins: 10
                            AppButton {
                                visible: win.compact
                                text: "Back"
                                onClicked: WhatsApp.selectedJid = ""
                            }
                            Avatar {
                                visible: !!chat
                                jid: chat ? chat.jid : ""
                                initials: Model.initials(chat ? chat.name : "")
                                source: chat ? win.avatarSource(chat.jid, chat.avatarUrl) : ""
                                Component.onCompleted: if (chat && chat.jid && !win.avatarSource(chat.jid, chat.avatarUrl))
                                    WhatsApp.fetchAvatar(chat.jid)
                            }
                            Text {
                                Layout.fillWidth: true
                                text: chat ? chat.name : "Select a chat"
                                textFormat: Text.PlainText
                                elide: Text.ElideRight
                                color: Theme.textPrimary
                                font.pixelSize: 14
                                font.bold: true
                            }
                        }
                    }
                    Item {
                        Layout.fillWidth: true
                        Layout.fillHeight: true
                        ListView {
                            id: thread
                            anchors.fill: parent
                            clip: true
                            spacing: 8
                            reuseItems: true
                            model: win.threadMessages
                            onMovementStarted: if (!win.pinning) win.dropUnreadAnchor()
                            onMovementEnded: {
                                restoreTimer.stop()
                                win.pinning = false
                                if (win.pinning)
                                    return
                                win.stickToEnd = atYEnd
                                win.captureAnchor()
                            }
                            onCountChanged: if (win.stickToEnd && !atYEnd && !moving && !win.pinning) win.goToNewest()
                            onContentHeightChanged: if (win.stickToEnd && !atYEnd && !moving && !win.pinning) win.goToNewest()
                            delegate: MessageDelegate {
                                required property var modelData
                                width: thread.width - 24
                                x: 12
                                message: modelData
                                isGroup: !!(chat && chat.isGroup)
                                onOpenMedia: function(msg) {
                                    win.viewer = msg
                                    if (msg.kind === "video" || msg.kind === "voice" || msg.kind === "audio") player.play()
                                }
                                onDownload: function(msg) {
                                    win.stickToEnd = false
                                    win.anchorY = thread.contentY
                                    WhatsApp.download(msg)
                                }
                                onReply: function(msg) { if (msg && !msg.pending) composer.reply = msg }
                                onReact: function(msg, emoji) { win.reactNow(msg, emoji) }
                                onPickReaction: function(msg) { win.pickReact(msg) }
                                onJumpTo: function(id) { win.focusMessage(id) }
                                onOpenLink: function(preview, external) { win.openLink(preview, external) }
                                highlighted: Model.messageHasId(modelData, win.highlightId)
                                unreadMark: !!win.unreadMarkId && Model.messageHasId(modelData, win.unreadMarkId)
                            }
                        }
                        Text {
                            anchors.centerIn: parent
                            visible: !!chat && thread.count === 0
                            text: WhatsApp.loadingMessages ? "Loading messages…" : "No messages yet"
                            textFormat: Text.PlainText
                            color: Theme.textDim
                            font.family: Theme.fontFamily
                            font.pixelSize: 13
                        }
                        Rectangle {
                            id: jumpNewest
                            visible: !!chat && thread.count > 0 && !thread.atYEnd
                            anchors.right: parent.right
                            anchors.bottom: parent.bottom
                            anchors.margins: 12
                            width: 36
                            height: 36
                            radius: 18
                            z: 5
                            color: jumpHover.hovered ? Theme.cardHover : Theme.cardBg
                            border.color: Theme.hairline
                            border.width: 1
                            Text {
                                anchors.centerIn: parent
                                text: "\uF078"
                                textFormat: Text.PlainText
                                color: Theme.textPrimary
                                font.family: Theme.fontFamily
                                font.pixelSize: 16
                            }
                            HoverHandler { id: jumpHover; cursorShape: Qt.PointingHandCursor }
                            TapHandler { onTapped: { win.dropUnreadAnchor(); win.goToNewest() } }
                        }
                    }
                    Text {
                        visible: !!WhatsApp.lastError && WhatsApp.lastError.toLowerCase().indexOf("busy syncing") === -1
                        Layout.fillWidth: true
                        Layout.margins: 8
                        text: WhatsApp.lastError
                        textFormat: Text.PlainText
                        wrapMode: Text.Wrap
                        color: Theme.red
                        font.pixelSize: 11
                    }
                    Composer {
                        id: composer
                        visible: !!chat
                        Layout.fillWidth: true
                        Layout.margins: 10
                        voice: voice
                        onSent: function(msg) { win.queueSend(msg) }
                    }
                }
            }
        }
    }

    Rectangle {
        visible: win.viewer !== null
        anchors.fill: parent
        color: Qt.rgba(0, 0, 0, 0.82)
        z: 20
        ColumnLayout {
            anchors.fill: parent
            anchors.margins: 20
            RowLayout {
                Layout.fillWidth: true
                Text {
                    Layout.fillWidth: true
                    Layout.minimumWidth: 0
                    elide: Text.ElideMiddle
                    text: Model.viewerTitle(win.viewer)
                    textFormat: Text.PlainText
                    color: "#ffffff"
                }
                AppButton {
                    visible: !!(win.viewer && win.viewer.kind === "embed" && win.viewer.pageUrl
                        && !win.viewer.direct)
                    icon: win.viewer && win.viewer.kind === "embed"
                        ? win.embedBrandIcon(win.viewer.pageUrl) : ""
                    text: "\uD83D\uDD11"
                    iconOnly: true
                    onClicked: win.openEmbedPage()
                }
                AppButton {
                    text: "\u2197"
                    iconOnly: true
                    onClicked: {
                        if (win.viewer && win.viewer.kind === "embed" && win.viewer.pageUrl)
                            Qt.openUrlExternally(win.viewer.pageUrl)
                        else if (win.viewer && win.viewer.localPath)
                            WhatsApp.openFile(win.viewer.localPath)
                    }
                }
                AppButton {
                    text: "Close"
                    onClicked: win.closeViewer()
                }
            }
            Item {
                Layout.fillWidth: true
                Layout.fillHeight: true
                Image {
                    anchors.fill: parent
                    visible: win.viewer && (win.viewer.kind === "image" || win.viewer.kind === "sticker")
                    source: win.viewer && win.viewer.fileUrl ? win.viewer.fileUrl : ""
                    fillMode: Image.PreserveAspectFit
                }
                AnimatedImage {
                    anchors.fill: parent
                    visible: win.viewer && win.viewer.kind === "gif"
                    source: win.viewer && win.viewer.fileUrl ? win.viewer.fileUrl : ""
                    fillMode: Image.PreserveAspectFit
                    playing: visible
                }
                VideoOutput {
                    id: videoOut
                    anchors.fill: parent
                    visible: win.viewer && (win.viewer.kind === "video" || win.viewer.kind === "voice" || win.viewer.kind === "audio")
                }
                Text {
                    anchors.centerIn: parent
                    visible: !!(win.viewer && win.viewer.kind === "embed" && !win.viewer.embedUrl)
                    text: (win.viewer && win.viewer.failed) ? "Couldn't play inline. Try 'Open page (sign in)' or open externally." : "Loading…"
                    textFormat: Text.PlainText
                    color: "#ffffff"
                    font.pixelSize: 14
                }
                Loader {
                    id: embedView
                    anchors.fill: parent
                    active: !!(win.viewer && win.viewer.kind === "embed" && win.viewer.embedUrl)
                    sourceComponent: Component {
                        WebEngineView {
                            id: embed
                            backgroundColor: "#000000"
                            audioMuted: false
                            settings.javascriptEnabled: true
                            settings.localStorageEnabled: true
                            settings.playbackRequiresUserGesture: false
                            profile: embedProfile
                            Component.onCompleted: boot()
                            function loadDirect(u) {
                                if (u)
                                    url = u
                            }
                            function boot() {
                                var u = win.embedWatchUrl(win.viewer && win.viewer.embedUrl)
                                if (!u)
                                    return
                                var origin = win.embedFrameOrigin(u)
                                if (origin && !win.viewer.direct) {
                                    loadHtml(win.embedFrameHtml(u), origin)
                                    return
                                }
                                url = u
                            }
                            function kickPlay() {
                                runJavaScript(win.embedKickScript(), function(ok) {
                                    if (ok)
                                        playTimer.stop()
                                })
                            }
                            Timer {
                                id: playTimer
                                interval: 250
                                repeat: true
                                triggeredOnStart: true
                                property int tries: 0
                                onTriggered: {
                                    tries += 1
                                    if (tries > 8) {
                                        stop()
                                        return
                                    }
                                    embed.kickPlay()
                                }
                            }
                            onLoadingChanged: function(info) {
                                if (info.status === WebEngineView.LoadSucceededStatus) {
                                    playTimer.tries = 0
                                    playTimer.restart()
                                    if (win.viewer && win.viewer.direct
                                        && win.embedHost(win.viewer.embedUrl) === "instagram.com")
                                        embed.runJavaScript(
                                            "(function(){return document.cookie.indexOf('ds_user_id')!==-1})()",
                                            function(ok) { if (ok) win.markEmbedLogin("instagram.com") })
                                }
                            }
                            onNavigationRequested: function(request) {
                                if (!request.isMainFrame
                                    || request.navigationType !== WebEngineView.LinkClickedNavigation
                                    || win.isEmbedPlayer(request.url.toString())
                                    || (win.viewer && win.viewer.direct)) {
                                    request.action = WebEngineView.AcceptRequest
                                    return
                                }
                                request.action = WebEngineView.IgnoreRequest
                            }
                            onNewWindowRequested: function(request) {
                            }
                        }
                    }
                }
                MouseArea {
                    anchors.fill: parent
                    visible: !embedView.active
                    cursorShape: Qt.PointingHandCursor
                    onClicked: win.closeViewer()
                }
            }
        }
        Keys.onEscapePressed: win.closeViewer()
    }

    Shortcut {
        enabled: win.viewer !== null
        sequences: ["Escape"]
        onActivated: win.closeViewer()
    }

    TextInput {
        id: emojiSink
        x: -20
        y: -20
        width: 1
        height: 1
        opacity: 0
        onTextChanged: {
            var emoji = String(text || "").trim()
            if (!win.pendingReactMsg || !emoji)
                return
            win.finishPick(emoji)
        }
    }

    component ControlsSearch: Rectangle {
        id: box
        property alias text: field.text
        implicitHeight: 32
        radius: Theme.radiusSmall
        color: Theme.cardBg
        border.color: Theme.hairline
        border.width: 1
        TextInput {
            id: field
            anchors.fill: parent
            anchors.margins: 8
            color: Theme.textPrimary
            font.family: Theme.fontFamily
            font.pixelSize: 13
            clip: true
            onTextChanged: win.query = text
        }
        Text {
            anchors.fill: field
            text: "Search"
            color: Theme.textDim
            visible: field.text.length === 0 && !field.activeFocus
            font.pixelSize: 13
        }
    }
}
