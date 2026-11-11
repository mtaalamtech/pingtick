package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFolderWritable(t *testing.T) {
	dir := t.TempDir()
	if !folderWritable(dir) {
		t.Fatal("temporary folder reported as not writable")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("write test left files behind: %v %v", entries, err)
	}
	if folderWritable(filepath.Join(dir, "missing")) {
		t.Fatal("missing folder reported as writable")
	}
}

func TestDatabasePathForTestBinaryUsesCurrentFolder(t *testing.T) {
	// go test, like go run, builds into the temporary folder.
	if got := databasePath(); got != "pingtick.db" {
		t.Fatalf("databasePath() = %q", got)
	}
}

func TestDatabasePathFor(t *testing.T) {
	temp := t.TempDir()
	appData := t.TempDir()
	portable := t.TempDir()
	saved := filepath.Join(appData, "PingTick", databaseName)

	cases := []struct {
		name, exeDir, want string
	}{
		{"extracted folder", portable, filepath.Join(portable, databaseName)},
		{"go run build", filepath.Join(temp, "go-build123", "b001", "exe"), databaseName},
		{"opened inside a zip", filepath.Join(temp, "Temp1_PingTick.zip", "PingTick"), saved},
		{"folder that cannot be written", filepath.Join(portable, "missing"), saved},
	}
	for _, c := range cases {
		if got := databasePathFor(c.exeDir, temp, appData); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAutoSpeedTestIsOffUnlessSwitchedOn(t *testing.T) {
	if autoSpeedTestEnabled() {
		t.Fatal("enabled with no setting saved")
	}
	saveSetting("auto_speed_test", "true")
	t.Cleanup(func() { db.Exec("DELETE FROM settings WHERE key = 'auto_speed_test'") })
	if !autoSpeedTestEnabled() {
		t.Fatal("not enabled after switching on")
	}
}
