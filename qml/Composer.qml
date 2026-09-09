import QtQuick
import QtQuick.Layouts
import QtQuick.Controls as Controls
import QtQuick.Window
import "Model.js" as Model

Item {
    id: root
    property var voice: null
    property string chatJid: WhatsApp.selectedJid
    property bool isGroup: !!(WhatsApp.selectedChat && WhatsApp.selectedChat.isGroup)
    property var files: []
    property var reply: null
    property var mentionJids: []
    property int mentionIndex: 0

    readonly property var mention: Model.mentionToken(input.text, input.cursorPosition)
    readonly property var mentionMatches: isGroup && mention
        ? Model.filterMembers(WhatsApp.participants, mention.query) : []
    readonly property bool mentionOpen: mentionMatches.length > 0

    implicitHeight: col.implicitHeight
    Layout.fillWidth: true
    readonly property int maxInputHeight: {
        var w = Window.window
        var h = w ? w.height : 720
        return Math.max(120, Math.min(360, Math.floor(h * 0.4)))
    }

    function restore() {
        var draft = WhatsApp.draftFor(chatJid)
        input.text = (draft && draft.text) || ""
        files = (draft && draft.files) || []
        reply = (draft && draft.reply) || null
        mentionJids = (draft && draft.mentions) || []
    }

    function persist() {
        if (!chatJid) return
        WhatsApp.saveDraft(chatJid, { text: input.text, files: files, reply: reply, mentions: mentionJids })
    }

    function clear() {
        input.text = ""
        files = []
        reply = null
        mentionJids = []
        persist()
    }

    signal sent(var message)

    function quoteFrom(msg) {
        if (!msg || !msg.id) return { id: "", sender: "", text: "" }
        return {
            id: msg.id,
            sender: msg.senderName || "",
            text: String(msg.text || msg.caption || "").slice(0, 280)
        }
    }

    function send() {
        if (!chatJid) return
        var q = quoteFrom(reply)
        if (voice && voice.previewing) {
            sent(Model.pendingMessage({
                chatJid: chatJid, kind: "voice", localPath: voice.path,
                fileUrl: String(voice.fileUrl || ""), downloaded: true,
                quotedId: q.id, quotedSender: q.sender, quotedText: q.text
            }))
            WhatsApp.sendVoice(voice.path, q.id)
            voice.discard()
            reply = null
            persist()
            return
        }
        var caption = input.text
        var pending = files.slice()
        if (pending.length > 0) {
            for (var i = 0; i < pending.length; i++) {
                var f = pending[i]
                var body = i === 0 ? caption : ""
                sent(Model.pendingMessage({
                    chatJid: chatJid, kind: Model.fileKind(f.name || f.path),
                    text: body, caption: body, filename: f.name || "",
                    localPath: f.path || "", fileUrl: f.fileUrl || "", downloaded: true,
                    quotedId: q.id, quotedSender: q.sender, quotedText: q.text
                }))
                WhatsApp.sendFile(f.path, body, q.id)
            }
            clear()
            return
        }
        if (!caption.trim()) return
        sent(Model.pendingMessage({
            chatJid: chatJid, kind: "text", text: caption,
            quotedId: q.id, quotedSender: q.sender, quotedText: q.text
        }))
        WhatsApp.sendText(caption, mentionJids, q.id)
        clear()
    }

    function pick() {
        WhatsApp.pickFiles(function(err, data) {
            if (err) return
            var picked = (data && data.files) || []
            files = files.concat(picked).slice(0, 10)
            persist()
        })
    }

    function pasteClipboard() {
        WhatsApp.clipboard(function(err, data) {
            if (err || !data) return
            if (data.kind === "file") {
                files = files.concat([{ path: data.path, fileUrl: data.fileUrl, name: data.name }]).slice(0, 10)
                persist()
            } else if (data.kind === "text" && data.text) {
                input.insert(input.cursorPosition, data.text)
            }
        })
    }

    function chooseMention(member) {
        if (!member) return
        var applied = Model.applyMention(input.text, input.cursorPosition, member.name)
        input.text = applied.text
        input.cursorPosition = applied.cursor
        var next = mentionJids.slice()
        if (next.indexOf(member.jid) === -1) next.push(member.jid)
        mentionJids = next
        persist()
    }

    onChatJidChanged: restore()

    Column {
        id: col
        width: parent.width
        spacing: 8

        Rectangle {
            visible: !!(reply && reply.id)
            width: parent.width
            height: 32
            radius: Theme.radiusSmall
            color: Theme.cardBg
            RowLayout {
                anchors.fill: parent
                anchors.margins: 8
                Text {
                    Layout.fillWidth: true
                    text: "Replying to " + ((reply && (reply.senderName || "message")) || "")
                    textFormat: Text.PlainText
                    elide: Text.ElideRight
                    color: Theme.textPrimary
                    font.pixelSize: 11
                }
                AppButton { text: "×"; onClicked: { root.reply = null; root.persist() } }
            }
        }

        Flow {
            width: parent.width
            spacing: 4
            visible: files.length > 0
            Repeater {
                model: files
                Rectangle {
                    required property var modelData
                    required property int index
                    height: 24
                    width: chipText.implicitWidth + 20
                    radius: Theme.radiusSmall
                    color: Theme.cardBg
                    Text {
                        id: chipText
                        anchors.centerIn: parent
                        text: modelData.name || "file"
                        textFormat: Text.PlainText
                        color: Theme.textPrimary
                        font.pixelSize: 11
                    }
                    MouseArea {
                        anchors.fill: parent
                        onClicked: {
                            var next = root.files.slice()
                            next.splice(index, 1)
                            root.files = next
                            root.persist()
                        }
                    }
                }
            }
        }

        Rectangle {
            visible: root.mentionOpen
            width: parent.width
            height: Math.min(160, mentionList.contentHeight + 8)
            radius: Theme.radiusSmall
            color: Theme.windowBg
            border.color: Theme.hairline
            border.width: 1
            ListView {
                id: mentionList
                anchors.fill: parent
                anchors.margins: 4
                clip: true
                model: root.mentionMatches
                currentIndex: root.mentionIndex
                delegate: Rectangle {
                    required property var modelData
                    required property int index
                    width: mentionList.width
                    height: 24
                    color: index === root.mentionIndex ? Theme.cardHover : "transparent"
                    Text {
                        anchors.verticalCenter: parent.verticalCenter
                        anchors.left: parent.left
                        anchors.leftMargin: 6
                        text: modelData.name
                        textFormat: Text.PlainText
                        color: Theme.textPrimary
                    }
                    MouseArea { anchors.fill: parent; onClicked: root.chooseMention(modelData) }
                }
            }
        }

        RowLayout {
            visible: voice && (voice.recording || voice.previewing)
            width: parent.width
            spacing: 6
            Text {
                Layout.fillWidth: true
                text: voice && voice.recording
                    ? "Recording… " + Math.round((voice.durationMs || 0) / 1000) + "s"
                    : "Voice note ready — Send or discard"
                textFormat: Text.PlainText
                color: Theme.accent
                font.pixelSize: 11
            }
            AppButton {
                visible: !!(voice && voice.previewing)
                text: "×"
                iconOnly: true
                onClicked: if (voice) voice.discard()
            }
        }

        RowLayout {
            width: parent.width
            spacing: 6
            AppButton {
                text: "+"
                iconOnly: true
                Layout.alignment: Qt.AlignBottom
                onClicked: root.pick()
            }
            Controls.ScrollView {
                id: inputScroll
                Layout.fillWidth: true
                Layout.preferredHeight: Math.max(36, Math.min(root.maxInputHeight, input.implicitHeight))
                Layout.maximumHeight: root.maxInputHeight
                Layout.alignment: Qt.AlignBottom
                clip: true
                contentWidth: availableWidth
                Controls.TextArea {
                    id: input
                    width: inputScroll.availableWidth
                    wrapMode: TextEdit.Wrap
                    color: Theme.textPrimary
                    font.family: Theme.fontFamily
                    font.pixelSize: 13
                    placeholderText: "Message"
                    background: Rectangle { radius: Theme.radiusSmall; color: Theme.cardBg }
                    onTextChanged: root.persist()
                    Keys.onPressed: function(event) {
                        if (root.mentionOpen && (event.key === Qt.Key_Down || event.key === Qt.Key_Up)) {
                            event.accepted = true
                            var delta = event.key === Qt.Key_Down ? 1 : -1
                            root.mentionIndex = (root.mentionIndex + delta + root.mentionMatches.length) % root.mentionMatches.length
                            return
                        }
                        if (root.mentionOpen && (event.key === Qt.Key_Return || event.key === Qt.Key_Enter) && !(event.modifiers & Qt.ShiftModifier)) {
                            event.accepted = true
                            root.chooseMention(root.mentionMatches[root.mentionIndex])
                            return
                        }
                        if ((event.key === Qt.Key_Return || event.key === Qt.Key_Enter) && !(event.modifiers & Qt.ShiftModifier)) {
                            event.accepted = true
                            root.send()
                            return
                        }
                        if (event.key === Qt.Key_V && (event.modifiers & Qt.ControlModifier) && (event.modifiers & Qt.ShiftModifier)) {
                            event.accepted = true
                            if (voice) voice.toggle()
                            return
                        }
                        if (event.key === Qt.Key_V && (event.modifiers & Qt.ControlModifier)) {
                            event.accepted = true
                            root.pasteClipboard()
                            return
                        }
                        if (event.key === Qt.Key_O && (event.modifiers & Qt.ControlModifier)) {
                            event.accepted = true
                            root.pick()
                        }
                    }
                }
            }
            AppButton {
                iconOnly: true
                Layout.alignment: Qt.AlignBottom
                text: voice && voice.recording ? "\uF04D" : "\uF130"
                kind: voice && voice.recording ? "primary" : "ghost"
                onClicked: {
                    if (!voice) return
                    if (voice.previewing) voice.discard()
                    voice.toggle()
                }
            }
            AppButton {
                text: "Send"
                kind: "primary"
                Layout.alignment: Qt.AlignBottom
                onClicked: root.send()
            }
        }
    }
}
