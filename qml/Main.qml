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

    function restoreAnchor() {
        if (shownJid !== WhatsApp.selectedJid) {
            win.pinning = false
            return
        }
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
            if (tries > 15 || thread.moving) {
                stop()
                win.pinning = false
                return
            }
            thread.contentY = win.anchorY
        }
    }

    function closeViewer() {
        player.stop()
        win.viewer = null
    }

    WebEngineProfile {
        id: embedProfile
        storageName: "whatsapp-link-embed"
        offTheRecord: false
        persistentCookiesPolicy: WebEngineProfile.ForcePersistentCookies
        Component.onCompleted: {
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

    function openLink(preview, external) {
        if (!preview || !preview.url)
            return
        if (external) {
            Qt.openUrlExternally(preview.url)
            return
        }
        var embed = Model.linkEmbed(preview)
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
        onActivated: win.goToNewest()
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
                    ControlsSearch {
                        id: search
                        Layout.fillWidth: true
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
                            TapHandler { onTapped: win.goToNewest() }
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
                    text: win.viewer ? (win.viewer.filename || win.viewer.kind) : ""
                    textFormat: Text.PlainText
                    color: "#ffffff"
                }
                AppButton {
                    text: "Open externally"
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
                    text: (win.viewer && win.viewer.failed) ? "Couldn't play inline. Open externally." : "Loading…"
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
                            function boot() {
                                var u = win.embedWatchUrl(win.viewer && win.viewer.embedUrl)
                                if (!u)
                                    return
                                var origin = win.embedFrameOrigin(u)
                                if (origin) {
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
                                }
                            }
                            onNavigationRequested: function(request) {
                                if (!request.isMainFrame
                                    || request.navigationType !== WebEngineView.LinkClickedNavigation
                                    || win.isEmbedPlayer(request.url.toString())) {
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
