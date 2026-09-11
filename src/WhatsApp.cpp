#include "WhatsApp.hpp"

#include <algorithm>
#include <utility>

#include <QCoreApplication>
#include <QDateTime>
#include <QDir>
#include <QFileInfo>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QQmlEngine>
#include <QStandardPaths>


WhatsApp::WhatsApp(QQmlEngine *engine, QObject *parent)
    : QObject(parent), m_engine(engine) {
    // Fast path: the helper runs once as a persistent daemon (one JSON argv array
    // per line in, one JSON response line out), saving a Python cold-start per
    // call. If that helper is too old to speak --daemon (or keeps crashing), we
    // fall back to spawning it once per call, same as before.
    connect(&m_proc, &QProcess::readyReadStandardOutput, this, [this] {
        m_stdout += m_proc.readAllStandardOutput();
        int nl;
        while ((nl = m_stdout.indexOf('\n')) >= 0) {
            const QByteArray line = m_stdout.left(nl);
            m_stdout.remove(0, nl + 1);
            if (!line.trimmed().isEmpty())
                handleResponse(line);
        }
    });
    connect(&m_proc, &QProcess::readyReadStandardError, this, [this] {
        m_stderr += m_proc.readAllStandardError();
        if (m_stderr.size() > 65536)
            m_stderr = m_stderr.right(4096);
    });
    connect(&m_proc, &QProcess::started, this, &WhatsApp::kick);
    connect(&m_proc, &QProcess::finished, this, &WhatsApp::onDaemonExit);
    connect(&m_proc, &QProcess::errorOccurred, this, [this](QProcess::ProcessError e) {
        if (e == QProcess::FailedToStart)
            onDaemonExit(-1, QProcess::CrashExit);  // no finished() on FailedToStart
    });
    startDaemon();

    m_debounce.setSingleShot(true);
    m_debounce.setInterval(100);  // coalesce sync-write bursts; not felt as latency
    connect(&m_debounce, &QTimer::timeout, this, &WhatsApp::refreshChats);

    // Sends and reactions are persisted by the helper before it answers, so
    // the reload right below already sees the row — no catch-up poll needed.

    // Integrity check, not an update channel: updates arrive via inotify and
    // fileChanged re-arms lost watches. This only catches a watch that died
    // without firing (Qt bug territory) and stale sync-daemon status.
    m_fallback.setInterval(60000);
    connect(&m_fallback, &QTimer::timeout, this, [this] {
        startWatch();
        refreshChats();
    });
    m_fallback.start();

    connect(&m_watcher, &QFileSystemWatcher::directoryChanged, this, [this](const QString &) {
        m_debounce.start();
    });
    connect(&m_watcher, &QFileSystemWatcher::fileChanged, this, [this](const QString &path) {
        m_debounce.start();
        // A SQLite checkpoint truncates/replaces wacli.db-wal, which drops the
        // watch; re-arm it so the next write still wakes us.
        if (!m_watcher.files().contains(path) && QFileInfo::exists(path))
            m_watcher.addPath(path);
    });

    refreshStatus();
    // Refresh cached profile pictures at most once a day; helper no-ops when fresh.
    call({QStringLiteral("refresh-avatars")}, QStringLiteral("avatar"));
    updateActivity();
}

QString WhatsApp::helperPath() {
    const QString env = qEnvironmentVariable("WHATSAPP_HELPER");
    if (!env.isEmpty())
        return env;
    const QString installed = QDir::homePath() + "/.local/lib/whatsapp-gui/whatsapp";
    if (QFileInfo::exists(installed))
        return installed;
    const QString nextToBin = QCoreApplication::applicationDirPath() + "/../lib/whatsapp-gui/whatsapp";
    if (QFileInfo::exists(nextToBin))
        return QFileInfo(nextToBin).canonicalFilePath();
    return QCoreApplication::applicationDirPath() + "/whatsapp";
}

QString WhatsApp::omarchyShellPath() {
    const QString found = QStandardPaths::findExecutable(QStringLiteral("omarchy-shell"));
    if (!found.isEmpty())
        return found;
    const QString fallback = QStringLiteral("/usr/share/omarchy/bin/omarchy-shell");
    if (QFileInfo::exists(fallback))
        return fallback;
    return {};
}

void WhatsApp::finishJs(QJSValue done, const QString &error, const QVariantMap &data) {
    if (!done.isCallable())
        return;
    QJSValueList args;
    args << QJSValue(error)
         << (error.isEmpty() && m_engine ? m_engine->toScriptValue(QVariant(data))
                                         : QJSValue(QJSValue::NullValue));
    done.call(args);
}

bool WhatsApp::busySyncError(const QString &error) {
    const QString low = error.toLower();
    return low.contains(QLatin1String("busy syncing"))
        || low.contains(QLatin1String("store is locked"))
        || low.contains(QLatin1String("another wacli"));
}

QString WhatsApp::jobArg(const Job &job, const QString &flag) {
    const int idx = job.args.indexOf(flag);
    if (idx >= 0 && idx + 1 < job.args.size())
        return job.args.at(idx + 1);
    return {};
}

bool WhatsApp::loadingMessages() const {
    return !m_selectedJid.isEmpty() && m_messagesJid != m_selectedJid;
}

void WhatsApp::updateActivity() {
    QString next;
    if (m_sending)
        next = QStringLiteral("Sending…");
    else if (m_busy && m_current.kind == QLatin1String("downloaded"))
        next = QStringLiteral("Downloading…");
    else if (loadingMessages() || (m_busy && m_current.kind == QLatin1String("messages")))
        next = QStringLiteral("Loading messages…");
    else if (m_refreshing || (m_busy && m_current.kind == QLatin1String("chats")))
        next = QStringLiteral("Loading chats…");
    else if (m_busy && m_current.kind == QLatin1String("status"))
        next = QStringLiteral("Checking connection…");
    else if (m_syncActive)
        next = QStringLiteral("Syncing");
    else if (!m_authenticated)
        next = QStringLiteral("Not signed in");
    else if (!m_connected)
        next = QStringLiteral("Offline");
    else
        next = QStringLiteral("Connected");
    if (next == m_activity)
        return;
    m_activity = next;
    emit activityChanged();
}

void WhatsApp::call(const QStringList &args, const QString &kind, QJSValue done) {
    Job job{args, done, kind};
    const bool jump = kind == QLatin1String("messages") || kind == QLatin1String("chats")
                      || kind == QLatin1String("status") || kind == QLatin1String("downloaded");
    if (jump) {
        // Coalesce identical refreshes/downloads. During an active sync the DB
        // watcher front-inserts refresh jobs faster than the queue drains; without
        // this, an already-queued download at the tail never runs.
        for (const Job &queued : std::as_const(m_queue))
            if (queued.kind == kind && queued.args == args)
                return;
        m_queue.insert(0, job);
    } else {
        m_queue.enqueue(job);
    }
    kick();
}

void WhatsApp::startDaemon() {
    if (!m_useDaemon || m_proc.state() != QProcess::NotRunning)
        return;
    m_stdout.clear();
    m_oneShotPending = false;
    m_daemonUptime.start();
    m_proc.start(helperPath(), {QStringLiteral("--daemon")});
}

void WhatsApp::onDaemonExit(int code, QProcess::ExitStatus) {
    const bool wasOneShot = m_oneShotPending;
    m_oneShotPending = false;

    if (m_busy) {
        // No response line arrived for the in-flight job.
        Job job = m_current;
        m_current = Job{};
        m_busy = false;
        if (job.kind == QLatin1String("sent") && m_sending) {
            m_sending = false;
            emit sendingChanged();
        }
        const QString err = QStringLiteral("helper stopped unexpectedly");
        if (!wasOneShot && m_lastError != err) {
            m_lastError = err;
            emit lastErrorChanged();
        }
        if (job.done.isCallable()) {
            QJSValueList args;
            args << QJSValue(err) << QJSValue(QJSValue::NullValue);
            job.done.call(args);
        }
        updateActivity();
    }
    m_stdout.clear();
    m_stderr.clear();

    if (wasOneShot) {
        // Guard against a FailedToStart spin: if it died instantly, pace retries.
        const bool instant = m_daemonUptime.isValid() && m_daemonUptime.elapsed() < 200;
        QTimer::singleShot(instant ? 500 : 0, this, [this] { kick(); });
        return;
    }
    // Daemon died. If it never worked and dies fast, the helper predates
    // --daemon: stop retrying and spawn per call instead.
    const bool quick = m_daemonUptime.isValid() && m_daemonUptime.elapsed() < 3000;
    if (quick && !m_daemonEverResponded && ++m_daemonFails >= 2) {
        m_useDaemon = false;
        kick();
        return;
    }
    if (!quick)
        m_daemonFails = 0;
    QTimer::singleShot(quick ? 600 : 0, this, [this] {
        startDaemon();
        kick();
    });
    Q_UNUSED(code);
}

void WhatsApp::kick() {
    if (m_busy || m_queue.isEmpty())
        return;
    if (m_useDaemon && m_proc.state() != QProcess::Running) {
        startDaemon();  // resumes from the started() signal
        return;
    }
    m_busy = true;
    m_current = m_queue.dequeue();
    m_stdout.clear();
    m_stderr.clear();
    updateActivity();
    if (m_useDaemon) {
        QJsonArray arr;
        for (const QString &a : m_current.args)
            arr.append(a);
        m_proc.write(QJsonDocument(arr).toJson(QJsonDocument::Compact));
        m_proc.write("\n");
        return;
    }
    // Fallback: one helper process per call.
    m_oneShotPending = true;
    m_daemonUptime.start();
    m_proc.start(helperPath(), m_current.args);
}

void WhatsApp::handleResponse(const QByteArray &line) {
    if (!m_busy)
        return;
    m_daemonEverResponded = true;
    Job job = m_current;
    m_current = Job{};
    m_busy = false;

    QJsonParseError err;
    const QJsonDocument doc = QJsonDocument::fromJson(line, &err);
    QString error;
    QVariantMap data;
    bool ok = false;
    if (err.error == QJsonParseError::NoError && doc.isObject()) {
        const QJsonObject obj = doc.object();
        ok = obj.value(QStringLiteral("ok")).toBool();
        if (ok)
            data = obj.value(QStringLiteral("data")).toVariant().toMap();
        else
            error = obj.value(QStringLiteral("error")).toString();
    } else {
        error = QStringLiteral("helper returned unreadable output");
    }
    if (error.isEmpty() && !ok)
        error = QStringLiteral("request failed");

    const bool lockWait = !ok && busySyncError(error);
    const bool quietFail = !ok && (job.kind == QLatin1String("mark-read")
                                   || job.kind == QLatin1String("link-preview")
                                   || job.kind == QLatin1String("avatar")
                                   || (lockWait && job.kind != QLatin1String("sent")));
    if (!quietFail && m_lastError != error) {
        m_lastError = error;
        emit lastErrorChanged();
    } else if (quietFail && lockWait && busySyncError(m_lastError) && !m_lastError.isEmpty()) {
        m_lastError.clear();
        emit lastErrorChanged();
    }
    if (!ok && job.kind == QLatin1String("link-preview")) {
        const QString url = job.args.size() >= 2 ? job.args.last() : QString();
        m_previewPending.remove(url);
    }
    if (!ok && job.kind == QLatin1String("avatar")) {
        const QString jid = job.args.size() >= 2 ? job.args.last() : QString();
        m_avatarPending.remove(jid);
    }
    if (!ok && job.kind == QLatin1String("messages")) {
        const QString requested = jobArg(job, QStringLiteral("--chat"));
        if (m_pendingMessagesJid == requested)
            m_pendingMessagesJid.clear();
        if (!requested.isEmpty() && requested == m_selectedJid && m_messagesJid != m_selectedJid) {
            m_messagesJid = m_selectedJid;
            emit loadingMessagesChanged();
        }
    }
    if (!ok && job.kind == QLatin1String("sent") && m_sending) {
        m_sending = false;
        emit sendingChanged();
    }
    if (ok && !job.kind.isEmpty())
        apply(job, data);

    if (job.done.isCallable()) {
        QJSValueList args;
        args << QJSValue(quietFail ? QString() : error)
             << (ok && m_engine ? m_engine->toScriptValue(QVariant(data)) : QJSValue(QJSValue::NullValue));
        job.done.call(args);
    }
    updateActivity();
    // In fallback mode the next job waits for this process's finished() signal
    // (via onDaemonExit) so we never start() an still-running QProcess.
    if (m_useDaemon)
        kick();
}

void WhatsApp::apply(const Job &job, const QVariantMap &data) {
    const QString &kind = job.kind;
    if (kind == QLatin1String("status")) {
        m_authenticated = data.value(QStringLiteral("authenticated")).toBool();
        m_connected = data.value(QStringLiteral("connected")).toBool();
        m_syncActive = data.value(QStringLiteral("syncActive")).toBool();
        m_receipts = data.value(QStringLiteral("receipts")).toBool();
        m_acks = data.value(QStringLiteral("acks")).toMap();
        m_storeDir = data.value(QStringLiteral("storeDir")).toString();
        m_unreadBadge = data.value(QStringLiteral("unreadBadge")).toInt();
        emit acksChanged();
        emit unreadBadgeChanged();
        emit statusChanged();
        startWatch();
        updateActivity();
        if (m_chats.isEmpty())
            refreshChats();
        return;
    }
    if (kind == QLatin1String("chats")) {
        const qint64 previousTs = m_selectedChat.value(QStringLiteral("lastMessageTs")).toLongLong();
        const QString previousPreview = m_selectedChat.value(QStringLiteral("preview")).toString();
        if (data.contains(QStringLiteral("syncActive"))) {
            const bool sync = data.value(QStringLiteral("syncActive")).toBool();
            if (sync != m_syncActive) {
                m_syncActive = sync;
                emit statusChanged();
            }
        }
        QVariantList next = data.value(QStringLiteral("chats")).toList();
        m_chats.swap(next);
        overlayAvatars();
        if (qEnvironmentVariableIsSet("WHATSAPP_DEBUG"))
            fprintf(stderr, "whatsapp-gui chats=%d\n", int(m_chats.size()));
        m_unreadBadge = badgeCount(m_chats, m_acks);
        const bool chatsSame = chatFingerprint(m_chats) == chatFingerprint(next);
        const bool wasRefreshing = m_refreshing;
        m_refreshing = false;
        if (!chatsSame)
            emit chatsChanged();
        emit unreadBadgeChanged();
        if (wasRefreshing)
            emit refreshingChanged();
        updateSelectedChat();
        updateActivity();
        if (!m_selectedJid.isEmpty()) {
            const qint64 nextTs = m_selectedChat.value(QStringLiteral("lastMessageTs")).toLongLong();
            const QString nextPreview = m_selectedChat.value(QStringLiteral("preview")).toString();
            const bool alreadyThisChat = m_messagesJid == m_selectedJid && !m_messages.isEmpty();
            const bool loadingThisChat = m_pendingMessagesJid == m_selectedJid;
            const bool threadChanged = nextTs > previousTs || nextPreview != previousPreview;
            if (!alreadyThisChat && !loadingThisChat)
                loadMessages(m_selectedJid);
            else if (alreadyThisChat && threadChanged)
                loadMessages(m_selectedJid);
            if (nextTs > previousTs)
                ackChat(m_selectedJid);
        }
        return;
    }
    if (kind == QLatin1String("messages")) {
        const QString requested = jobArg(job, QStringLiteral("--chat"));
        if (!requested.isEmpty() && requested != m_selectedJid) {
            if (m_pendingMessagesJid == requested)
                m_pendingMessagesJid.clear();
            return;
        }
        const QVariantList list = data.value(QStringLiteral("messages")).toList();
        QString chatJid = requested;
        if (chatJid.isEmpty() && !list.isEmpty())
            chatJid = list.first().toMap().value(QStringLiteral("chatJid")).toString();
        if (!m_selectedJid.isEmpty() && !chatJid.isEmpty() && chatJid != m_selectedJid)
            return;
        const bool wasLoading = loadingMessages();
        const QString oldStamp = messageStamp(m_messages);
        m_messages = list;
        m_messagesJid = m_selectedJid;
        if (!m_selectedJid.isEmpty()) {
            if (m_msgCache.size() > 24)
                m_msgCache.clear();
            m_msgCache.insert(m_selectedJid, m_messages);
        }
        if (m_pendingMessagesJid == m_selectedJid)
            m_pendingMessagesJid.clear();
        if (wasLoading != loadingMessages())
            emit loadingMessagesChanged();
        updateActivity();
        bool previewSeed = false;
        for (int i = 0; i < m_messages.size(); ++i) {
            const QVariantMap preview = m_messages[i].toMap().value(QStringLiteral("linkPreview")).toMap();
            const QString url = preview.value(QStringLiteral("url")).toString();
            if (url.isEmpty() || m_previewCache.contains(url))
                continue;
            if (preview.value(QStringLiteral("fetched")).toBool()
                || !preview.value(QStringLiteral("imageUrl")).toString().isEmpty()) {
                m_previewCache.insert(url, preview);
                previewSeed = true;
            }
        }
        if (previewSeed)
            emit linkPreviewsChanged();
        if (oldStamp != messageStamp(m_messages))
            emit messagesChanged();
        const qint64 downloadCutoff = QDateTime::currentSecsSinceEpoch() - 7 * 86400;
        auto considerDownload = [&](const QVariantMap &msg) {
            const QString msgId = msg.value(QStringLiteral("id")).toString();
            if (msg.value(QStringLiteral("kind")).toString() != QLatin1String("image"))
                return;
            if (msg.value(QStringLiteral("downloaded")).toBool()
                || msg.value(QStringLiteral("unavailable")).toBool()
                || msg.value(QStringLiteral("ts")).toLongLong() <= downloadCutoff
                || msgId.isEmpty() || m_autoDownloaded.contains(msgId))
                return;
            m_autoDownloaded.insert(msgId);
            download(msg);
        };
        for (const QVariant &item : m_messages) {
            const QVariantMap msg = item.toMap();
            considerDownload(msg);
            for (const QVariant &piece : msg.value(QStringLiteral("album")).toList())
                considerDownload(piece.toMap());
        }
        if (!m_selectedJid.isEmpty())
            ackChat(m_selectedJid);
        return;
    }
    if (kind == QLatin1String("participants")) {
        m_participants = data.value(QStringLiteral("participants")).toList();
        emit participantsChanged();
        return;
    }
    if (kind == QLatin1String("ack") || kind == QLatin1String("mark-read")) {
        if (!data.contains(QStringLiteral("acks")))
            return;
        m_acks = data.value(QStringLiteral("acks")).toMap();
        m_unreadBadge = badgeCount(m_chats, m_acks);
        emit acksChanged();
        emit unreadBadgeChanged();
        return;
    }
    if (kind == QLatin1String("receipts")) {
        m_receipts = data.value(QStringLiteral("receipts")).toBool();
        emit statusChanged();
        return;
    }
    if (kind == QLatin1String("sync")) {
        m_syncActive = data.value(QStringLiteral("active")).toBool();
        emit statusChanged();
        updateActivity();
        return;
    }
    if (kind == QLatin1String("sent")) {
        m_sending = false;
        emit sendingChanged();
        updateActivity();
        if (!m_selectedJid.isEmpty())
            loadMessages(m_selectedJid);
        refreshChats();
        return;
    }
    if (kind == QLatin1String("downloaded")) {
        patchDownloaded(data);
        return;
    }
    if (kind == QLatin1String("reacted")) {
        // Reaction rows persist before the helper answers, same as sends.
        if (!m_selectedJid.isEmpty())
            loadMessages(m_selectedJid);
        refreshChats();
        return;
    }
    if (kind == QLatin1String("link-preview")) {
        patchLinkPreview(data);
        return;
    }
    if (kind == QLatin1String("avatar")) {
        patchAvatar(data);
        return;
    }
}

QVariant WhatsApp::selectedChat() const {
    return m_selectedChat.isEmpty() ? QVariant() : QVariant(m_selectedChat);
}

QString WhatsApp::chatFingerprint(const QVariantList &chats) {
    QString out;
    for (const QVariant &item : chats) {
        const QVariantMap chat = item.toMap();
        out += chat.value(QStringLiteral("jid")).toString();
        out += QLatin1Char('\n');
        out += chat.value(QStringLiteral("name")).toString();
        out += QLatin1Char('\n');
        out += chat.value(QStringLiteral("preview")).toString();
        out += QLatin1Char('\n');
        out += QString::number(chat.value(QStringLiteral("lastMessageTs")).toLongLong());
        out += QLatin1Char('\n');
        out += QString::number(chat.value(QStringLiteral("unreadCount")).toInt());
        out += QLatin1Char('\n');
    }
    return out;
}

QString WhatsApp::messageStamp(const QVariantList &messages) {
    QString out;
    for (const QVariant &item : messages) {
        const QVariantMap m = item.toMap();
        out += m.value(QStringLiteral("id")).toString();
        out += QLatin1Char('|');
        out += m.value(QStringLiteral("downloaded")).toBool() ? QLatin1Char('d') : QLatin1Char('-');
        out += QLatin1Char('|');
        out += m.value(QStringLiteral("myReaction")).toString();
        out += QLatin1Char('|');
        out += QString::number(m.value(QStringLiteral("reactions")).toList().size());
        out += QLatin1Char('|');
        for (const QVariant &piece : m.value(QStringLiteral("album")).toList())
            out += piece.toMap().value(QStringLiteral("downloaded")).toBool() ? QLatin1Char('d') : QLatin1Char('-');
        out += QLatin1Char('\n');
    }
    return out;
}

int WhatsApp::badgeCount(const QVariantList &chats, const QVariantMap &acks) {
    int total = 0;
    for (const QVariant &item : chats) {
        const QVariantMap chat = item.toMap();
        if (chat.value(QStringLiteral("muted")).toBool() || chat.value(QStringLiteral("archived")).toBool())
            continue;
        const qint64 last = chat.value(QStringLiteral("lastMessageTs")).toLongLong();
        const qint64 ack = acks.value(chat.value(QStringLiteral("jid")).toString()).toLongLong();
        if (last > ack && chat.value(QStringLiteral("unreadCount")).toInt() > 0)
            ++total;
    }
    return total;
}

void WhatsApp::updateSelectedChat() {
    QVariantMap found;
    for (const QVariant &item : m_chats) {
        const QVariantMap chat = item.toMap();
        if (chat.value(QStringLiteral("jid")).toString() == m_selectedJid) {
            found = chat;
            break;
        }
    }
    if (found != m_selectedChat) {
        m_selectedChat = found;
        emit selectedChatChanged();
    }
}

void WhatsApp::setSelectedJid(const QString &jid) {
    if (jid == m_selectedJid)
        return;
    if (jid.isEmpty()) {
        const bool wasLoading = loadingMessages();
        m_selectedJid.clear();
        m_messages.clear();
        m_messagesJid.clear();
        m_pendingMessagesJid.clear();
        m_participants.clear();
        m_participantsJid.clear();
        emit selectedJidChanged();
        emit messagesChanged();
        emit participantsChanged();
        if (wasLoading)
            emit loadingMessagesChanged();
        updateSelectedChat();
        updateActivity();
        return;
    }
    selectChat(jid);
}

void WhatsApp::refreshStatus() {
    call({QStringLiteral("status")}, QStringLiteral("status"));
}

void WhatsApp::refreshChats() {
    if (!m_refreshing) {
        m_refreshing = true;
        emit refreshingChanged();
        updateActivity();
    }
    call({QStringLiteral("chats"), QStringLiteral("--limit"), QStringLiteral("120")}, QStringLiteral("chats"));
}

void WhatsApp::loadMessages(const QString &jid) {
    if (jid.isEmpty()) {
        m_messages.clear();
        m_messagesJid.clear();
        m_pendingMessagesJid.clear();
        emit messagesChanged();
        return;
    }
    if (m_pendingMessagesJid == jid)
        return;
    m_pendingMessagesJid = jid;
    call({QStringLiteral("messages"), QStringLiteral("--chat"), jid, QStringLiteral("--limit"), QStringLiteral("80")},
         QStringLiteral("messages"));
}

void WhatsApp::loadParticipants(const QString &jid) {
    if (jid.isEmpty()) {
        m_participantsJid.clear();
        if (!m_participants.isEmpty()) {
            m_participants.clear();
            emit participantsChanged();
        }
        return;
    }
    if (m_participantsJid == jid)
        return;
    m_participantsJid = jid;
    call({QStringLiteral("participants"), QStringLiteral("--chat"), jid}, QStringLiteral("participants"));
}

void WhatsApp::selectChat(const QString &jid) {
    if (jid.isEmpty())
        return;
    if (m_selectedJid != jid) {
        const bool wasLoading = loadingMessages();
        m_selectedJid = jid;
        if (m_messagesJid != jid) {
            if (m_msgCache.contains(jid)) {
                m_messages = m_msgCache.value(jid);  // paint the last thread now
                m_messagesJid = jid;                 // a fresh copy loads below
            } else {
                m_messages.clear();
                m_messagesJid.clear();
            }
            emit messagesChanged();
        }
        emit selectedJidChanged();
        updateSelectedChat();
        if (wasLoading != loadingMessages())
            emit loadingMessagesChanged();
        updateActivity();
    }
    loadMessages(jid);
    // Only groups have a participant list; a DM query just round-trips an empty one.
    if (m_selectedChat.value(QStringLiteral("isGroup")).toBool() || jid.endsWith(QLatin1String("@g.us")))
        loadParticipants(jid);
    else
        loadParticipants(QString());
    ackChat(jid);
}

void WhatsApp::ackChat(const QString &jid) {
    QVariantMap chat;
    for (const QVariant &item : m_chats) {
        const QVariantMap c = item.toMap();
        if (c.value(QStringLiteral("jid")).toString() == jid) {
            chat = c;
            break;
        }
    }
    if (chat.isEmpty())
        return;
    qint64 ts = chat.value(QStringLiteral("lastMessageTs")).toLongLong();
    for (const QVariant &item : m_messages) {
        const QVariantMap msg = item.toMap();
        if (msg.value(QStringLiteral("chatJid")).toString() == jid)
            ts = std::max(ts, msg.value(QStringLiteral("ts")).toLongLong());
    }
    if (ts <= m_acks.value(jid).toLongLong())
        return;
    m_acks.insert(jid, ts);
    emit acksChanged();
    m_unreadBadge = badgeCount(m_chats, m_acks);
    emit unreadBadgeChanged();
    // One call: mark-read persists the ack locally, then tells wacli.
    call({QStringLiteral("mark-read"), QStringLiteral("--chat"), jid,
          QStringLiteral("--ts"), QString::number(ts)},
         QStringLiteral("mark-read"));
}

void WhatsApp::patchDownloaded(const QVariantMap &data) {
    const QString id = data.value(QStringLiteral("id")).toString();
    if (id.isEmpty())
        return;
    auto apply = [&](QVariantMap &msg) {
        msg.insert(QStringLiteral("localPath"), data.value(QStringLiteral("localPath")));
        msg.insert(QStringLiteral("fileUrl"), data.value(QStringLiteral("fileUrl")));
        msg.insert(QStringLiteral("thumbUrl"), data.value(QStringLiteral("thumbUrl")));
        msg.insert(QStringLiteral("downloaded"), true);
        const QString filename = data.value(QStringLiteral("filename")).toString();
        if (!filename.isEmpty())
            msg.insert(QStringLiteral("filename"), filename);
    };
    for (int i = 0; i < m_messages.size(); ++i) {
        QVariantMap msg = m_messages[i].toMap();
        bool hit = msg.value(QStringLiteral("id")).toString() == id;
        if (hit)
            apply(msg);
        QVariantList album = msg.value(QStringLiteral("album")).toList();
        bool albumHit = false;
        for (int j = 0; j < album.size(); ++j) {
            QVariantMap piece = album[j].toMap();
            if (piece.value(QStringLiteral("id")).toString() != id)
                continue;
            apply(piece);
            album[j] = piece;
            albumHit = true;
        }
        if (!hit && !albumHit)
            continue;
        if (albumHit) {
            msg.insert(QStringLiteral("album"), album);
            bool all = true;
            for (const QVariant &piece : album)
                all = all && piece.toMap().value(QStringLiteral("downloaded")).toBool();
            if (all)
                msg.insert(QStringLiteral("downloaded"), true);
        }
        m_messages[i] = msg;
        emit messagesChanged();
        return;
    }
}

void WhatsApp::patchLinkPreview(const QVariantMap &data) {
    const QString url = data.value(QStringLiteral("url")).toString();
    m_previewPending.remove(url);
    if (url.isEmpty())
        return;
    if (m_previewCache.value(url).toMap() == data)
        return;
    m_previewCache.insert(url, data);
    emit linkPreviewsChanged();
}

void WhatsApp::fetchLinkPreview(const QString &url) {
    if (url.isEmpty() || m_previewPending.contains(url))
        return;
    const QString cachedEmbed = m_previewCache.value(url).toMap().value(QStringLiteral("embedUrl")).toString();
    const bool usable = !cachedEmbed.isEmpty()
        && !cachedEmbed.contains(QLatin1String("share"))
        && !cachedEmbed.contains(QLatin1String("embed/v3"));
    if (m_previewCache.contains(url) && usable)
        return;
    m_previewPending.insert(url);
    call({QStringLiteral("link-preview"), QStringLiteral("--url"), url}, QStringLiteral("link-preview"));
}

void WhatsApp::overlayAvatars() {
    for (int i = 0; i < m_chats.size(); ++i) {
        QVariantMap chat = m_chats[i].toMap();
        const QString jid = chat.value(QStringLiteral("jid")).toString();
        if (jid.isEmpty())
            continue;
        if (chat.value(QStringLiteral("avatarUrl")).toString().isEmpty() && m_avatarCache.contains(jid)) {
            chat.insert(QStringLiteral("avatarUrl"), m_avatarCache.value(jid));
            m_chats[i] = chat;
        }
    }
}

void WhatsApp::patchAvatar(const QVariantMap &data) {
    const QString jid = data.value(QStringLiteral("jid")).toString();
    const QString url = data.value(QStringLiteral("fileUrl")).toString();
    m_avatarPending.remove(jid);
    if (jid.isEmpty() || url.isEmpty())
        return;
    if (m_avatarCache.value(jid).toString() == url)
        return;
    m_avatarCache.insert(jid, url);
    emit avatarsChanged();
}

void WhatsApp::fetchAvatar(const QString &jid) {
    if (jid.isEmpty() || m_avatarCache.contains(jid) || m_avatarPending.contains(jid))
        return;
    m_avatarPending.insert(jid);
    call({QStringLiteral("avatar"), QStringLiteral("--jid"), jid}, QStringLiteral("avatar"));
}

void WhatsApp::markRead() {
    if (m_selectedJid.isEmpty())
        return;
    call({QStringLiteral("mark-read"), QStringLiteral("--chat"), m_selectedJid}, QStringLiteral("ack"));
    refreshChats();
}

// ponytail: one app-state round trip per unread chat; batch it if the list ever gets long.
void WhatsApp::markAllRead() {
    for (const QVariant &item : m_chats) {
        const QVariantMap chat = item.toMap();
        if (chat.value(QStringLiteral("unreadCount")).toInt() <= 0)
            continue;
        ackChat(chat.value(QStringLiteral("jid")).toString());
    }
}

void WhatsApp::setReceipts(bool on) {
    call({QStringLiteral("receipts"), on ? QStringLiteral("--on") : QStringLiteral("--off")},
         QStringLiteral("receipts"));
}

void WhatsApp::setOnline(bool on) {
    call({QStringLiteral("sync"), on ? QStringLiteral("start") : QStringLiteral("stop")},
         QStringLiteral("sync"));
}

void WhatsApp::sendText(const QString &text, const QVariantList &mentions, const QString &replyId) {
    if (m_selectedJid.isEmpty())
        return;
    QStringList args{QStringLiteral("send-text"), QStringLiteral("--chat"), m_selectedJid,
                     QStringLiteral("--message"), text};
    if (!replyId.isEmpty())
        args << QStringLiteral("--reply-to") << replyId;
    for (const QVariant &m : mentions)
        args << QStringLiteral("--mention") << m.toString();
    m_sending = true;
    emit sendingChanged();
    updateActivity();
    call(args, QStringLiteral("sent"));
}

void WhatsApp::sendFile(const QString &path, const QString &caption, const QString &replyId) {
    if (m_selectedJid.isEmpty())
        return;
    QStringList args{QStringLiteral("send-file"), QStringLiteral("--chat"), m_selectedJid,
                     QStringLiteral("--file"), path};
    if (!caption.isEmpty())
        args << QStringLiteral("--caption") << caption;
    if (!replyId.isEmpty())
        args << QStringLiteral("--reply-to") << replyId;
    m_sending = true;
    emit sendingChanged();
    updateActivity();
    call(args, QStringLiteral("sent"));
}

void WhatsApp::sendVoice(const QString &path, const QString &replyId) {
    if (m_selectedJid.isEmpty())
        return;
    QStringList args{QStringLiteral("send-voice"), QStringLiteral("--chat"), m_selectedJid,
                     QStringLiteral("--file"), path};
    if (!replyId.isEmpty())
        args << QStringLiteral("--reply-to") << replyId;
    m_sending = true;
    emit sendingChanged();
    updateActivity();
    call(args, QStringLiteral("sent"));
}

void WhatsApp::download(const QVariantMap &message) {
    if (m_selectedJid.isEmpty() || message.value(QStringLiteral("id")).toString().isEmpty())
        return;
    call({QStringLiteral("download"), QStringLiteral("--chat"), m_selectedJid, QStringLiteral("--id"),
          message.value(QStringLiteral("id")).toString()},
         QStringLiteral("downloaded"));
}

void WhatsApp::react(const QVariantMap &message, const QString &emoji) {
    const QString id = message.value(QStringLiteral("id")).toString();
    if (m_selectedJid.isEmpty() || id.isEmpty())
        return;
    QStringList args{QStringLiteral("react"), QStringLiteral("--chat"), m_selectedJid,
                     QStringLiteral("--id"), id, QStringLiteral("--emoji"), emoji};
    const QString sender = message.value(QStringLiteral("senderJid")).toString();
    if (!sender.isEmpty())
        args << QStringLiteral("--sender") << sender;
    call(args, QStringLiteral("reacted"));
}

void WhatsApp::pickFiles(QJSValue done) {
    call({QStringLiteral("pick-files")}, QString(), done);
}

void WhatsApp::openEmojiPicker() {
    const QString shell = omarchyShellPath();
    if (shell.isEmpty())
        return;
    QProcess::startDetached(shell, {QStringLiteral("shell"), QStringLiteral("summon"),
                                    QStringLiteral("omarchy.emojis"), QStringLiteral("{}")});
}

void WhatsApp::pickEmoji(QJSValue done) {
    // Overlay is already opening via openEmojiPicker(); this process only
    // watches the clipboard, off the helper daemon so a chat load can't stall it.
    auto *p = new QProcess(this);
    connect(p, &QProcess::finished, this, [this, p, done]() mutable {
        const QByteArray out = p->readAllStandardOutput().trimmed();
        p->deleteLater();
        QJsonParseError err;
        const QJsonDocument doc = QJsonDocument::fromJson(out, &err);
        if (err.error != QJsonParseError::NoError || !doc.isObject()) {
            finishJs(done, QStringLiteral("helper returned unreadable output"), {});
            return;
        }
        const QJsonObject obj = doc.object();
        if (!obj.value(QStringLiteral("ok")).toBool()) {
            finishJs(done, obj.value(QStringLiteral("error")).toString(), {});
            return;
        }
        finishJs(done, QString(), obj.value(QStringLiteral("data")).toVariant().toMap());
    });
    connect(p, &QProcess::errorOccurred, this, [this, p, done](QProcess::ProcessError e) mutable {
        if (e != QProcess::FailedToStart)
            return;
        p->deleteLater();
        finishJs(done, QStringLiteral("could not open emoji menu"), {});
    });
    p->start(helperPath(), {QStringLiteral("pick-emoji"),
             QStringLiteral("--watch-only")});
}

void WhatsApp::clipboard(QJSValue done) {
    call({QStringLiteral("clipboard")}, QString(), done);
}

void WhatsApp::openFile(const QString &path) {
    if (path.isEmpty())
        return;
    call({QStringLiteral("open-file"), QStringLiteral("--file"), path});
}

void WhatsApp::voicePath(QJSValue done) {
    call({QStringLiteral("voice-path")}, QString(), done);
}

QVariantMap WhatsApp::draftFor(const QString &jid) const {
    return m_drafts.value(jid).toMap();
}

void WhatsApp::saveDraft(const QString &jid, const QVariantMap &draft) {
    if (jid.isEmpty())
        return;
    m_drafts.insert(jid, draft);
}

void WhatsApp::startWatch() {
    if (m_storeDir.isEmpty())
        return;
    // Watch the DB files, not just the dir: WAL-mode writes append to
    // wacli.db-wal in place and often don't touch the directory entry.
    const QStringList targets{
        m_storeDir,
        m_storeDir + QStringLiteral("/wacli.db"),
        m_storeDir + QStringLiteral("/wacli.db-wal"),
    };
    const QStringList watched = m_watcher.directories() + m_watcher.files();
    for (const QString &t : targets)
        if (!watched.contains(t) && QFileInfo::exists(t))
            m_watcher.addPath(t);
}
