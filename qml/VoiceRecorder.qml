import QtQuick
import QtMultimedia

Item {
    id: root
    property string path: ""
    property url fileUrl: ""
    property int durationMs: recorder.duration
    readonly property bool recording: recorder.recorderState === MediaRecorder.RecordingState
    readonly property bool previewing: !recording && path !== "" && durationMs > 0
    property string lastError: ""

    CaptureSession {
        id: session
        audioInput: AudioInput {}
        recorder: MediaRecorder {
            id: recorder
            quality: MediaRecorder.HighQuality
            onErrorOccurred: function(error, message) { root.lastError = String(message || "recording failed") }
        }
    }

    function start() {
        lastError = ""
        WhatsApp.voicePath(function(err, data) {
            if (err || !data) {
                root.lastError = err || "could not allocate a voice draft"
                return
            }
            root.path = data.path
            root.fileUrl = data.fileUrl
            recorder.outputLocation = data.fileUrl
            var fmt = recorder.mediaFormat
            fmt.fileFormat = MediaFormat.Ogg
            fmt.audioCodec = MediaFormat.AudioCodec.Opus
            recorder.mediaFormat = fmt
            recorder.record()
        })
    }

    function stop() {
        if (recording) recorder.stop()
    }

    function discard() {
        if (recording) recorder.stop()
        path = ""
        fileUrl = ""
        lastError = ""
    }

    function toggle() {
        if (recording) stop()
        else start()
    }
}
