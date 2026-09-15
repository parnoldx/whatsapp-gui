// Regression test for the daemon pipe framing.
//
// The helper answers one JSON line per request and pushes unsolicited results
// (media downloads, link previews, mark-read rollbacks) whenever they land. A
// push written in the same chunk as a job response used to be destroyed by
// m_stdout.clear() in kick(), so the GUI silently lost it — and a half-read
// line made the next answer fail to parse.
#include <QCoreApplication>
#include <QElapsedTimer>
#include <QEventLoop>
#include <QFileInfo>
#include <QQmlEngine>
#include <QTest>

#include "WhatsApp.hpp"

#ifndef STUB_HELPER
#define STUB_HELPER ""
#endif

class PipeTest : public QObject {
    Q_OBJECT
private slots:
    void pushTrailingAResponseIsDelivered();
};

void PipeTest::pushTrailingAResponseIsDelivered() {
    QVERIFY2(QFileInfo::exists(QStringLiteral(STUB_HELPER)), "tests/stub-helper.sh is missing");
    qputenv("WHATSAPP_HELPER", STUB_HELPER);

    QQmlEngine engine;
    WhatsApp client(&engine);
    // Queued behind the constructor's status job, so kick() runs with a
    // non-empty queue — the moment the buffer used to be cleared.
    client.refreshChats();
    QVERIFY(client.linkPreviews().isEmpty());

    const QString url = QStringLiteral("https://example.com");
    QElapsedTimer elapsed;
    elapsed.start();
    while (!client.linkPreviews().contains(url) && elapsed.elapsed() < 5000)
        QCoreApplication::processEvents(QEventLoop::AllEvents, 50);

    QVERIFY2(client.linkPreviews().contains(url),
             "the push line sent with the first response was dropped");
}

QTEST_MAIN(PipeTest)
#include "pipe_test.moc"
