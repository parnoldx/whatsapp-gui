// Command whatsapp-helper is the local backend for whatsapp-gui.
//
// Reads wacli's SQLite mirror read-only. Every WhatsApp write goes through
// the embedded wacli commands, invoked by re-executing this same binary in
// "wacli mode" (`whatsapp-helper wacli --json ...`), so the whole backend is
// one file. Never logs chat names, JIDs, message bodies, or media paths.
//
// Usage: one-shot `whatsapp-helper <command ...>` prints a single JSON line,
// or `whatsapp-helper --daemon` reads newline-delimited JSON argv arrays on
// stdin and answers one JSON line per command.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	maxText    = 4096
	maxFile    = 100 * 1024 * 1024
	maxChats   = 200
	maxMessage = 200
	maxPartic  = 500
	maxStdout  = 2 * 1024 * 1024
	maxFiles   = 10
)

// helperError is a user-facing failure with an exit code.
type helperError struct {
	msg  string
	code int
}

func (e *helperError) Error() string { return e.msg }

func fail(format string, args ...any) *helperError {
	return &helperError{msg: fmt.Sprintf(format, args...), code: 1}
}

func emit(payload map[string]any) {
	out, _ := json.Marshal(payload)
	os.Stdout.Write(out)
	os.Stdout.Write([]byte("\n"))
}

func ok(data any)  { emit(map[string]any{"ok": true, "data": data}) }
func oops(err error) {
	if he := (*helperError)(nil); errors.As(err, &he) {
		emit(map[string]any{"ok": false, "error": he.msg})
		return
	}
	emit(map[string]any{"ok": false, "error": "helper error"})
}

// runWacliFn is swapped out in tests.
var runWacliFn = runWacli

type wacliOpts struct {
	store    string
	timeout  time.Duration
	readonly bool
	lockWait string // "" omits the flag
}

// runWacli runs the embedded wacli commands and returns the unwrapped data.
func runWacli(args []string, opts wacliOpts) (map[string]any, *helperError) {
	self, err := os.Executable()
	if err != nil {
		return nil, fail("could not locate the helper binary")
	}
	cmd := []string{"wacli", "--json"}
	if opts.readonly {
		cmd = append(cmd, "--read-only")
	}
	if opts.store != "" {
		cmd = append(cmd, "--store", opts.store)
	}
	if !opts.readonly && opts.lockWait != "" {
		cmd = append(cmd, "--lock-wait", opts.lockWait)
	}
	cmd = append(cmd, args...)
	if bin := os.Getenv("WACLI_BIN"); bin != "" && bin != self {
		// ponytail: WACLI_BIN bypass for debugging against a real wacli;
		// self-exec is the default and the only path the GUI uses.
		self = bin
		cmd = cmd[1:] // drop the "wacli" mode token
	}
	ec := exec.Command(self, cmd...)
	ec.Stdout = &limitedBuffer{limit: maxStdout}
	ec.Stderr = &limitedBuffer{limit: maxStdout}
	if err := ec.Start(); err != nil {
		if _, ok := err.(*exec.Error); ok {
			return nil, fail("wacli is not installed")
		}
		return nil, fail("wacli command failed")
	}
	runErr := waitTimeout(ec, opts.timeout)
	if errors.Is(runErr, errTimedOut) {
		return nil, fail("wacli timed out")
	}
	stdout := ec.Stdout.(*limitedBuffer)
	stderr := ec.Stderr.(*limitedBuffer)
	if stdout.over || stderr.over {
		return nil, fail("wacli output was too large")
	}
	raw := strings.TrimSpace(stdout.String())
	if raw == "" {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fail("wacli produced no output")
		}
		return nil, fail("%s", firstLine(msg))
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fail("wacli did not return JSON")
	}
	if ec.ProcessState.ExitCode() != 0 {
		if he := unwrapWacli(payload); he != nil {
			return nil, he
		}
		return nil, fail("wacli command failed")
	}
	data, he := unwrapWacliData(payload)
	return data, he
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 180 {
		s = s[:180]
	}
	return s
}

type limitedBuffer struct {
	buf   []byte
	limit int
	over  bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(b.buf)+len(p) > b.limit {
		b.over = true
		return len(p), nil
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *limitedBuffer) String() string { return string(b.buf) }

// unwrapWacli fails when the payload signals an error.
func unwrapWacli(payload map[string]any) *helperError {
	if success, ok := payload["success"].(bool); ok && !success {
		return fail("%s", errText(payload))
	}
	if success, ok := payload["ok"].(bool); ok && !success {
		return fail("%s", errText(payload))
	}
	return nil
}

func errText(payload map[string]any) string {
	if msg, ok := payload["error"].(string); ok && msg != "" {
		return msg
	}
	return "wacli command failed"
}

// unwrapWacliData mirrors the Python helper's unwrap: {"data": ...} peeling,
// scalars wrapped as {"value": ...}, lists as {"items": [...]}.
func unwrapWacliData(payload map[string]any) (map[string]any, *helperError) {
	if he := unwrapWacli(payload); he != nil {
		return nil, he
	}
	data, present := payload["data"]
	if !present || data == nil {
		return payload, nil
	}
	switch v := data.(type) {
	case map[string]any:
		return v, nil
	case []any:
		return map[string]any{"items": v}, nil
	default:
		return map[string]any{"value": v}, nil
	}
}

// Slow commands answer "pending" and push the real result later, so one media
// download does not stall every chat and message query behind it on the single
// request pipe. outLine is nil in one-shot mode, where everything stays
// synchronous — pushable() is what the slow paths branch on.
var (
	outMu   sync.Mutex
	outLine func(map[string]any)
)

func writeOut(payload map[string]any) bool {
	outMu.Lock()
	defer outMu.Unlock()
	if outLine == nil {
		return false
	}
	outLine(payload)
	return true
}

func pushable() bool {
	outMu.Lock()
	defer outMu.Unlock()
	return outLine != nil
}

// push delivers an unsolicited result. The GUI routes it by kind instead of
// matching it against the request it has in flight.
func push(kind string, data map[string]any) {
	writeOut(map[string]any{"ok": true, "push": kind, "data": data})
}

// runDaemon answers one JSON line per input line.
func runDaemon() int {
	dieWithParent()
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	outMu.Lock()
	outLine = func(payload map[string]any) {
		out, _ := json.Marshal(payload)
		w.Write(out)
		w.WriteByte('\n')
		w.Flush()
	}
	outMu.Unlock()
	defer func() {
		outMu.Lock()
		outLine = nil
		outMu.Unlock()
	}()
	answer := func(payload map[string]any) { writeOut(payload) }
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var argv []string
		if err := json.Unmarshal([]byte(line), &argv); err != nil {
			answer(map[string]any{"ok": false, "error": "bad command"})
			continue
		}
		data, err := dispatch(argv)
		if err != nil {
			answer(map[string]any{"ok": false, "error": err.msg})
		} else {
			answer(map[string]any{"ok": true, "data": data})
		}
	}
	return 0
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "wacli" {
		// Embedded wacli mode: run the vendored CLI verbatim.
		if err := cliRun(os.Args[2:]); err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--daemon" {
		os.Exit(runDaemon())
	}
	data, err := dispatch(os.Args[1:])
	if err != nil {
		oops(err)
		os.Exit(err.code)
	}
	ok(data)
}
