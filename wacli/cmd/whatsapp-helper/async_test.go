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
