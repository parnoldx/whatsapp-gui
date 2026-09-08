#pragma once

#include <QObject>
#include <QByteArray>
#include <QElapsedTimer>
#include <QHash>
#include <QProcess>
#include <QString>
#include <QStringList>
#include <QVariant>
#include <QVariantList>
#include <QVariantMap>
#include <QJSValue>
#include <QFileSystemWatcher>
#include <QTimer>
#include <QQueue>
#include <QSet>

class QQmlEngine;

// Talks to the Python helper. Reads are SQLite; writes go through wacli.
class WhatsApp : public QObject {
    Q_OBJECT
    Q_PROPERTY(QVariantList chats READ chats NOTIFY chatsChanged)
    Q_PROPERTY(QVariantList messages READ messages NOTIFY messagesChanged)
    Q_PROPERTY(QVariantList participants READ participants NOTIFY participantsChanged)
    Q_PROPERTY(QVariantMap acks READ acks NOTIFY acksChanged)
    Q_PROPERTY(QVariantMap avatars READ avatars NOTIFY avatarsChanged)
    Q_PROPERTY(QVariantMap linkPreviews READ linkPreviews NOTIFY linkPreviewsChanged)
    Q_PROPERTY(QString selectedJid READ selectedJid WRITE setSelectedJid NOTIFY selectedJidChanged)
    Q_PROPERTY(QVariant selectedChat READ selectedChat NOTIFY selectedChatChanged)
    Q_PROPERTY(int unreadBadge READ unreadBadge NOTIFY unreadBadgeChanged)
    Q_PROPERTY(bool authenticated READ authenticated NOTIFY statusChanged)
    Q_PROPERTY(bool connected READ connected NOTIFY statusChanged)
    Q_PROPERTY(bool syncActive READ syncActive NOTIFY statusChanged)
    Q_PROPERTY(bool receipts READ receipts NOTIFY statusChanged)
    Q_PROPERTY(bool refreshing READ refreshing NOTIFY refreshingChanged)
    Q_PROPERTY(bool sending READ sending NOTIFY sendingChanged)
    Q_PROPERTY(bool loadingMessages READ loadingMessages NOTIFY loadingMessagesChanged)
    Q_PROPERTY(QString activity READ activity NOTIFY activityChanged)
    Q_PROPERTY(QString lastError READ lastError NOTIFY lastErrorChanged)
    Q_PROPERTY(QString storeDir READ storeDir NOTIFY statusChanged)

public:
    explicit WhatsApp(QQmlEngine *engine, QObject *parent = nullptr);

    QVariantList chats() const { return m_chats; }
    QVariantList messages() const { return m_messages; }
    QVariantList participants() const { return m_participants; }
    QVariantMap acks() const { return m_acks; }
    QVariantMap avatars() const { return m_avatarCache; }
    QVariantMap linkPreviews() const { return m_previewCache; }
    QString selectedJid() const { return m_selectedJid; }
    QVariant selectedChat() const;
    int unreadBadge() const { return m_unreadBadge; }
    bool authenticated() const { return m_authenticated; }
    bool connected() const { return m_connected; }
    bool syncActive() const { return m_syncActive; }
    bool receipts() const { return m_receipts; }
    bool refreshing() const { return m_refreshing; }
    bool sending() const { return m_sending; }
    bool loadingMessages() const;
    QString activity() const { return m_activity; }
    QString lastError() const { return m_lastError; }
    QString storeDir() const { return m_storeDir; }

    void setSelectedJid(const QString &jid);

    Q_INVOKABLE void refreshStatus();
    Q_INVOKABLE void refreshChats();
    Q_INVOKABLE void selectChat(const QString &jid);
    Q_INVOKABLE void markRead();
    Q_INVOKABLE void setReceipts(bool on);
    Q_INVOKABLE void setOnline(bool on);
    Q_INVOKABLE void sendText(const QString &text, const QVariantList &mentions, const QString &replyId);
    Q_INVOKABLE void sendFile(const QString &path, const QString &caption);
    Q_INVOKABLE void sendVoice(const QString &path, const QString &replyId);
    Q_INVOKABLE void download(const QVariantMap &message);
    Q_INVOKABLE void react(const QVariantMap &message, const QString &emoji);
    Q_INVOKABLE void fetchLinkPreview(const QString &url);
    Q_INVOKABLE void fetchAvatar(const QString &jid);
    Q_INVOKABLE void pickFiles(QJSValue done);
    Q_INVOKABLE void openEmojiPicker();
    Q_INVOKABLE void pickEmoji(QJSValue done);
    Q_INVOKABLE void clipboard(QJSValue done);
    Q_INVOKABLE void openFile(const QString &path);
    Q_INVOKABLE void voicePath(QJSValue done);
    Q_INVOKABLE QVariantMap draftFor(const QString &jid) const;
    Q_INVOKABLE void saveDraft(const QString &jid, const QVariantMap &draft);

signals:
    void chatsChanged();
    void messagesChanged();
    void participantsChanged();
    void acksChanged();
    void avatarsChanged();
    void linkPreviewsChanged();
    void selectedJidChanged();
    void selectedChatChanged();
    void unreadBadgeChanged();
    void statusChanged();
    void refreshingChanged();
    void sendingChanged();
    void loadingMessagesChanged();
    void activityChanged();
    void lastErrorChanged();

private:
    struct Job {
        QStringList args;
        QJSValue done;
        QString kind;
    };

    void call(const QStringList &args, const QString &kind = QString(), QJSValue done = QJSValue());
    void kick();
    void startDaemon();
    void handleResponse(const QByteArray &line);
    void onDaemonExit(int code, QProcess::ExitStatus status);
    void apply(const Job &job, const QVariantMap &data);
    void updateSelectedChat();
    void updateActivity();
    static bool busySyncError(const QString &error);
    static QString jobArg(const Job &job, const QString &flag);
    void ackChat(const QString &jid);
    void patchDownloaded(const QVariantMap &data);
    void patchLinkPreview(const QVariantMap &data);
    void patchAvatar(const QVariantMap &data);
    void overlayAvatars();
    void loadMessages(const QString &jid);
    void loadParticipants(const QString &jid);
    void startWatch();
    static QString helperPath();
    static QString omarchyShellPath();
    void finishJs(QJSValue done, const QString &error, const QVariantMap &data);
    static int badgeCount(const QVariantList &chats, const QVariantMap &acks);
    static QString chatFingerprint(const QVariantList &chats);
    static QString messageStamp(const QVariantList &messages);

    QQmlEngine *m_engine{nullptr};
    QProcess m_proc;
    QQueue<Job> m_queue;
    Job m_current;
    bool m_busy{false};
    bool m_useDaemon{true};
    bool m_oneShotPending{false};
    bool m_daemonEverResponded{false};
    int m_daemonFails{0};
    QElapsedTimer m_daemonUptime;
    QByteArray m_stdout;
    QByteArray m_stderr;

    QVariantList m_chats;
    QVariantList m_messages;
    QVariantList m_participants;
    QVariantMap m_acks;
    QVariantMap m_drafts;
    QString m_selectedJid;
    QVariantMap m_selectedChat;
    int m_unreadBadge{0};
    bool m_authenticated{false};
    bool m_connected{false};
    bool m_syncActive{false};
    bool m_receipts{false};
    bool m_refreshing{false};
    bool m_sending{false};
    QString m_activity;
    QString m_lastError;
    QString m_storeDir;

    QFileSystemWatcher m_watcher;
    QTimer m_debounce;
    QTimer m_fallback;
    QTimer m_postSend;
    int m_postSendTicks{0};
    QSet<QString> m_previewPending;
    QVariantMap m_previewCache;
    QSet<QString> m_avatarPending;
    QVariantMap m_avatarCache;
    QSet<QString> m_autoDownloaded;
    QString m_pendingMessagesJid;
    QString m_messagesJid;
    QString m_participantsJid;
    // Last-seen thread per chat, so re-opening a chat paints instantly while the
    // fresh copy loads. ponytail: flat clear at 24 entries, no LRU.
    QHash<QString, QVariantList> m_msgCache;
};
