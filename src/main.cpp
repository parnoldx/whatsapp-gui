#include <QGuiApplication>
#include <QFont>
#include <QLocalServer>
#include <QLocalSocket>
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QQuickStyle>
#include <QQuickWindow>
#include <QTimer>

#include <cstdio>

#include "OmarchyTheme.hpp"
#include "WhatsApp.hpp"

#include <QtWebEngineQuick/qtwebenginequickglobal.h>

static const char kSocketName[] = "whatsapp-gui";

class Shell : public QObject {
    Q_OBJECT
public:
    using QObject::QObject;
    Q_INVOKABLE void quit() { QCoreApplication::quit(); }
signals:
    void toggleRequested();
    void openChatRequested(const QString &jid);
};

static bool sendToRunning(const QString &line) {
    QLocalSocket sock;
    sock.connectToServer(QLatin1String(kSocketName));
    if (!sock.waitForConnected(250))
        return false;
    sock.write(line.toUtf8());
    sock.write("\n");
    sock.waitForBytesWritten(250);
    sock.waitForReadyRead(250);
    return true;
}

int main(int argc, char *argv[]) {
    QByteArray chromium = qgetenv("QTWEBENGINE_CHROMIUM_FLAGS");
    if (!chromium.contains("autoplay-policy")) {
        if (!chromium.isEmpty())
            chromium += ' ';
        chromium += QByteArrayLiteral("--autoplay-policy=no-user-gesture-required");
        qputenv("QTWEBENGINE_CHROMIUM_FLAGS", chromium);
    }

    QGuiApplication app(argc, argv);
    app.setApplicationName(QStringLiteral("WhatsApp"));
    app.setOrganizationName(QStringLiteral("pa"));
    app.setApplicationDisplayName(QStringLiteral("WhatsApp"));
    app.setDesktopFileName(QStringLiteral("whatsapp-gui"));
    app.setQuitOnLastWindowClosed(false);
    QQuickStyle::setStyle(QStringLiteral("Basic"));

    QFont base(QStringLiteral("JetBrainsMono Nerd Font"));
    base.setStyleHint(QFont::Monospace);
    base.setPixelSize(13);
    app.setFont(base);

    QString chatArg;
    bool toggle = false;
    const QStringList args = app.arguments();
    for (int i = 1; i < args.size(); ++i) {
        if (args[i] == QLatin1String("--toggle"))
            toggle = true;
        else if (args[i] == QLatin1String("--chat") && i + 1 < args.size())
            chatArg = args[++i];
        else if (args[i] == QLatin1String("--help")) {
            fputs("usage: whatsapp-gui [--toggle] [--chat JID]\n"
                  "  --toggle     hide the running window, or show and raise it\n"
                  "  --chat JID   open a chat in the running window\n", stdout);
            return 0;
        }
    }

    QString line = toggle ? QStringLiteral("toggle") : QStringLiteral("raise");
    if (!chatArg.isEmpty())
        line = QStringLiteral("open ") + chatArg;
    if (sendToRunning(line))
        return 0;

    QtWebEngineQuick::initialize();

    QLocalServer::removeServer(QLatin1String(kSocketName));
    QLocalServer server;
    server.listen(QLatin1String(kSocketName));

    OmarchyTheme theme;
    QQmlApplicationEngine engine;
    WhatsApp client(&engine);
    Shell shell;

    QObject::connect(&server, &QLocalServer::newConnection, &server, [&] {
        QLocalSocket *sock = server.nextPendingConnection();
        if (!sock)
            return;
        QObject::connect(sock, &QLocalSocket::readyRead, sock, [&shell, sock] {
            const QByteArray raw = sock->readAll();
            const QString cmd = QString::fromUtf8(raw).trimmed();
            if (cmd.startsWith(QLatin1String("open ")))
                emit shell.openChatRequested(cmd.mid(5).trimmed());
            else
                emit shell.toggleRequested();
            sock->write("ok\n");
            sock->disconnectFromServer();
        });
    });

    QQmlContext *ctx = engine.rootContext();
    ctx->setContextProperty(QStringLiteral("Theme"), &theme);
    ctx->setContextProperty(QStringLiteral("WhatsApp"), &client);
    ctx->setContextProperty(QStringLiteral("Shell"), &shell);

    QObject::connect(
        &engine, &QQmlApplicationEngine::objectCreationFailed, &app,
        [] { QCoreApplication::exit(-1); }, Qt::QueuedConnection);
    engine.load(QUrl(QStringLiteral("qrc:/qml/Main.qml")));
    if (engine.rootObjects().isEmpty())
        return -1;

    if (!chatArg.isEmpty())
        QTimer::singleShot(0, &client, [&client, chatArg] { client.selectChat(chatArg); });

    return app.exec();
}

#include "main.moc"
