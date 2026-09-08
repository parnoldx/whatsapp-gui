import QtQuick
import QtQuick.Controls.Basic
import "Model.js" as Model

Item {
    id: root
    property var message: ({})
    property bool isGroup: false
    readonly property var reactEmojis: ["👍", "❤️", "😂", "😮", "😢", "🙏"]

    signal openMedia(var message)
    signal download(var message)
    signal reply(var message)
    signal react(var message, string emoji)
    signal pickReaction(var message)

    property bool reactOpen: false
    readonly property string messageId: (message && message.id) ? message.id : ""
    onMessageIdChanged: reactOpen = false

    readonly property bool fromMe: message && message.fromMe === true
    readonly property string kind: message && message.kind ? message.kind : "text"
    readonly property var linked: Model.linkify((message && (message.text || message.caption)) || "")
    readonly property var preview: {
        var fromMsg = message && message.linkPreview
        var parsed = fromMsg && fromMsg.url ? fromMsg : Model.parseLink((message && (message.text || message.caption)) || "")
        var url = parsed && parsed.url ? parsed.url : ""
        if (url && WhatsApp.linkPreviews && WhatsApp.linkPreviews[url])
            return WhatsApp.linkPreviews[url]
        return parsed
    }
    readonly property bool urlOnly: !!(preview && preview.url
        && Model.textIsOnlyUrl((message && (message.text || message.caption)) || "", preview.url))
    readonly property var palette: Theme.avatarPalette

    implicitHeight: col.implicitHeight
    readonly property string previewUrl: preview && preview.url ? preview.url : ""

    onPreviewUrlChanged: {
        if (previewUrl && preview && !preview.fetched && !preview.imageUrl)
            WhatsApp.fetchLinkPreview(previewUrl)
    }

    Component.onCompleted: {
        if (previewUrl && preview && !preview.fetched && !preview.imageUrl)
            WhatsApp.fetchLinkPreview(previewUrl)
    }

    Column {
        id: col
        anchors.left: fromMe ? undefined : parent.left
        anchors.right: fromMe ? parent.right : undefined
        spacing: 4
        width: Math.min(root.width * 0.78, 420)

        Text {
            visible: root.isGroup && !root.fromMe && !!(message && message.senderName)
            text: (message && message.senderName) || ""
            textFormat: Text.PlainText
            color: Theme.avatarColor((message && message.senderJid) || "")
            font.family: Theme.fontFamily
            font.pixelSize: 11
            font.bold: true
        }

        Rectangle {
            id: bubble
            width: col.width
            height: inner.implicitHeight + 16
            radius: Theme.radiusSmall
            color: fromMe ? Qt.rgba(Theme.accent.r, Theme.accent.g, Theme.accent.b, 0.18)
                          : Qt.rgba(Theme.foreground.r, Theme.foreground.g, Theme.foreground.b, 0.08)

            Column {
                id: inner
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.verticalCenter: parent.verticalCenter
                anchors.margins: 8
                spacing: 6

                Rectangle {
                    visible: !!(message && message.quotedText)
                    width: parent.width
                    height: quoteCol.implicitHeight + 8
                    radius: Theme.radiusSmall
                    color: Qt.rgba(Theme.foreground.r, Theme.foreground.g, Theme.foreground.b, 0.08)
                    Column {
                        id: quoteCol
                        anchors.left: parent.left
                        anchors.right: parent.right
                        anchors.verticalCenter: parent.verticalCenter
                        anchors.margins: 6
                        Text {
                            visible: !!(message && message.quotedSender)
                            text: (message && message.quotedSender) || ""
                            textFormat: Text.PlainText
                            color: Theme.accent
                            font.pixelSize: 11
                            font.bold: true
                        }
                        Text {
                            width: parent.width
                            text: (message && message.quotedText) || ""
                            textFormat: Text.PlainText
                            wrapMode: Text.Wrap
                            elide: Text.ElideRight
                            maximumLineCount: 3
                            color: Theme.textPrimary
                            font.pixelSize: 11
                        }
                    }
                }

                Item {
                    visible: kind === "image" || kind === "sticker" || kind === "gif"
                    width: parent.width
                    height: kind === "sticker" ? 140 : 220
                    Image {
                        anchors.fill: parent
                        visible: kind !== "gif" && message.downloaded
                        source: message.fileUrl || ""
                        fillMode: Image.PreserveAspectFit
                        asynchronous: true
                        cache: true
                    }
                    AnimatedImage {
                        anchors.fill: parent
                        visible: kind === "gif" && message.downloaded
                        source: message.fileUrl || ""
                        fillMode: Image.PreserveAspectFit
                        playing: true
                    }
                    Rectangle {
                        anchors.fill: parent
                        visible: !message.downloaded
                        color: Qt.rgba(0, 0, 0, 0.25)
                        Text {
                            anchors.centerIn: parent
                            text: "Download " + Model.kindLabel(kind)
                            textFormat: Text.PlainText
                            color: Theme.textPrimary
                        }
                    }
                    MouseArea {
                        anchors.fill: parent
                        onClicked: message.downloaded ? root.openMedia(message) : root.download(message)
                    }
                }

                Rectangle {
                    visible: kind === "video"
                    width: parent.width
                    height: 180
                    color: Qt.rgba(0, 0, 0, 0.35)
                    radius: Theme.radiusSmall
                    clip: true
                    Image {
                        anchors.fill: parent
                        visible: !!(message.thumbUrl)
                        source: message.thumbUrl || ""
                        fillMode: Image.PreserveAspectFit
                        asynchronous: true
                        cache: true
                    }
                    Text {
                        anchors.centerIn: parent
                        visible: !message.thumbUrl
                        text: message.downloaded ? "Play video" : "Download video"
                        textFormat: Text.PlainText
                        color: Theme.textPrimary
                    }
                    Rectangle {
                        visible: !!(message.thumbUrl)
                        anchors.centerIn: parent
                        width: 44
                        height: 44
                        radius: 22
                        color: Qt.rgba(0, 0, 0, 0.45)
                        Text {
                            anchors.centerIn: parent
                            text: "\u25B6"
                            textFormat: Text.PlainText
                            color: "#ffffff"
                            font.pixelSize: 16
                        }
                    }
                    MouseArea {
                        anchors.fill: parent
                        onClicked: message.downloaded ? root.openMedia(message) : root.download(message)
                    }
                }

                Rectangle {
                    visible: kind === "voice" || kind === "audio" || kind === "document"
                    width: parent.width
                    height: 36
                    radius: Theme.radiusSmall
                    color: Qt.rgba(Theme.foreground.r, Theme.foreground.g, Theme.foreground.b, 0.08)
                    Text {
                        anchors.centerIn: parent
                        width: parent.width - 12
                        elide: Text.ElideMiddle
                        horizontalAlignment: Text.AlignHCenter
                        text: kind === "document"
                            ? (message.filename || "Document")
                            : (message.downloaded
                                ? (kind === "voice" ? "Voice note" : "Audio")
                                : "Download " + (kind === "voice" ? "voice note" : "audio"))
                        textFormat: Text.PlainText
                        color: Theme.textPrimary
                    }
                    MouseArea {
                        anchors.fill: parent
                        onClicked: message.downloaded ? root.openMedia(message) : root.download(message)
                    }
                }

                Text {
                    visible: kind === "location"
                    width: parent.width
                    wrapMode: Text.Wrap
                    text: ((message.locationName || "Location") + (message.locationAddress ? "\n" + message.locationAddress : ""))
                    textFormat: Text.PlainText
                    color: Theme.textPrimary
                    font.pixelSize: 12
                }

                Text {
                    visible: !!(linked.plain) && !urlOnly
                    width: parent.width
                    wrapMode: Text.Wrap
                    textFormat: linked.hasLinks ? Text.StyledText : Text.PlainText
                    text: linked.hasLinks ? linked.html : linked.plain
                    color: Theme.textPrimary
                    font.family: Theme.fontFamily
                    font.pixelSize: 13
                    onLinkActivated: function(link) { Qt.openUrlExternally(link) }
                }

                Rectangle {
                    visible: !!(preview && preview.url)
                    width: parent.width
                    height: previewCol.implicitHeight
                    radius: Theme.radiusSmall
                    color: Qt.rgba(Theme.foreground.r, Theme.foreground.g, Theme.foreground.b, 0.08)
                    clip: true
                    Column {
                        id: previewCol
                        width: parent.width
                        spacing: 0
                        Item {
                            width: parent.width
                            height: 158
                            Rectangle {
                                anchors.fill: parent
                                color: Qt.rgba(0, 0, 0, 0.38)
                                visible: previewImage.status !== Image.Ready
                                Text {
                                    anchors.centerIn: parent
                                    text: (preview && preview.site) || "Link"
                                    textFormat: Text.PlainText
                                    color: "#ffffff"
                                    font.pixelSize: 13
                                    font.bold: true
                                }
                            }
                            Image {
                                id: previewImage
                                anchors.fill: parent
                                source: (preview && preview.imageUrl) || ""
                                fillMode: Image.PreserveAspectCrop
                                asynchronous: true
                                cache: true
                                visible: status === Image.Ready
                            }
                        }
                        Column {
                            width: parent.width
                            leftPadding: 8
                            rightPadding: 8
                            topPadding: 8
                            bottomPadding: 8
                            spacing: 4
                            Text {
                                text: (preview && preview.site) || ""
                                textFormat: Text.PlainText
                                color: Theme.accent
                                font.pixelSize: 11
                                font.bold: true
                            }
                            Text {
                                width: parent.width - 16
                                visible: !!(preview && (preview.title || preview.label))
                                text: (preview && (preview.title || preview.label)) || ""
                                textFormat: Text.PlainText
                                wrapMode: Text.Wrap
                                maximumLineCount: 3
                                elide: Text.ElideRight
                                color: Theme.textPrimary
                                font.pixelSize: 13
                            }
                            Text {
                                width: parent.width - 16
                                visible: !!(preview && preview.description)
                                text: (preview && preview.description) || ""
                                textFormat: Text.PlainText
                                wrapMode: Text.Wrap
                                maximumLineCount: 2
                                elide: Text.ElideRight
                                color: Theme.textDim
                                font.pixelSize: 11
                            }
                            Text {
                                width: parent.width - 16
                                text: (preview && preview.host) || ""
                                textFormat: Text.PlainText
                                elide: Text.ElideMiddle
                                color: Theme.textDim
                                font.pixelSize: 11
                            }
                        }
                    }
                    MouseArea {
                        anchors.fill: parent
                        onClicked: if (preview && preview.url) Qt.openUrlExternally(preview.url)
                    }
                }

                Row {
                    spacing: 6
                    anchors.right: parent.right
                    Text {
                        visible: !!(message && message.forwarded)
                        text: "Fwd"
                        textFormat: Text.PlainText
                        color: Theme.textDim
                        font.pixelSize: 11
                    }
                    Text {
                        visible: !!(message && message.edited)
                        text: "edited"
                        textFormat: Text.PlainText
                        color: Theme.textDim
                        font.pixelSize: 11
                    }
                    Text {
                        text: Model.formatTime(message.ts)
                        textFormat: Text.PlainText
                        color: Theme.textDim
                        font.pixelSize: 11
                    }
                    Text {
                        text: "\uF118"
                        textFormat: Text.PlainText
                        color: root.reactOpen ? Theme.accent : Theme.textDim
                        font.family: Theme.fontFamily
                        font.pixelSize: 13
                        MouseArea {
                            anchors.fill: parent
                            anchors.margins: -4
                            cursorShape: Qt.PointingHandCursor
                            onClicked: root.reactOpen = !root.reactOpen
                        }
                    }
                }

                Row {
                    visible: root.reactOpen
                    spacing: 4
                    Repeater {
                        model: root.reactEmojis
                        Rectangle {
                            required property string modelData
                            height: 22
                            width: 22
                            radius: 11
                            color: root.message && root.message.myReaction === modelData
                                ? Qt.rgba(Theme.accent.r, Theme.accent.g, Theme.accent.b, 0.30)
                                : Qt.rgba(Theme.foreground.r, Theme.foreground.g, Theme.foreground.b, 0.12)
                            Text {
                                anchors.centerIn: parent
                                text: modelData
                                textFormat: Text.PlainText
                                font.pixelSize: 13
                            }
                            MouseArea {
                                anchors.fill: parent
                                onClicked: {
                                    root.react(root.message,
                                        (root.message && root.message.myReaction === modelData) ? "" : modelData)
                                    root.reactOpen = false
                                }
                            }
                        }
                    }
                    Rectangle {
                        height: 22
                        width: 22
                        radius: 11
                        color: Qt.rgba(Theme.foreground.r, Theme.foreground.g, Theme.foreground.b, 0.12)
                        Text {
                            anchors.centerIn: parent
                            text: "+"
                            textFormat: Text.PlainText
                            color: Theme.textPrimary
                            font.pixelSize: 14
                        }
                        MouseArea {
                            anchors.fill: parent
                            onClicked: {
                                root.reactOpen = false
                                root.pickReaction(root.message)
                            }
                        }
                    }
                }

                Row {
                    visible: !!(message && message.reactions && message.reactions.length)
                    spacing: 4
                    Repeater {
                        model: (message && message.reactions) || []
                        Rectangle {
                            required property var modelData
                            height: 18
                            width: chip.implicitWidth + 12
                            radius: 9
                            color: modelData.mine
                                ? Qt.rgba(Theme.accent.r, Theme.accent.g, Theme.accent.b, 0.30)
                                : Qt.rgba(Theme.foreground.r, Theme.foreground.g, Theme.foreground.b, 0.12)
                            Text {
                                id: chip
                                anchors.centerIn: parent
                                text: modelData.emoji + (modelData.count > 1 ? " " + modelData.count : "")
                                textFormat: Text.PlainText
                                color: Theme.textPrimary
                                font.pixelSize: 11
                            }
                            MouseArea {
                                anchors.fill: parent
                                hoverEnabled: true
                                onClicked: root.react(root.message, modelData.mine ? "" : modelData.emoji)
                                ToolTip.visible: containsMouse && !!tipText
                                ToolTip.text: tipText
                                readonly property string tipText:
                                    (modelData.who && modelData.who.length ? modelData.who.join(", ") : "")
                            }
                        }
                    }
                }
            }

            MouseArea {
                anchors.fill: parent
                acceptedButtons: Qt.RightButton
                onClicked: ctxMenu.popup()
            }

            Menu {
                id: ctxMenu
                MenuItem {
                    text: "Reply"
                    onTriggered: root.reply(root.message)
                }
                MenuItem {
                    text: "React…"
                    onTriggered: root.pickReaction(root.message)
                }
                MenuItem {
                    text: "Remove reaction"
                    enabled: !!(root.message && root.message.myReaction)
                    onTriggered: root.react(root.message, "")
                }
            }
        }
    }
}
