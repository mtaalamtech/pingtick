package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

// csvText stops a spreadsheet from running a text value as a formula. Only
// use it for text, because it would turn a negative number into text.
func csvText(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\r\n", rune(value[0])) {
		return "'" + value
	}
	return value
}

func writeHistoryCSV(w io.Writer, history []LatencyHistory) error {
	out := csv.NewWriter(w)
	if err := out.Write([]string{"Timestamp", "Status", "Latency(ms)", "ReplyIP", "TTL"}); err != nil {
		return err
	}
	for _, h := range history {
		record := []string{csvText(h.Timestamp), csvText(h.Status), fmt.Sprint(h.Latency), csvText(h.ReplyIP), fmt.Sprint(h.TTL)}
		if err := out.Write(record); err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}

func writeReportCSV(w io.Writer, reports []TargetReport) error {
	out := csv.NewWriter(w)
	header := []string{"Date", "Target Name", "Type", "Category", "Host", "Current Status", "Total Checks", "Up Count", "Down Count", "Uptime %", "Avg Latency(ms)"}
	if err := out.Write(header); err != nil {
		return err
	}
	for _, r := range reports {
		record := []string{
			csvText(r.Date), csvText(r.Name), csvText(r.Type), csvText(r.Category), csvText(r.Host), csvText(r.Current),
			fmt.Sprint(r.Checks), fmt.Sprint(r.Up), fmt.Sprint(r.Down),
			fmt.Sprintf("%.1f", r.UptimePct), fmt.Sprintf("%.1f", r.AvgLatency),
		}
		if err := out.Write(record); err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}
