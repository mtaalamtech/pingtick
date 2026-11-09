package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/getlantern/systray"
	"github.com/jung-kurt/gofpdf"
)

//go:embed frontend/*
var embedFrontend embed.FS

//go:embed icon2.ico
var trayIcon []byte

//go:embed winres/icon2.png
var reportLogo []byte

var activeURL string

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

func main() {
	// PingTick is meant to stay open for days, so favour a small footprint.
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(64 << 20)

	systray.Run(onReady, onExit)
}

func onExit() {
	CloseDB()
}

func onReady() {
	systray.SetIcon(trayIcon)
	systray.SetTitle("PingTick")
	systray.SetTooltip("PingTick Monitor Daemon Active")

	mMonitor := systray.AddMenuItem("Open Monitor", "Show the PingTick monitor window")
	mOpen := systray.AddMenuItem("Open Web Dashboard", "Open settings, reports and the IP scanner in a browser")
	mQuit := systray.AddMenuItem("Shutdown Engine", "Stop PingTick daemon")

	// Initialize Database
	InitDB()

	// Start the background monitoring engine
	StartMonitoring()

	// Start hourly automatic speed test
	StartAutoSpeedTest()

	// Setup embedded filesystem serving
	frontendFS, err := fs.Sub(embedFrontend, "frontend")
	if err != nil {
		log.Fatalf("Failed to create sub filesystem: %v", err)
	}

	fsHandler := http.FileServer(http.FS(frontendFS))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		fsHandler.ServeHTTP(w, r)
	})

	// API routes
	http.HandleFunc("/api/status", handleStatus)
	http.HandleFunc("/api/targets", handleTargets)
	http.HandleFunc("/api/history", handleHistory)
	http.HandleFunc("/api/history_all", handleHistoryAll)
	http.HandleFunc("/api/export", handleExportCsv)
	http.HandleFunc("/api/export_global", handleExportGlobalCsv)
	http.HandleFunc("/api/history_report", handleHistoryReport)
	http.HandleFunc("/api/settings", handleSettings)
	http.HandleFunc("/api/speedtest", handleSpeedTest)
	http.HandleFunc("/api/test_alerts", handleTestAlerts)
	http.HandleFunc("/api/shutdown", handleShutdown)
	http.HandleFunc("/api/scan", handleScan)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port
	activeURL = fmt.Sprintf("http://localhost:%d", actualPort)

	fmt.Printf("Starting PingTick on %s\n", activeURL)

	go func() {
		for {
			select {
			case <-mMonitor.ClickedCh:
				showMonitorWindow()
			case <-mOpen.ClickedCh:
				openBrowser(activeURL)
			case <-mQuit.ClickedCh:
				systray.Quit()
			}
		}
	}()

	go runMonitorWindow()

	go func() {
		log.Fatal(http.Serve(listener, localOnly(http.DefaultServeMux)))
	}()
}

// localOnly blocks requests that come from other websites, including through
// DNS rebinding, so a web page cannot read or drive this local API.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil || (host != "localhost" && host != "127.0.0.1") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isKnownTargetType(t string) bool {
	switch t {
	case "ping", "ssl", "port", "dns", "snmp", "bandwidth", "wmi":
		return true
	}
	return false
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status": "ok"}`))
}

func handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status": "shutting down"}`))
	go func() {
		time.Sleep(1 * time.Second)
		systray.Quit()
	}()
}

func handleTargets(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		json.NewEncoder(w).Encode(GetTargets())

	case http.MethodPost:
		var t Target
		if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if t.Name == "" || t.Host == "" || t.Interval <= 0 {
			http.Error(w, "Invalid inputs details", http.StatusBadRequest)
			return
		}
		if !isKnownTargetType(t.Type) {
			t.Type = "ping"
		}

		if err := AddTarget(&t); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(t)

	case http.MethodPut:
		var t Target
		if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !isKnownTargetType(t.Type) {
			t.Type = "ping"
		}
		if t.ID == "" || t.Name == "" || t.Host == "" || t.Interval <= 0 {
			http.Error(w, "Invalid input details", http.StatusBadRequest)
			return
		}
		if err := UpdateTarget(&t); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(t)

	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "Missing id parameter", http.StatusBadRequest)
			return
		}
		if err := DeleteTarget(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{"success": true}`))

	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func handleHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "Missing id parameter", http.StatusBadRequest)
		return
	}
	history, err := GetTargetHistory(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(history)
}

func handleHistoryAll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	history, err := GetGlobalHistory()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(history)
}

func reportErrorStatus(err error) int {
	if errors.Is(err, errInvalidReportDate) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func handleHistoryReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "today"
	}
	reports, err := GetGlobalReport(period, r.URL.Query().Get("start"), r.URL.Query().Get("end"))
	if err != nil {
		http.Error(w, err.Error(), reportErrorStatus(err))
		return
	}
	json.NewEncoder(w).Encode(reports)
}

func handleExportCsv(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "Missing id parameter", http.StatusBadRequest)
		return
	}
	history, err := GetFullTargetHistory(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"pingtick-export-%s.csv\"", id))

	if err := writeHistoryCSV(w, history); err != nil {
		log.Printf("Could not write history export: %v", err)
	}
}

func handleExportGlobalCsv(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "today"
	}
	customStart := r.URL.Query().Get("start")
	customEnd := r.URL.Query().Get("end")

	reports, err := GetGlobalReport(period, customStart, customEnd)
	if err != nil {
		http.Error(w, err.Error(), reportErrorStatus(err))
		return
	}

	dateStr := period
	if period == "today" {
		dateStr = time.Now().Format("2006-01-02")
	} else if period == "yesterday" {
		dateStr = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	} else if period == "this_week" {
		dateStr = time.Now().AddDate(0, 0, -7).Format("2006-01-02") + "_to_" + time.Now().Format("2006-01-02")
	} else if period == "custom" {
		dateStr = customStart + "_to_" + customEnd
	}

	total := len(reports)
	up := 0
	down := 0
	uptimeSum := 0.0
	for _, rp := range reports {
		if rp.Current == "online" {
			up++
		} else {
			down++
		}
		uptimeSum += rp.UptimePct
	}
	avgUptime := 0.0
	if total > 0 {
		avgUptime = uptimeSum / float64(total)
	}

	format := r.URL.Query().Get("format")
	if format == "pdf" {
		pdf := gofpdf.New("L", "mm", "A4", "")
		pdf.AddPage()

		// Embed native PNG logo dynamically top left.
		pdf.RegisterImageOptionsReader("pingtick_logo", gofpdf.ImageOptions{ImageType: "PNG", ReadDpi: true}, bytes.NewReader(reportLogo))
		pdf.ImageOptions("pingtick_logo", 10, 10, 15, 0, false, gofpdf.ImageOptions{ImageType: "PNG", ReadDpi: true}, 0, "")

		pdf.SetFont("Arial", "B", 16)
		pdf.CellFormat(277, 10, "PingTick Analytical Report", "", 1, "C", false, 0, "")

		pdf.SetFont("Arial", "", 10)
		pdf.CellFormat(277, 10, fmt.Sprintf("Report Period: %s  |  Generated On: %s", dateStr, time.Now().Format("2006-01-02 15:04:05")), "", 1, "C", false, 0, "")
		pdf.Ln(5)

		pdf.SetFont("Arial", "B", 12)
		pdf.CellFormat(69, 10, fmt.Sprintf("Total Monitored: %d", total), "1", 0, "C", false, 0, "")
		pdf.SetTextColor(16, 185, 129)
		pdf.CellFormat(69, 10, fmt.Sprintf("Currently Up: %d", up), "1", 0, "C", false, 0, "")
		pdf.SetTextColor(239, 68, 68)
		pdf.CellFormat(69, 10, fmt.Sprintf("Currently Down: %d", down), "1", 0, "C", false, 0, "")
		pdf.SetTextColor(59, 130, 246)
		pdf.CellFormat(70, 10, fmt.Sprintf("Average Uptime: %.2f%%", avgUptime), "1", 1, "C", false, 0, "")

		pdf.Ln(10)
		pdf.SetTextColor(0, 0, 0)
		pdf.SetFont("Arial", "B", 10)

		cols := []string{"Name", "Type", "Category", "State", "Checks", "Uptime %", "Avg Lat(ms)"}
		widths := []float64{67, 30, 50, 40, 30, 30, 30}

		for i, col := range cols {
			pdf.CellFormat(widths[i], 10, col, "1", 0, "C", false, 0, "")
		}
		pdf.Ln(-1)

		pdf.SetFont("Arial", "", 9)
		for _, rp := range reports {
			pdf.CellFormat(widths[0], 10, rp.Name, "1", 0, "L", false, 0, "")
			pdf.CellFormat(widths[1], 10, rp.Type, "1", 0, "C", false, 0, "")
			pdf.CellFormat(widths[2], 10, rp.Category, "1", 0, "C", false, 0, "")
			pdf.CellFormat(widths[3], 10, rp.Current, "1", 0, "C", false, 0, "")
			pdf.CellFormat(widths[4], 10, fmt.Sprintf("%d", rp.Checks), "1", 0, "C", false, 0, "")
			pdf.CellFormat(widths[5], 10, fmt.Sprintf("%.1f%%", rp.UptimePct), "1", 0, "C", false, 0, "")
			pdf.CellFormat(widths[6], 10, fmt.Sprintf("%.1f", rp.AvgLatency), "1", 1, "C", false, 0, "")
		}

		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"pingtick-report-%s.pdf\"", dateStr))
		pdf.Output(w)
		return
	}

	// CSV Block
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"pingtick-report-%s.csv\"", dateStr))

	// Prepend Summary Layout for CSV / XLSX parser viewing
	fmt.Fprintf(w, "PINGTICK ANALYTICAL REPORT\n")
	fmt.Fprintf(w, "Report Period:,%s\n", dateStr)
	fmt.Fprintf(w, "Generated On:,%s\n\n", time.Now().Format("2006-01-02 15:04:05"))

	fmt.Fprintf(w, "--- EXECUTIVE SUMMARY ---\n")
	fmt.Fprintf(w, "Total Monitored:,%d\n", total)
	fmt.Fprintf(w, "Currently Up:,%d\n", up)
	fmt.Fprintf(w, "Currently Down:,%d\n", down)
	fmt.Fprintf(w, "Average Uptime:,%.2f%%\n\n", avgUptime)

	fmt.Fprintf(w, "--- TARGET METRICS ---\n")
	w.Write([]byte("Date,Target Name,Type,Category,Host,Current Status,Total Checks,Up Count,Down Count,Uptime %,Avg Latency(ms)\n"))
	for _, rp := range reports {
		fmt.Fprintf(w, "%s,%s,%s,%s,%s,%s,%d,%d,%d,%.1f,%.1f\n",
			rp.Date, rp.Name, rp.Type, rp.Category, rp.Host, rp.Current, rp.Checks, rp.Up, rp.Down, rp.UptimePct, rp.AvgLatency)
	}
}

func handleTestAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	triggerType := r.URL.Query().Get("type")
	msg := fmt.Sprintf("TEST MODE - Triggered from UI! The %s Alert engine is configured correctly.", triggerType)

	// Waits for the result so the dashboard can show why a test failed
	if err := sendTestNotification(triggerType, msg); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success": true}`))
}

func handleSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		settings := map[string]string{
			"alert_threshold":      getSetting("alert_threshold"),
			"telegram_enabled":     getSetting("telegram_enabled"),
			"telegram_bot_token":   getSetting("telegram_bot_token"),
			"telegram_chat_id":     getSetting("telegram_chat_id"),
			"email_enabled":        getSetting("email_enabled"),
			"smtp_host":            getSetting("smtp_host"),
			"smtp_port":            getSetting("smtp_port"),
			"smtp_user":            getSetting("smtp_user"),
			"smtp_pass":            getSetting("smtp_pass"),
			"alert_email_to":       getSetting("alert_email_to"),
			"daily_report_enabled": getSetting("daily_report_enabled"),
			"daily_report_time":    getSetting("daily_report_time"),
			"data_retention":       getSetting("data_retention"),
			"smtp_protocol":        getSetting("smtp_protocol"),
			"smtp_from":            getSetting("smtp_from"),
			"target_categories":    getSetting("target_categories"),
			"target_tags":          getSetting("target_tags"),
			"show_public_ip":       getSetting("show_public_ip"),
		}
		json.NewEncoder(w).Encode(settings)

	case http.MethodPost:
		var settings map[string]string
		if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		for k, v := range settings {
			if err := saveSetting(k, v); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.Write([]byte(`{"success": true}`))

	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func openBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}
	if err != nil {
		log.Printf("Could not open browser: %v", err)
	}
}

func handleSpeedTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		json.NewEncoder(w).Encode(speedTestForDisplay())
	} else if r.Method == http.MethodPost {
		if speedTestSnapshot().IsRunning {
			w.Write([]byte(`{"status": "already_running"}`))
			return
		}
		go RunSpeedTest()
		w.Write([]byte(`{"status": "started"}`))
	} else {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// Force Cache Bust 1

// Force Cache Bust 3
