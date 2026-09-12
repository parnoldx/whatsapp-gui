package main

import (
	"os"
	"testing"
	"time"
)

// A cache miss must answer from the daemon loop immediately; the slow
// picture-info call belongs on the background goroutine.
func TestCmdAvatarDoesNotBlockOnMiss(t *testing.T) {
	t.Setenv("PA_WHATSAPP_STATE", t.TempDir())
	started := make(chan struct{})
	release := make(chan struct{})
	old := runWacliFn
	runWacliFn = func(args []string, opts wacliOpts) (map[string]any, *helperError) {
		close(started)
		<-release
		return map[string]any{}, nil
	}
	defer func() { runWacliFn = old; close(release) }()

	done := make(chan map[string]any, 1)
	go func() {
		data, he := cmdAvatar("", "491234567890@s.whatsapp.net")
		if he != nil {
			t.Error(he)
		}
		done <- data
	}()
	select {
	case data := <-done:
		if data["fileUrl"] != "" {
			t.Fatalf("miss should report no avatar, got %v", data["fileUrl"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cmdAvatar blocked on the network fetch")
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("background fetch never ran")
	}
}

// A media transfer must leave the request pipe free and arrive as a push.
func TestDownloadPushesInsteadOfBlocking(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	con := openrw(t, f.db)
	mustExec(t, con, insertMsg, []any{
		"111@s.whatsapp.net", "Ada", "slow1", "111@s.whatsapp.net", "Ada", 110, 0,
		"", "", "", "", 0, 0, "", "", "image", "", "pic.jpg", "image/jpeg",
		12, "", 0, 0, 0, 0, 0, "",
	})
	con.Close()

	pushes := make(chan map[string]any, 4)
	outMu.Lock()
	outLine = func(payload map[string]any) { pushes <- payload }
	outMu.Unlock()
	defer func() { outMu.Lock(); outLine = nil; outMu.Unlock() }()

	release := make(chan struct{})
	saved := runWacliFn
	runWacliFn = func(args []string, opts wacliOpts) (map[string]any, *helperError) {
		<-release
		for i, a := range args {
			if a == "--output" {
				if err := os.WriteFile(args[i+1], []byte("\xff\xd8\xff\xd9"), 0o600); err != nil {
					t.Error(err)
				}
			}
		}
		return map[string]any{}, nil
	}
	defer func() { runWacliFn = saved }()

	done := make(chan map[string]any, 1)
	go func() {
		data, he := downloadMedia(f.store, "111@s.whatsapp.net", "slow1")
		if he != nil {
			t.Error(he)
		}
		done <- data
	}()
	select {
	case data := <-done:
		if data["pending"] != true {
			t.Fatalf("expected a pending answer, got %v", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("downloadMedia blocked on the transfer")
	}
	close(release)
	select {
	case p := <-pushes:
		if p["push"] != "downloaded" {
			t.Fatalf("expected a downloaded push, got %v", p["push"])
		}
		data, _ := p["data"].(map[string]any)
		if data["localPath"] == "" || data["id"] != "slow1" {
			t.Fatalf("push carries no finished file: %v", data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("finished download was never pushed")
	}
}

// The daily refresh must skip avatars already on disk — re-fetching all of them
// keeps wacli-sync stopped for as long as the walk takes.
func TestFetchAvatarSkipsCachedFile(t *testing.T) {
	t.Setenv("PA_WHATSAPP_STATE", t.TempDir())
	jid := "491234567890@s.whatsapp.net"
	if err := os.WriteFile(avatarFile(jid), []byte("\xff\xd8\xff\xd9"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	saved := runWacliFn
	runWacliFn = func(args []string, opts wacliOpts) (map[string]any, *helperError) {
		called = true
		return map[string]any{}, nil
	}
	defer func() { runWacliFn = saved }()
	fetchAvatar("", jid)
	if called {
		t.Fatal("refetched an avatar that was already cached")
	}
}

// Avatars must never cost a sync restart: WhatsApp replays the offline backlog
// on reconnect and it is acked whether or not it lands in the store.
func TestRefreshAvatarsSkipsWhileSyncRuns(t *testing.T) {
	f := newFixture(t)
	saved := syncActiveFn
	syncActiveFn = func() bool { return true }
	defer func() { syncActiveFn = saved }()
	called := false
	savedRun := runWacliFn
	runWacliFn = func(args []string, opts wacliOpts) (map[string]any, *helperError) {
		called = true
		return map[string]any{}, nil
	}
	defer func() { runWacliFn = savedRun }()

	data, he := cmdRefreshAvatars(f.store)
	if he != nil {
		t.Fatal(he)
	}
	if data["skipped"] != true {
		t.Fatalf("expected a skip while sync runs, got %v", data)
	}
	if called {
		t.Fatal("fetched avatars while sync was running")
	}
}

// mark-read must answer from the local ack at once; a failed WhatsApp round
// trip rolls the ack back and pushes the corrected map.
func TestMarkReadAnswersBeforeWacliAndPushesRollback(t *testing.T) {
	resetLidMap()
	f := newFixture(t)
	pushes := make(chan map[string]any, 4)
	outMu.Lock()
	outLine = func(payload map[string]any) { pushes <- payload }
	outMu.Unlock()
	defer func() { outMu.Lock(); outLine = nil; outMu.Unlock() }()

	release := make(chan struct{})
	saved := runWacliFn
	runWacliFn = func(args []string, opts wacliOpts) (map[string]any, *helperError) {
		<-release
		return nil, fail("store is locked")
	}
	defer func() { runWacliFn = saved }()

	done := make(chan map[string]any, 1)
	go func() {
		data, he := cmdMarkRead(f.store, "111@s.whatsapp.net", 0)
		if he != nil {
			t.Error(he)
		}
		done <- data
	}()
	select {
	case data := <-done:
		if acks, _ := data["acks"].(map[string]int64); acks["111@s.whatsapp.net"] != 100 {
			t.Fatalf("acks = %v, want chat acked at 100", data["acks"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cmdMarkRead blocked on the wacli round trip")
	}
	close(release)
	select {
	case p := <-pushes:
		if p["push"] != "mark-read" {
			t.Fatalf("push kind = %v", p["push"])
		}
		acks, _ := p["data"].(map[string]any)["acks"].(map[string]int64)
		if acks["111@s.whatsapp.net"] != 0 {
			t.Errorf("pushed ack = %d, want rolled back to 0", acks["111@s.whatsapp.net"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no rollback push")
	}
	if got := loadPrefs().Acks["111@s.whatsapp.net"]; got != 0 {
		t.Errorf("ack on disk = %d, want 0", got)
	}
}
