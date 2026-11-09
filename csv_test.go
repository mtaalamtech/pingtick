package main

import (
	"bytes"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCSVTextNeutralisesFormulas(t *testing.T) {
	for _, value := range []string{"=1+1", "+1", "-1", "@SUM(A1)", "\tx", "\rx", "\nx"} {
		if got := csvText(value); got != "'"+value {
			t.Errorf("csvText(%q) = %q", value, got)
		}
	}
	for _, value := range []string{"", "Router", "10.0.0.1", "a=b", "web-01"} {
		if got := csvText(value); got != value {
			t.Errorf("csvText(%q) = %q", value, got)
		}
	}
}

func readCSV(t *testing.T, data []byte) [][]string {
	t.Helper()
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("invalid csv: %v\n%s", err, data)
	}
	return records
}

func TestWriteReportCSVKeepsColumnsAndNumbers(t *testing.T) {
	var out bytes.Buffer
	err := writeReportCSV(&out, []TargetReport{{
		Date: "2026-11-09", Name: `=HYPERLINK("http://x.test","a, b")`, Type: "ping", Category: "Core, East", Host: "@host",
		Current: "online", Checks: 10, Up: 9, Down: 1, UptimePct: 90, AvgLatency: 1.25,
	}})
	if err != nil {
		t.Fatal(err)
	}

	records := readCSV(t, out.Bytes())
	if len(records) != 2 || len(records[1]) != len(records[0]) {
		t.Fatalf("records = %q", records)
	}
	row := records[1]
	if row[1] != `'=HYPERLINK("http://x.test","a, b")` || row[3] != "Core, East" || row[4] != "'@host" {
		t.Fatalf("text columns = %q", row)
	}
	if row[6] != "10" || row[7] != "9" || row[8] != "1" || row[9] != "90.0" || row[10] != "1.2" {
		t.Fatalf("number columns = %q", row)
	}
}

func TestWriteHistoryCSVKeepsNegativeTTLAsNumber(t *testing.T) {
	var out bytes.Buffer
	if err := writeHistoryCSV(&out, []LatencyHistory{{Timestamp: "t", Status: "online", Latency: 5, ReplyIP: "=evil", TTL: -1}}); err != nil {
		t.Fatal(err)
	}
	if want := "Timestamp,Status,Latency(ms),ReplyIP,TTL\nt,online,5,'=evil,-1\n"; out.String() != want {
		t.Fatalf("csv = %q", out.String())
	}
}

func TestExportGlobalCSVNeutralisesHostileTargetName(t *testing.T) {
	name := `=cmd|' /C calc'!A0, "x"`
	target := &Target{Name: name, Host: "export.test", Type: "ping", Interval: 10, Tag: "Core"}
	if err := AddTarget(target); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { DeleteTarget(target.ID) })
	if _, err := db.Exec("UPDATE targets SET tag = 'Core' WHERE id = ?", target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO latency_history (target_id, timestamp, latency, status) VALUES (?, ?, 5, 'online')",
		target.ID, time.Now().UTC().Format("2006-01-02 15:04:05")); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handleExportGlobalCsv(response, httptest.NewRequest(http.MethodGet, "/api/export_global?format=csv&period=today", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}

	found := false
	for _, record := range readCSV(t, response.Body.Bytes()) {
		if len(record) > 4 && record[4] == "export.test" {
			found = true
			if record[1] != "'"+name || len(record) != 11 {
				t.Fatalf("row = %q", record)
			}
		}
	}
	if !found {
		t.Fatal("target row is missing from the export")
	}
}

func TestExportGlobalCSVIgnoresTextInPeriod(t *testing.T) {
	for _, query := range []url.Values{
		{"period": {"=evil(),1"}},
		{"period": {"custom"}, "start": {"=evil()"}},
	} {
		response := httptest.NewRecorder()
		handleExportGlobalCsv(response, httptest.NewRequest(http.MethodGet, "/api/export_global?format=csv&"+query.Encode(), nil))
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d", response.Code)
		}
		if strings.Contains(response.Body.String(), "evil") || strings.Contains(response.Header().Get("Content-Disposition"), "evil") {
			t.Fatalf("%v reached the export", query)
		}
	}
}
