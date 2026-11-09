package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

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
