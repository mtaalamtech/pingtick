package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unsafe"
)

const (
	wmPaint = 0x000F

	mbIconInformation = 0x40

	idInsightsExport  = 301
	insightsTimerID   = 1
	insightsRefreshMs = 5000
	msgInsightsLoaded = wmApp + 3

	stockWhiteBrush   = 0
	stockLtGrayBrush  = 1
	textAlignLeft     = 0
	textAlignRight    = 2
	bkModeTransparent = 1

	chartLineColor = 0x00F6823B // blue, as BGR
	chartFailColor = 0x004444EF // red, as BGR
	chartGridColor = 0x00E6E6E6
	chartTextColor = 0x00505050
)

var insightColumns = []struct {
	title string
	width int32
}{
	{"Time", 150},
	{"Status", 80},
	{"Result", 90},
	{"Reply IP", 130},
	{"TTL", 50},
}

type insightRow struct {
	when    time.Time
	latency int64
	failed  bool
	cells   [5]string
}

type insightsResult struct {
	targetID string
	rows     []insightRow
	err      error
}

// insightsUI is only touched from the window's own thread.
var insightsUI struct {
	hwnd, summary, list, exportBtn uintptr
	target                         *Target
	rows                           []insightRow // oldest first
	chart                          winRect
}

var (
	insightsPending atomic.Pointer[insightsResult]
	insightsWindow  atomic.Uintptr
)

type paintStruct struct {
	hdc       uintptr
	erase     int32
	paint     winRect
	restore   int32
	incUpdate int32
	reserved  [32]byte
}

func parseHistoryTime(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04:05 -0700 MST"} {
		if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func buildInsightRows(history []LatencyHistory, targetType string) []insightRow {
	rows := make([]insightRow, len(history))
	for i, h := range history {
		row := insightRow{latency: h.Latency, failed: h.Status != "online"}
		timeText := h.Timestamp
		if parsed, ok := parseHistoryTime(h.Timestamp); ok {
			row.when = parsed.Local()
			timeText = row.when.Format("2006-01-02 15:04:05")
		}
		status, result := "Succeeded", formatMeasurement(targetType, h.Latency)
		if row.failed {
			status, result = "Failed", "-"
		}
		replyIP := h.ReplyIP
		if replyIP == "" {
			replyIP = "-"
		}
		row.cells = [5]string{timeText, status, result, replyIP, formatTTL(h.TTL)}
		rows[i] = row
	}
	return rows
}

func summarizeInsights(rows []insightRow, targetType string) string {
	if len(rows) == 0 {
		return "No checks recorded yet."
	}
	var failed int
	var succeeded, total, lowest, highest int64
	for _, row := range rows {
		if row.failed {
			failed++
			continue
		}
		if succeeded == 0 || row.latency < lowest {
			lowest = row.latency
		}
		highest = max(highest, row.latency)
		total += row.latency
		succeeded++
	}
	summary := fmt.Sprintf("Last %d checks: %d failed (%.1f%%).", len(rows), failed, float64(failed)/float64(len(rows))*100)
	if succeeded > 0 {
		summary += fmt.Sprintf(" Average %s, lowest %s, highest %s.", formatMeasurement(targetType, total/succeeded),
			formatMeasurement(targetType, lowest), formatMeasurement(targetType, highest))
	}
	return summary
}

func safeFileName(name string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' {
			return r
		}
		return '-'
	}, name)
}

func showInsightsFromWindow() {
	target := selectedTarget()
	if target == nil {
		return
	}
	if insightsUI.hwnd == 0 && !createInsightsWindow() {
		showMonitorMessage("Could not open Insights.")
		return
	}

	insightsUI.target = target
	insightsUI.rows = nil
	applyInsightRows()
	procSetWindowText.Call(insightsUI.hwnd, wideArg(fmt.Sprintf("Insights - %s (%s)", target.Name, target.Host)))
	procSetWindowText.Call(insightsUI.summary, wideArg("Loading..."))
	procShowWindow.Call(insightsUI.hwnd, swShow)
	if minimized, _, _ := procIsIconic.Call(insightsUI.hwnd); minimized != 0 {
		procShowWindow.Call(insightsUI.hwnd, swRestore)
	}
	procSetForeground.Call(insightsUI.hwnd)
	loadInsights()
}

func createInsightsWindow() bool {
	hwnd, _, _ := procCreateWindowEx.Call(wsExControlParent, wideArg("PingTickInsights"), wideArg("Insights"), wsOverlappedWindow,
		cwUseDefault, cwUseDefault, uintptr(scaled(780)), uintptr(scaled(600)), 0, 0, monitorUI.instance, 0)
	if hwnd == 0 {
		return false
	}

	instance := monitorUI.instance
	insightsUI.hwnd = hwnd
	insightsUI.summary = createChild(hwnd, instance, 0, "Static", "", ssCenterImage, 0)
	insightsUI.exportBtn = createChild(hwnd, instance, 0, "Button", "Export CSV", wsTabStop, idInsightsExport)
	insightsUI.list = createVirtualList(hwnd, instance)
	for i, column := range insightColumns {
		addListColumn(insightsUI.list, i, column.title, column.width)
	}

	layoutInsightsWindow()
	procSetTimer.Call(hwnd, insightsTimerID, insightsRefreshMs, 0)
	insightsWindow.Store(hwnd)
	return true
}

func layoutInsightsWindow() {
	var client winRect
	procGetClientRect.Call(insightsUI.hwnd, uintptr(unsafe.Pointer(&client)))

	gap, rowHeight, buttonWidth := scaled(8), scaled(26), scaled(100)
	moveControl(insightsUI.summary, gap, gap, client.right-buttonWidth-gap*3, rowHeight)
	moveControl(insightsUI.exportBtn, client.right-buttonWidth-gap, gap, buttonWidth, rowHeight)

	chartTop := gap*2 + rowHeight
	chartHeight := max((client.bottom-chartTop)*45/100, scaled(120))
	insightsUI.chart = winRect{left: gap, top: chartTop, right: client.right - gap, bottom: chartTop + chartHeight}

	listTop := insightsUI.chart.bottom + gap
	moveControl(insightsUI.list, gap, listTop, client.right-gap*2, client.bottom-listTop-gap)
	procInvalidateRect.Call(insightsUI.hwnd, 0, 1)
}

// loadInsights reads history off the window thread so the UI never waits on the database.
func loadInsights() {
	id, targetType := insightsUI.target.ID, insightsUI.target.Type
	go func() {
		history, err := GetTargetHistory(id)
		insightsPending.Store(&insightsResult{targetID: id, rows: buildInsightRows(history, targetType), err: err})
		if hwnd := insightsWindow.Load(); hwnd != 0 {
			procPostMessage.Call(hwnd, msgInsightsLoaded, 0, 0)
		}
	}()
}

func applyInsightRows() {
	sendMessage(insightsUI.list, lvmSetItemCount, uintptr(len(insightsUI.rows)), lvsicfNoScroll)
	procInvalidateRect.Call(insightsUI.list, 0, 0)
	procInvalidateRect.Call(insightsUI.hwnd, uintptr(unsafe.Pointer(&insightsUI.chart)), 0)
}

func receiveInsights() {
	result := insightsPending.Swap(nil)
	if result == nil || insightsUI.target == nil || result.targetID != insightsUI.target.ID {
		return
	}
	if result.err != nil {
		log.Printf("Could not load history for %s: %v", result.targetID, result.err)
		procSetWindowText.Call(insightsUI.summary, wideArg("Could not load the history for this target."))
		return
	}
	insightsUI.rows = result.rows
	applyInsightRows()
	procSetWindowText.Call(insightsUI.summary, wideArg(summarizeInsights(result.rows, insightsUI.target.Type)))
}

// insightsExportPath chooses a file in Downloads. The Windows Save dialog is
// avoided because it permanently adds about 50 MB to the process.
func insightsExportPath(name string, now time.Time) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	folder := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(folder, fmt.Sprintf("pingtick-%s-%s.csv", safeFileName(name), now.Format("20060102-150405"))), nil
}

func exportInsightsCSV() {
	target := insightsUI.target
	if target == nil {
		return
	}

	var path string
	err := func() error {
		var err error
		if path, err = insightsExportPath(target.Name, time.Now()); err != nil {
			return err
		}
		history, err := GetFullTargetHistory(target.ID)
		if err != nil {
			return err
		}
		file, err := os.Create(path)
		if err != nil {
			return err
		}
		defer file.Close()
		return writeHistoryCSV(file, history)
	}()
	if err != nil {
		showWindowMessage(insightsUI.hwnd, "Could not export the history: "+err.Error(), mbOK|mbIconWarning)
		return
	}
	showWindowMessage(insightsUI.hwnd, "History saved to "+path, mbOK|mbIconInformation)
}

func drawChartText(dc uintptr, x, y int32, text string, align uintptr) {
	encoded := utf16.Encode([]rune(text))
	if len(encoded) == 0 {
		return
	}
	procSetTextAlign.Call(dc, align)
	procTextOut.Call(dc, uintptr(x), uintptr(y), uintptr(unsafe.Pointer(&encoded[0])), uintptr(len(encoded)))
}

// withPen selects a temporary pen, runs draw and releases the pen.
func withPen(dc uintptr, width int32, color uintptr, draw func()) {
	pen, _, _ := procCreatePen.Call(0, uintptr(width), color)
	previous, _, _ := procSelectObject.Call(dc, pen)
	draw()
	procSelectObject.Call(dc, previous)
	procDeleteObject.Call(pen)
}

func chartLine(dc uintptr, x1, y1, x2, y2 int32) {
	procMoveToEx.Call(dc, uintptr(x1), uintptr(y1), 0)
	procLineTo.Call(dc, uintptr(x2), uintptr(y2))
}

func paintInsightsChart(dc uintptr) {
	box := insightsUI.chart
	grayBrush, _, _ := procGetStockObject.Call(stockLtGrayBrush)
	whiteBrush, _, _ := procGetStockObject.Call(stockWhiteBrush)
	procFillRect.Call(dc, uintptr(unsafe.Pointer(&box)), grayBrush)
	inner := winRect{left: box.left + 1, top: box.top + 1, right: box.right - 1, bottom: box.bottom - 1}
	procFillRect.Call(dc, uintptr(unsafe.Pointer(&inner)), whiteBrush)

	previousFont, _, _ := procSelectObject.Call(dc, monitorUI.font)
	defer procSelectObject.Call(dc, previousFont)
	procSetBkMode.Call(dc, bkModeTransparent)
	procSetTextColor.Call(dc, chartTextColor)

	rows := insightsUI.rows
	if len(rows) == 0 {
		drawChartText(dc, box.left+scaled(12), box.top+scaled(12), "No checks recorded yet.", textAlignLeft)
		return
	}

	left, right := box.left+scaled(72), box.right-scaled(14)
	top, bottom := box.top+scaled(14), box.bottom-scaled(26)
	if right <= left || bottom <= top {
		return
	}

	var peak int64
	for _, row := range rows {
		peak = max(peak, row.latency)
	}
	peak = max(peak+peak%2, 2) // even, so the middle gridline is a whole number

	valueY := func(value int64) int32 { return bottom - int32(value*int64(bottom-top)/peak) }
	pointX := func(index int) int32 {
		if len(rows) == 1 {
			return (left + right) / 2
		}
		return left + int32(int64(index)*int64(right-left)/int64(len(rows)-1))
	}

	targetType := insightsUI.target.Type
	withPen(dc, 1, chartGridColor, func() {
		for _, value := range []int64{0, peak / 2, peak} {
			y := valueY(value)
			chartLine(dc, left, y, right, y)
			drawChartText(dc, left-scaled(6), y-scaled(8), formatMeasurement(targetType, value), textAlignRight)
		}
	})

	withPen(dc, max(scaled(2), 1), chartLineColor, func() {
		procMoveToEx.Call(dc, uintptr(pointX(0)), uintptr(valueY(rows[0].latency)), 0)
		for i := range rows {
			procLineTo.Call(dc, uintptr(pointX(i)), uintptr(valueY(rows[i].latency)))
		}
		if len(rows) == 1 {
			procLineTo.Call(dc, uintptr(pointX(0)+1), uintptr(valueY(rows[0].latency)))
		}
	})

	withPen(dc, max(scaled(2), 1), chartFailColor, func() {
		for i, row := range rows {
			if row.failed {
				chartLine(dc, pointX(i), bottom, pointX(i), bottom-scaled(10))
			}
		}
	})

	timeY := bottom + scaled(5)
	if !rows[0].when.IsZero() {
		drawChartText(dc, left, timeY, rows[0].when.Format("15:04:05"), textAlignLeft)
	}
	if last := rows[len(rows)-1].when; !last.IsZero() && len(rows) > 1 {
		drawChartText(dc, right, timeY, last.Format("15:04:05"), textAlignRight)
	}
}

func fillInsightCell(info *nmLvDispInfo) {
	item := &info.item
	if item.mask&lvifText == 0 || int(item.item) >= len(insightsUI.rows) || int(item.subItem) >= len(insightColumns) {
		return
	}
	// The table lists the newest check first.
	row := &insightsUI.rows[len(insightsUI.rows)-1-int(item.item)]
	copyCellText(item, row.cells[item.subItem])
}

func handleInsightsCustomDraw(draw *nmLvCustomDraw) uintptr {
	switch draw.drawStage {
	case cddsPrePaint:
		return cdrfNotifyItem
	case cddsItemPrePaint:
		if int(draw.itemSpec) >= len(insightsUI.rows) || listRowSelected(insightsUI.list, draw.itemSpec) {
			return 0
		}
		if insightsUI.rows[len(insightsUI.rows)-1-int(draw.itemSpec)].failed {
			draw.backColor = offlineRowColor
			return cdrfNewFont
		}
	}
	return 0
}

func closeInsightsWindow() {
	insightsWindow.Store(0)
	procDestroyWindow.Call(insightsUI.hwnd)
	insightsUI.hwnd, insightsUI.list = 0, 0
	insightsUI.target, insightsUI.rows = nil, nil
	insightsPending.Store(nil)
}

func insightsWindowProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmSize:
		if wParam != sizeMinimized && insightsUI.list != 0 {
			layoutInsightsWindow()
		}
		return 0

	case wmPaint:
		var paint paintStruct
		dc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
		paintInsightsChart(dc)
		procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
		return 0

	case wmClose:
		closeInsightsWindow()
		return 0

	case wmTimer:
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		minimized, _, _ := procIsIconic.Call(hwnd)
		if visible != 0 && minimized == 0 && insightsUI.target != nil {
			loadInsights()
		}
		return 0

	case msgInsightsLoaded:
		receiveInsights()
		return 0

	case wmCommand:
		if wParam&0xFFFF == idInsightsExport {
			exportInsightsCSV()
			return 0
		}

	case wmNotify:
		header := fromLParam[nmHdr](lParam)
		if header.hwndFrom == insightsUI.list {
			switch header.code {
			case lvnGetDispInfoW:
				fillInsightCell(fromLParam[nmLvDispInfo](lParam))
				return 0
			case nmCustomDraw:
				return handleInsightsCustomDraw(fromLParam[nmLvCustomDraw](lParam))
			}
		}
	}

	result, _, _ := procDefWindowProc.Call(hwnd, msg, wParam, lParam)
	return result
}
