import QtQuick
import QtQuick.Controls.Basic
import QtQuick.Layouts
import QtMultimedia
import QtQuick.Window
import "Model.js" as Model

ApplicationWindow {
    id: win
    width: 1100
    height: 720
    minimumWidth: 720
    minimumHeight: 480
    visible: true
    title: "WhatsApp"
    color: Theme.windowBg

    Behavior on color { ColorAnimation { duration: Theme.anim } }

    readonly property bool compact: width < 860
    property string query: ""
    property var viewer: null
    property bool stickToEnd: true
    property bool pinning: false
    property string anchorId: ""
    property real anchorY: 0
    property var shownMessages: []
    property string shownJid: ""
    property string shownIds: ""
    property var pendingReactions: ({})
    property var pendingSends: []
    property var pendingReactMsg: null
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
        win.anchorId = ""
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
        if (pinning || stickToEnd || !thread.count) {
            if (stickToEnd)
                anchorId = ""
            return
        }
        anchorY = thread.contentY
        var idx = thread.indexAt(thread.width / 2, thread.contentY + 24)
        if (idx < 0)
            idx = thread.indexAt(thread.width / 2, thread.contentY + 80)
        var item = idx >= 0 ? thread.model[idx] : null
        anchorId = item && item.id ? item.id : anchorId
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
        if (anchorId) {
            var list = thread.model
            var n = thread.count
            for (var i = 0; i < n; i++) {
                var item = list[i]
                if (item && item.id === anchorId) {
                    thread.positionViewAtIndex(i, ListView.Contain)
                    Qt.callLater(function() { win.pinning = false })
                    return
                }
            }
        }
        thread.contentY = anchorY
        Qt.callLater(function() { win.pinning = false })
    }

    function closeViewer() {
        player.stop()
        win.viewer = null
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
            win.anchorId = ""
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
    }

    VoiceRecorder { id: voice }

    MediaPlayer {
        id: player
        audioOutput: AudioOutput {}
        videoOutput: videoOut
        source: win.viewer && win.viewer.fileUrl ? win.viewer.fileUrl : ""
    }

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
                spacing: 8
                Text {
                    text: "WhatsApp"
                    textFormat: Text.PlainText
                    color: Theme.textPrimary
                    font.family: Theme.fontFamily
                    font.pixelSize: 14
                    font.bold: true
                }
                Item { Layout.fillWidth: true }
                Text {
                    text: WhatsApp.activity
                    textFormat: Text.PlainText
                    color: (WhatsApp.sending || WhatsApp.loadingMessages || WhatsApp.refreshing || WhatsApp.syncActive)
                           ? Theme.accent : Theme.textDim
                    font.family: Theme.fontFamily
                    font.pixelSize: 12
                    elide: Text.ElideLeft
                }
            }
        }

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
                            Text {
                                visible: !!(chat && chat.isGroup)
                                text: "group"
                                textFormat: Text.PlainText
                                color: Theme.textDim
                                font.pixelSize: 11
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
                                    win.anchorId = msg && msg.id ? msg.id : win.anchorId
                                    win.anchorY = thread.contentY
                                    WhatsApp.download(msg)
                                }
                                onReply: function(msg) { if (msg && !msg.pending) composer.reply = msg }
                                onReact: function(msg, emoji) { win.reactNow(msg, emoji) }
                                onPickReaction: function(msg) { win.pickReact(msg) }
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
                    onClicked: if (win.viewer && win.viewer.localPath) WhatsApp.openFile(win.viewer.localPath)
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
                MouseArea {
                    anchors.fill: parent
                    cursorShape: Qt.PointingHandCursor
                    onClicked: win.closeViewer()
                }
            }
        }
        Keys.onEscapePressed: win.closeViewer()
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
