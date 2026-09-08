import QtQuick
import Qt5Compat.GraphicalEffects

Item {
    id: root
    property string jid: ""
    property string initials: "?"
    property string source: ""

    width: 32
    height: 32

    Rectangle {
        anchors.fill: parent
        radius: width / 2
        color: Theme.avatarColor(root.jid)
        clip: true

        Text {
            anchors.centerIn: parent
            visible: pic.status !== Image.Ready
            text: root.initials
            color: Theme.onAccent
            font.bold: true
            font.pixelSize: Math.max(10, Math.round(root.width * 0.34))
            textFormat: Text.PlainText
        }

        Image {
            id: pic
            anchors.fill: parent
            source: root.source
            fillMode: Image.PreserveAspectCrop
            asynchronous: true
            cache: true
            visible: false
        }

        // Rectangle clip is square, so round the picture with a mask.
        OpacityMask {
            anchors.fill: parent
            visible: pic.status === Image.Ready
            source: pic
            maskSource: Rectangle {
                width: pic.width
                height: pic.height
                radius: pic.width / 2
            }
        }
    }
}
