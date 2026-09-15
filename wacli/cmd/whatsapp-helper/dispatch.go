package main

import (
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/openclaw/wacli/internal/cli"
)

// detachProcAttr detaches the child from our process group so it survives us.
func detachProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

type flagSpec struct {
	key      string // canonical name in the parsed map
	kind     byte   // 's' string, 'i' int, 'b' bool flag, 'a' repeat-string
	required bool
}

type command struct {
	name  string
	flags []flagSpec
}

var commands = map[string][]flagSpec{
	"status": {},
	"chats": {
		{key: "query", kind: 's'},
		{key: "limit", kind: 'i'},
		{key: "include-archived", kind: 'b'},
	},
	"messages": {
		{key: "chat", kind: 's', required: true},
		{key: "limit", kind: 'i'},
		{key: "before", kind: 'i'},
		{key: "query", kind: 's'},
	},
	"participants": {
		{key: "chat", kind: 's', required: true},
	},
	"send-text": {
		{key: "chat", kind: 's', required: true},
		{key: "message", kind: 's', required: true},
		{key: "reply-to", kind: 's'},
		{key: "mention", kind: 'a'},
	},
	"send-file": {
		{key: "chat", kind: 's', required: true},
		{key: "file", kind: 's', required: true},
		{key: "caption", kind: 's'},
		{key: "as", kind: 's'},
		{key: "reply-to", kind: 's'},
	},
	"send-voice": {
		{key: "chat", kind: 's', required: true},
		{key: "file", kind: 's', required: true},
		{key: "reply-to", kind: 's'},
	},
	"download": {
		{key: "chat", kind: 's', required: true},
		{key: "id", kind: 's', required: true},
	},
	"react": {
		{key: "chat", kind: 's', required: true},
		{key: "id", kind: 's', required: true},
		{key: "sender", kind: 's'},
		{key: "emoji", kind: 's', required: true},
	},
	"ack": {
		{key: "chat", kind: 's', required: true},
		{key: "ts", kind: 'i', required: true},
	},
	"receipts": {
		{key: "on", kind: 'b'},
		{key: "off", kind: 'b'},
	},
	"mark-read": {
		{key: "chat", kind: 's', required: true},
		{key: "ts", kind: 'i'},
	},
	"voice-path":   {},
	"pick-files":   {},
	"pick-emoji":   {{key: "watch-only", kind: 'b'}},
	"clipboard":    {},
	"open-file":    {{key: "file", kind: 's', required: true}},
	"link-preview": {{key: "url", kind: 's', required: true}},
	"avatar":       {{key: "jid", kind: 's', required: true}},
	"refresh-avatars": {},
}

// parseArgs mirrors the Python argparse contract: any unknown command, unknown
// flag, missing value, or missing required flag is "bad command".
func parseArgs(argv []string) (string, map[string]any, *helperError) {
	if len(argv) == 0 {
		return "", nil, fail("bad command")
	}
	spec, ok := commands[argv[0]]
	if !ok {
		return "", nil, fail("bad command")
	}
	parsed := map[string]any{}
	var positional []string
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if !strings.HasPrefix(arg, "--") {
			positional = append(positional, arg)
			continue
		}
		name := strings.TrimPrefix(arg, "--")
		var specFound *flagSpec
		for idx := range spec {
			if spec[idx].key == name {
				specFound = &spec[idx]
				break
			}
		}
		if specFound == nil {
			return "", nil, fail("bad command")
		}
		switch specFound.kind {
		case 'b':
			parsed[specFound.key] = true
		case 's', 'i', 'a':
			if i+1 >= len(argv) {
				return "", nil, fail("bad command")
			}
			i++
			value := argv[i]
			switch specFound.kind {
			case 'i':
				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					return "", nil, fail("bad command")
				}
				parsed[specFound.key] = n
			case 'a':
				existing, _ := parsed[specFound.key].([]string)
				parsed[specFound.key] = append(existing, value)
			default:
				parsed[specFound.key] = value
			}
		}
	}
	if len(positional) > 0 {
		return "", nil, fail("bad command")
	}
	for _, f := range spec {
		if f.required {
			if _, ok := parsed[f.key]; !ok {
				return "", nil, fail("bad command")
			}
		}
	}
	return argv[0], parsed, nil
}

// dispatch parses one command and returns its payload.
func dispatch(argv []string) (map[string]any, *helperError) {
	storeOverride := ""
	if len(argv) >= 2 && argv[0] == "--store" {
		// Global --store precedes the subcommand, as argparse had it.
		storeOverride = argv[1]
		argv = argv[2:]
	}
	cmd, args, he := parseArgs(argv)
	if he != nil {
		return nil, he
	}
	store := storeDir(storeOverride)
	pruneMedia(7, 6*time.Hour)

	switch cmd {
	case "status":
		return cmdStatus(store)
	case "chats":
		chats, he := listChats(store, strArg(args, "query"), int(intArg(args, "limit", 80)), boolArg(args, "include-archived"))
		if he != nil {
			return nil, he
		}
		return map[string]any{"chats": chats, "syncActive": syncActive()}, nil
	case "messages":
		jid, he := requireJID(strArg(args, "chat"))
		if he != nil {
			return nil, he
		}
		messages, he := listMessagesQuery(store, jid, int(intArg(args, "limit", 80)), intArg(args, "before", 0), strArg(args, "query"))
		if he != nil {
			return nil, he
		}
		return map[string]any{"messages": messages}, nil
	case "participants":
		jid, he := requireJID(strArg(args, "chat"))
		if he != nil {
			return nil, he
		}
		participants, he := listParticipants(store, jid)
		if he != nil {
			return nil, he
		}
		return map[string]any{"participants": participants}, nil
	case "send-text":
		jid, he := requireJID(strArg(args, "chat"))
		if he != nil {
			return nil, he
		}
		mentions, _ := args["mention"].([]string)
		return sendText(store, jid, strArg(args, "message"), strArg(args, "reply-to"), mentions)
	case "send-file":
		jid, he := requireJID(strArg(args, "chat"))
		if he != nil {
			return nil, he
		}
		return sendFile(store, jid, strArg(args, "file"), strArg(args, "caption"),
			firstNonEmpty(strArg(args, "as"), "auto"), strArg(args, "reply-to"))
	case "send-voice":
		jid, he := requireJID(strArg(args, "chat"))
		if he != nil {
			return nil, he
		}
		return sendVoice(store, jid, strArg(args, "file"), strArg(args, "reply-to"))
	case "download":
		jid, he := requireJID(strArg(args, "chat"))
		if he != nil {
			return nil, he
		}
		return downloadMedia(store, jid, strArg(args, "id"))
	case "react":
		jid, he := requireJID(strArg(args, "chat"))
		if he != nil {
			return nil, he
		}
		return sendReact(store, jid, strArg(args, "id"), strArg(args, "sender"), strArg(args, "emoji"))
	case "ack":
		jid, he := requireJID(strArg(args, "chat"))
		if he != nil {
			return nil, he
		}
		return cmdAck(jid, intArg(args, "ts", 0)), nil
	case "receipts":
		on := boolArg(args, "on")
		off := boolArg(args, "off")
		if on == off {
			return nil, fail("pass --on or --off")
		}
		return cmdReceipts(on), nil
	case "mark-read":
		jid, he := requireJID(strArg(args, "chat"))
		if he != nil {
			return nil, he
		}
		return cmdMarkRead(store, jid, intArg(args, "ts", 0))
	case "voice-path":
		return cmdVoicePath(), nil
	case "pick-files":
		return cmdPickFiles()
	case "pick-emoji":
		return cmdPickEmoji(boolArg(args, "watch-only"))
	case "clipboard":
		return cmdClipboard()
	case "open-file":
		return cmdOpenFile(strArg(args, "file"))
	case "link-preview":
		return cmdLinkPreview(strArg(args, "url"))
	case "avatar":
		return cmdAvatar(store, strArg(args, "jid"))
	case "refresh-avatars":
		return cmdRefreshAvatars(store)
	}
	return nil, fail("bad command")
}

func requireJID(value string) (string, *helperError) {
	jid := strings.TrimSpace(value)
	if jid == "" || !jidRe.MatchString(jid) {
		return "", fail("chat target is not a valid JID")
	}
	return jid, nil
}

func strArg(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

func boolArg(args map[string]any, key string) bool {
	b, _ := args[key].(bool)
	return b
}

func intArg(args map[string]any, key string, def int64) int64 {
	if n, ok := args[key].(int64); ok {
		return n
	}
	return def
}

// cliRun runs the embedded wacli CLI (same setup its own main() performs).
func cliRun(args []string) error {
	return cli.Run(args)
}
