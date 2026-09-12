import QtQuick

Rectangle {
    id: root
    property string text: ""
    property string icon: ""
    property string kind: "ghost"
    property bool active: true
    property bool iconOnly: false
    signal clicked()

    readonly property bool primary: kind === "primary"
    implicitWidth: iconOnly ? implicitHeight : label.implicitWidth + 24
    implicitHeight: 32
    radius: Theme.radiusSmall
    opacity: active ? 1 : 0.4
    color: primary ? (hover.hovered ? Qt.lighter(Theme.accent, 1.12) : Theme.accent)
                   : hover.hovered ? Theme.cardHover : "transparent"
    border.width: primary ? 0 : 1
    border.color: Theme.hairline

    Text {
        id: label
        anchors.centerIn: parent
        visible: root.icon === ""
        text: root.text
        textFormat: Text.PlainText
        font.family: Theme.fontFamily
        font.pixelSize: root.iconOnly ? 16 : 13
        color: root.primary ? Theme.onAccent : Theme.textPrimary
    }

    Image {
        anchors.centerIn: parent
        visible: root.icon !== ""
        source: root.icon
        width: 16
        height: 16
        fillMode: Image.PreserveAspectFit
        smooth: true
    }

    HoverHandler { id: hover; enabled: root.active; cursorShape: Qt.PointingHandCursor }
    TapHandler { enabled: root.active; onTapped: root.clicked() }
}
