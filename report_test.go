package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestReportWindowUsesLocalDays(t *testing.T) {
	zone := time.FixedZone("UTC+3", 3*60*60)
	now := time.Date(2026, 11, 9, 1, 0, 0, 0, zone) // UTC is still 8 November

	tests := []struct {
		period, start, end string
		from, to           string
	}{
		{"today", "", "", "2026-11-08 21:00:00", "2026-11-09 21:00:00"},
		{"unknown", "", "", "2026-11-08 21:00:00", "2026-11-09 21:00:00"},
		{"yesterday", "", "", "2026-11-07 21:00:00", "2026-11-08 21:00:00"},
		{"this_week", "", "", "2026-11-01 21:00:00", "2026-11-09 21:00:00"},
		{"custom", "2026-11-01", "2026-11-03", "2026-10-31 21:00:00", "2026-11-03 21:00:00"},
		{"custom", "2026-11-05", "", "2026-11-08 21:00:00", "2026-11-09 21:00:00"},
	}
	for _, tt := range tests {
		from, to, err := reportWindow(tt.period, tt.start, tt.end, now)
		if err != nil || from != tt.from || to != tt.to {
			t.Errorf("%s %q %q: got %s to %s (%v), want %s to %s", tt.period, tt.start, tt.end, from, to, err, tt.from, tt.to)
		}
	}
}

func TestReportWindowHandlesShortDaylightSavingDay(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("time zone data not available")
	}
	from, to, err := reportWindow("today", "", "", time.Date(2026, 3, 8, 12, 0, 0, 0, zone))
	if err != nil || from != "2026-03-08 05:00:00" || to != "2026-03-09 04:00:00" {
		t.Fatalf("got %s to %s (%v)", from, to, err)
	}
}

func TestGlobalReportPlacesChecksOnTheirLocalDay(t *testing.T) {
	target := &Target{Name: "Report Day Target", Host: "report.test", Type: "ping", Interval: 10}
	if err := AddTarget(target); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { DeleteTarget(target.ID) })

	now := time.Now()
	localTime := func(dayOffset, hour, minute int) time.Time {
		return time.Date(now.Year(), now.Month(), now.Day()+dayOffset, hour, minute, 0, 0, now.Location())
	}
	for _, check := range []struct {
		when   time.Time
		status string
	}{
		{localTime(0, 0, 30), "online"},
		{localTime(-1, 23, 30), "offline"},
		{localTime(-1, 0, 30), "online"},
	} {
		_, err := db.Exec("INSERT INTO latency_history (target_id, timestamp, latency, status) VALUES (?, ?, 5, ?)",
			target.ID, check.when.UTC().Format("2006-01-02 15:04:05"), check.status)
		if err != nil {
			t.Fatal(err)
		}
	}

	today, yesterday := localTime(0, 0, 0).Format("2006-01-02"), localTime(-1, 0, 0).Format("2006-01-02")
	find := func(period, start, end string) []TargetReport {
		reports, err := GetGlobalReport(period, start, end)
		if err != nil {
			t.Fatal(err)
		}
		var found []TargetReport
		for _, report := range reports {
			if report.Name == target.Name {
				found = append(found, report)
			}
		}
		return found
	}

	if got := find("today", "", ""); len(got) != 1 || got[0].Date != today || got[0].Checks != 1 || got[0].Down != 0 {
		t.Fatalf("today = %+v", got)
	}
	if got := find("yesterday", "", ""); len(got) != 1 || got[0].Date != yesterday || got[0].Checks != 2 || got[0].Down != 1 {
		t.Fatalf("yesterday = %+v", got)
	}
	if got := find("custom", yesterday, today); len(got) != 2 || got[0].Date != today || got[1].Date != yesterday {
		t.Fatalf("custom = %+v", got)
	}
}

func TestGlobalReportRejectsUnsafeDates(t *testing.T) {
	for _, value := range []string{
		"2026-01-01' OR '1'='1",
		"2026-01-01'; DROP TABLE settings; --",
		"not-a-date",
		"2026-13-45",
	} {
		if _, err := GetGlobalReport("custom", value, "2026-01-31"); !errors.Is(err, errInvalidReportDate) {
			t.Errorf("start %q: err = %v", value, err)
		}
		if _, err := GetGlobalReport("custom", "2026-01-01", value); !errors.Is(err, errInvalidReportDate) {
			t.Errorf("end %q: err = %v", value, err)
		}
	}
}

func TestHistoryReportReturnsBadRequestForUnsafeDates(t *testing.T) {
	query := url.Values{"period": {"custom"}, "start": {"2026-01-01' OR '1'='1"}, "end": {"2026-01-31"}}
	response := httptest.NewRecorder()
	handleHistoryReport(response, httptest.NewRequest(http.MethodGet, "/api/history_report?"+query.Encode(), nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}
