package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validTargetForm() targetForm {
	return targetForm{Name: " Router ", Type: "ping", Host: " 192.168.1.1 ", Interval: "30", Category: "Core", Tag: "QA"}
}

func TestTargetFromFormAcceptsValidInput(t *testing.T) {
	target, err := targetFromForm(validTargetForm())
	if err != nil {
		t.Fatal(err)
	}
	if target.Name != "Router" || target.Host != "192.168.1.1" || target.Interval != 30 || target.Category != "Core" || target.Tag != "QA" {
		t.Fatalf("target = %+v", target)
	}
}

func TestTargetFromFormRejectsBadInput(t *testing.T) {
	tests := []struct {
		name   string
		change func(*targetForm)
	}{
		{"empty name", func(f *targetForm) { f.Name = " " }},
		{"unknown type", func(f *targetForm) { f.Type = "telnet" }},
		{"empty host", func(f *targetForm) { f.Host = "" }},
		{"host with space", func(f *targetForm) { f.Host = "a b" }},
		{"zero interval", func(f *targetForm) { f.Interval = "0" }},
		{"text interval", func(f *targetForm) { f.Interval = "soon" }},
		{"port without port", func(f *targetForm) { f.Type = "port"; f.Host = "server" }},
		{"port out of range", func(f *targetForm) { f.Type = "port"; f.Host = "server:70000" }},
		{"port without host", func(f *targetForm) { f.Type = "port"; f.Host = ":3306" }},
	}
	for _, tt := range tests {
		form := validTargetForm()
		tt.change(&form)
		if _, err := targetFromForm(form); err == nil {
			t.Errorf("%s: expected an error", tt.name)
		}
	}
}

func TestTargetFromFormKeepsOnlyFieldsForItsType(t *testing.T) {
	form := validTargetForm()
	form.WmiUser, form.WmiPass, form.SnmpOID = "admin", "secret", "1.3.6"

	target, err := targetFromForm(form)
	if err != nil {
		t.Fatal(err)
	}
	if target.WmiUser != "" || target.WmiPass != "" || target.SnmpOID != "" {
		t.Fatalf("ping target kept credentials: %+v", target)
	}

	form.Type = "wmi"
	target, err = targetFromForm(form)
	if err != nil {
		t.Fatal(err)
	}
	if target.WmiUser != "admin" || target.WmiPass != "secret" || target.SnmpOID != "" {
		t.Fatalf("wmi target = %+v", target)
	}
}

func TestParseHistoryTimeTreatsStoredTimesAsUTC(t *testing.T) {
	want := time.Date(2026, 11, 9, 13, 5, 6, 0, time.UTC)
	for _, value := range []string{"2026-11-09T13:05:06Z", "2026-11-09 13:05:06", "2026-11-09 13:05:06 +0000 UTC"} {
		got, ok := parseHistoryTime(value)
		if !ok || !got.Equal(want) {
			t.Errorf("parseHistoryTime(%q) = %v, %v", value, got, ok)
		}
	}
	if _, ok := parseHistoryTime("yesterday"); ok {
		t.Error("parsed an invalid time")
	}
}

func TestBuildInsightRowsAndSummary(t *testing.T) {
	rows := buildInsightRows([]LatencyHistory{
		{Timestamp: "2026-11-09 13:00:00", Latency: 10, Status: "online", ReplyIP: "10.0.0.1", TTL: 64},
		{Timestamp: "2026-11-09 13:00:10", Latency: 0, Status: "offline"},
		{Timestamp: "2026-11-09 13:00:20", Latency: 30, Status: "online", ReplyIP: "10.0.0.1"},
		{Timestamp: "2026-11-09 13:00:30", Latency: 5, Status: "online"},
	}, "ping")

	if rows[0].cells[1] != "Succeeded" || rows[0].cells[2] != "10 ms" || rows[0].cells[3] != "10.0.0.1" || rows[0].cells[4] != "64" {
		t.Fatalf("success row = %+v", rows[0])
	}
	if !rows[1].failed || rows[1].cells[1] != "Failed" || rows[1].cells[2] != "-" || rows[1].cells[3] != "-" {
		t.Fatalf("failed row = %+v", rows[1])
	}

	want := "Last 4 checks: 1 failed (25.0%). Average 15 ms, lowest 5 ms, highest 30 ms."
	if got := summarizeInsights(rows, "ping"); got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

func TestSummarizeInsightsWithFailuresFirstAndAllFailed(t *testing.T) {
	rows := []insightRow{{failed: true}, {latency: 40}, {latency: 20}}
	if got := summarizeInsights(rows, "ping"); !strings.Contains(got, "lowest 20 ms, highest 40 ms") {
		t.Fatalf("summary = %q", got)
	}
	if got := summarizeInsights([]insightRow{{failed: true}}, "ping"); got != "Last 1 checks: 1 failed (100.0%)." {
		t.Fatalf("summary = %q", got)
	}
	if got := summarizeInsights(nil, "ping"); got != "No checks recorded yet." {
		t.Fatalf("summary = %q", got)
	}
}

func TestSafeFileNameRemovesPathCharacters(t *testing.T) {
	if got := safeFileName(`..\web:01/"a b"`); got != "..-web-01--a-b-" {
		t.Fatalf("safeFileName = %q", got)
	}
}

func TestInsightsExportPathUsesDownloads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)

	path, err := insightsExportPath(`web/01`, time.Date(2026, 11, 9, 13, 5, 6, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "Downloads", "pingtick-web-01-20261109-130506.csv"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || !info.IsDir() {
		t.Fatalf("Downloads folder was not created: %v", err)
	}
}

func TestWriteHistoryCSV(t *testing.T) {
	var out bytes.Buffer
	if err := writeHistoryCSV(&out, []LatencyHistory{{Timestamp: "t", Status: "online", Latency: 5, ReplyIP: "1.1.1.1", TTL: 9}}); err != nil {
		t.Fatal(err)
	}
	if want := "Timestamp,Status,Latency(ms),ReplyIP,TTL\nt,online,5,1.1.1.1,9\n"; out.String() != want {
		t.Fatalf("csv = %q", out.String())
	}
}

func TestUpdateTargetKeepsCountersUnlessHostOrTypeChanges(t *testing.T) {
	target := &Target{Name: "Router", Host: "10.0.0.1", Type: "ping", Interval: 10}
	if err := AddTarget(target); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { DeleteTarget(target.ID) })

	mutex.Lock()
	targets[target.ID].TotalSent = 7
	targets[target.ID].FailedCount = 2
	mutex.Unlock()

	if err := UpdateTarget(&Target{ID: target.ID, Name: "Renamed", Host: "10.0.0.1", Type: "ping", Interval: 20}); err != nil {
		t.Fatal(err)
	}
	mutex.RLock()
	renamed := *targets[target.ID]
	mutex.RUnlock()
	if renamed.Name != "Renamed" || renamed.Interval != 20 || renamed.TotalSent != 7 || renamed.FailedCount != 2 {
		t.Fatalf("renamed target = %+v", renamed)
	}

	if err := UpdateTarget(&Target{ID: target.ID, Name: "Moved", Host: "10.0.0.2", Type: "ping", Interval: 20}); err != nil {
		t.Fatal(err)
	}
	mutex.RLock()
	moved := *targets[target.ID]
	mutex.RUnlock()
	if moved.TotalSent != 0 || moved.FailedCount != 0 {
		t.Fatalf("moved target kept old counters: %+v", moved)
	}
}

func TestGetTargetsOrdersEqualNamesConsistently(t *testing.T) {
	first := &Target{Name: "Same", Host: "a", Type: "ping", Interval: 10}
	second := &Target{Name: "Same", Host: "b", Type: "ping", Interval: 10}
	for _, target := range []*Target{first, second} {
		if err := AddTarget(target); err != nil {
			t.Fatal(err)
		}
		id := target.ID
		t.Cleanup(func() { DeleteTarget(id) })
	}

	want := GetTargets()
	for i := 0; i < 50; i++ {
		got := GetTargets()
		for j := range got {
			if got[j].ID != want[j].ID {
				t.Fatal("order changed between calls")
			}
		}
	}
}
