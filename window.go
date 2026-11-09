package main

import (
	"fmt"
	"log"
	"net"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// The monitor window uses plain Win32 controls instead of an embedded browser,
// which is what keeps it small enough to leave open all day.

const (
	wsOverlappedWindow = 0x00CF0000
	wsChild            = 0x40000000
	wsVisible          = 0x10000000
	wsTabStop          = 0x00010000
	wsBorder           = 0x00800000
	wsExControlParent  = 0x00010000
	wsExClientEdge     = 0x00000200

	wmSize     = 0x0005
	wmClose    = 0x0010
	wmSetFont  = 0x0030
	wmNotify   = 0x004E
	wmCommand  = 0x0111
	wmTimer    = 0x0113
	wmApp      = 0x8000
	msgShow    = wmApp + 1
	msgRefresh = wmApp + 2

	swHide        = 0
	swShow        = 5
	swRestore     = 9
	sizeMinimized = 1

	bsDefPushButton = 1
	esAutoHScroll   = 0x0080
	emSetCueBanner  = 0x1501
	logPixelsX      = 88
	cwUseDefault    = 0x80000000
	colorBtnFace    = 15
	idcArrow        = 32512

	mbOK          = 0x00
	mbYesNo       = 0x04
	mbIconWarning = 0x30
	idYes         = 6

	idAdd      = 1 // IDOK, so Enter in the host box adds the target
	idDelete   = 101
	idWeb      = 102
	idEdit     = 103
	idClone    = 104
	idInsights = 105

	refreshTimerID = 1
	refreshMillis  = 2000

	iccListViewClasses = 0x01

	lvsReport       = 0x0001
	lvsSingleSel    = 0x0004
	lvsShowSelAlway = 0x0008
	lvsOwnerData    = 0x1000

	lvsExGridLines    = 0x00000001
	lvsExFullRowSel   = 0x00000020
	lvsExDoubleBuffer = 0x00010000

	lvmFirst                  = 0x1000
	lvmGetNextItem            = lvmFirst + 12
	lvmGetItemState           = lvmFirst + 44
	lvisSelected              = 0x0002
	lvmSetItemCount           = lvmFirst + 47
	lvmSetExtendedStyle       = lvmFirst + 54
	lvmInsertColumnW          = lvmFirst + 97
	lvniSelected              = 0x0002
	lvsicfNoScroll            = 0x0002
	lvcfWidth                 = 0x0002
	lvcfText                  = 0x0004
	lvifText                  = 0x0001
	lvnGetDispInfoW     int32 = -177
	nmCustomDraw        int32 = -12

	cddsPrePaint     = 0x00000001
	cddsItemPrePaint = 0x00010001
	cdrfNewFont      = 0x02
	cdrfNotifyItem   = 0x20

	offlineRowColor  = 0x00DCDCFF // light red, as BGR
	unknownRowColor  = 0x00E8E8E8 // light grey
	expiringRowColor = 0x00B3E8FF // light amber, as BGR
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	comctl32 = syscall.NewLazyDLL("comctl32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procRegisterClassEx     = user32.NewProc("RegisterClassExW")
	procCreateWindowEx      = user32.NewProc("CreateWindowExW")
	procDefWindowProc       = user32.NewProc("DefWindowProcW")
	procGetMessage          = user32.NewProc("GetMessageW")
	procIsDialogMessage     = user32.NewProc("IsDialogMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessage     = user32.NewProc("DispatchMessageW")
	procPostMessage         = user32.NewProc("PostMessageW")
	procSendMessage         = user32.NewProc("SendMessageW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForeground       = user32.NewProc("SetForegroundWindow")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procIsIconic            = user32.NewProc("IsIconic")
	procMoveWindow          = user32.NewProc("MoveWindow")
	procGetClientRect       = user32.NewProc("GetClientRect")
	procInvalidateRect      = user32.NewProc("InvalidateRect")
	procSetTimer            = user32.NewProc("SetTimer")
	procSetWindowText       = user32.NewProc("SetWindowTextW")
	procGetWindowText       = user32.NewProc("GetWindowTextW")
	procMessageBox          = user32.NewProc("MessageBoxW")
	procLoadCursor          = user32.NewProc("LoadCursorW")
	procLoadIcon            = user32.NewProc("LoadIconW")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")
	procGetDeviceCaps       = gdi32.NewProc("GetDeviceCaps")
	procCreateFont          = gdi32.NewProc("CreateFontW")
	procInitCommonCtrls     = comctl32.NewProc("InitCommonControlsEx")
	procGetModuleHandle     = kernel32.NewProc("GetModuleHandleW")
	procEnableWindow        = user32.NewProc("EnableWindow")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procGetWindowRect       = user32.NewProc("GetWindowRect")
	procSetFocus            = user32.NewProc("SetFocus")
	procBeginPaint          = user32.NewProc("BeginPaint")
	procEndPaint            = user32.NewProc("EndPaint")
	procFillRect            = user32.NewProc("FillRect")
	procGetWindowTextLength = user32.NewProc("GetWindowTextLengthW")
	procGetStockObject      = gdi32.NewProc("GetStockObject")
	procSelectObject        = gdi32.NewProc("SelectObject")
	procDeleteObject        = gdi32.NewProc("DeleteObject")
	procCreatePen           = gdi32.NewProc("CreatePen")
	procMoveToEx            = gdi32.NewProc("MoveToEx")
	procLineTo              = gdi32.NewProc("LineTo")
	procTextOut             = gdi32.NewProc("TextOutW")
	procSetTextAlign        = gdi32.NewProc("SetTextAlign")
	procSetTextColor        = gdi32.NewProc("SetTextColor")
	procSetBkMode           = gdi32.NewProc("SetBkMode")

	monitorWindow          atomic.Uintptr
	monitorWindowCallback  = syscall.NewCallback(monitorWindowProc)
	editorWindowCallback   = syscall.NewCallback(editorWindowProc)
	insightsWindowCallback = syscall.NewCallback(insightsWindowProc)
)

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

type winPoint struct{ x, y int32 }

type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      winPoint
	private uint32
}

type winRect struct{ left, top, right, bottom int32 }

type initCommonControls struct {
	size uint32
	icc  uint32
}

type nmHdr struct {
	hwndFrom uintptr
	idFrom   uintptr
	code     int32
}

type lvColumn struct {
	mask     uint32
	format   int32
	width    int32
	text     *uint16
	textMax  int32
	subItem  int32
	image    int32
	order    int32
	minWidth int32
	defWidth int32
	ideal    int32
}

type lvItem struct {
	mask      uint32
	item      int32
	subItem   int32
	state     uint32
	stateMask uint32
	text      *uint16
	textMax   int32
	image     int32
	lParam    uintptr
	indent    int32
	groupID   int32
	columns   uint32
	puColumns uintptr
	colFormat uintptr
	group     int32
}

type nmLvDispInfo struct {
	hdr  nmHdr
	item lvItem
}

type nmLvCustomDraw struct {
	hdr       nmHdr
	drawStage uint32
	hdc       uintptr
	rc        winRect
	itemSpec  uintptr
	itemState uint32
	itemParam uintptr
	textColor uint32
	backColor uint32
}

type monitorColumn struct {
	title string
	width int32
	value func(*Target) string
}

var monitorColumns = []monitorColumn{
	{"Name", 170, func(t *Target) string { return t.Name }},
	{"Host", 170, func(t *Target) string { return t.Host }},
	{"Status", 75, func(t *Target) string { return strings.ToUpper(t.Status) }},
	{"Reply IP", 120, func(t *Target) string { return t.ReplyIP }},
	{"Result", 80, formatResult},
	{"Min", 55, func(t *Target) string { return formatBound(t, t.MinLatency) }},
	{"Max", 55, func(t *Target) string { return formatBound(t, t.MaxLatency) }},
	{"Sent", 55, func(t *Target) string { return fmt.Sprint(t.TotalSent) }},
	{"Failed", 55, func(t *Target) string { return fmt.Sprint(t.FailedCount) }},
	{"% Failed", 65, formatFailedPercent},
	{"TTL", 40, func(t *Target) string { return formatTTL(t.TTL) }},
	{"Last Check", 80, formatLastCheck},
	{"Type", 75, func(t *Target) string { return t.Type }},
}

// monitorUI is only touched from the window's own thread.
var monitorUI struct {
	hwnd, list, hostBox, addBtn, deleteBtn, webBtn, statusBar uintptr
	editBtn, cloneBtn, insightsBtn                            uintptr
	font, instance, icon, cursor                              uintptr
	dpi                                                       int32
	rows                                                      []*Target
	statusText                                                string
}

func registerWindowClass(name string, proc uintptr) error {
	class := wndClassEx{
		wndProc:    proc,
		instance:   monitorUI.instance,
		icon:       monitorUI.icon,
		iconSm:     monitorUI.icon,
		cursor:     monitorUI.cursor,
		background: colorBtnFace + 1,
		className:  wideString(name),
	}
	class.size = uint32(unsafe.Sizeof(class))
	if atom, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		return err
	}
	return nil
}

func formatResult(t *Target) string {
	if t.Status != "online" {
		return "-"
	}
	return formatMeasurement(t.Type, t.Latency)
}

func formatMeasurement(targetType string, value int64) string {
	switch targetType {
	case "ssl":
		return fmt.Sprintf("%d days", value)
	case "bandwidth", "wmi":
		return fmt.Sprintf("%d Mbps", value)
	case "snmp":
		return fmt.Sprint(value)
	}
	return fmt.Sprintf("%d ms", value)
}

func formatBound(t *Target, value int64) string {
	if (t.Type != "ping" && t.Type != "port") || t.SucceedCount == 0 {
		return "-"
	}
	return fmt.Sprint(value)
}

func formatFailedPercent(t *Target) string {
	if t.TotalSent == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", float64(t.FailedCount)/float64(t.TotalSent)*100)
}

func formatTTL(ttl int) string {
	if ttl <= 0 {
		return "-"
	}
	return fmt.Sprint(ttl)
}

func formatLastCheck(t *Target) string {
	if t.LastCheck.IsZero() {
		return "Pending"
	}
	return t.LastCheck.Format("15:04:05")
}

// targetFromHostInput treats host:port as a TCP check and anything else as ping.
func targetFromHostInput(input string) (*Target, error) {
	host := strings.TrimSpace(input)
	if host == "" || strings.ContainsAny(host, " \t") {
		return nil, fmt.Errorf("enter one IP address or hostname, for example 192.168.1.1 or server:3306")
	}
	targetType := "ping"
	if _, _, err := net.SplitHostPort(host); err == nil {
		targetType = "port"
	}
	return &Target{Name: host, Host: host, Type: targetType, Interval: 10, Category: "General"}, nil
}

func wideString(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		p, _ = syscall.UTF16PtrFromString("")
	}
	return p
}

func wideArg(s string) uintptr { return uintptr(unsafe.Pointer(wideString(s))) }

// fromLParam reads a pointer Windows passed as an integer.
func fromLParam[T any](p uintptr) *T { return *(**T)(unsafe.Pointer(&p)) }

func sendMessage(hwnd, msg, wParam, lParam uintptr) uintptr {
	r, _, _ := procSendMessage.Call(hwnd, msg, wParam, lParam)
	return r
}

func scaled(v int32) int32 { return v * monitorUI.dpi / 96 }

// showMonitorWindow can be called from any goroutine.
func showMonitorWindow() {
	if hwnd := monitorWindow.Load(); hwnd != 0 {
		procPostMessage.Call(hwnd, msgShow, 0, 0)
	}
}

func requestMonitorRefresh() {
	if hwnd := monitorWindow.Load(); hwnd != 0 {
		procPostMessage.Call(hwnd, msgRefresh, 0, 0)
	}
}

// runMonitorWindow owns the window and blocks for the life of the process.
func runMonitorWindow() {
	runtime.LockOSThread()

	init := initCommonControls{icc: iccListViewClasses}
	init.size = uint32(unsafe.Sizeof(init))
	procInitCommonCtrls.Call(uintptr(unsafe.Pointer(&init)))

	instance, _, _ := procGetModuleHandle.Call(0)
	icon, _, _ := procLoadIcon.Call(instance, wideArg("APP"))
	cursor, _, _ := procLoadCursor.Call(0, idcArrow)
	monitorUI.instance, monitorUI.icon, monitorUI.cursor = instance, icon, cursor

	for name, callback := range map[string]uintptr{
		"PingTickMonitor":  monitorWindowCallback,
		"PingTickEditor":   editorWindowCallback,
		"PingTickInsights": insightsWindowCallback,
	} {
		if err := registerWindowClass(name, callback); err != nil {
			log.Printf("Could not register window class %s: %v", name, err)
			return
		}
	}

	screen, _, _ := procGetDC.Call(0)
	dpi, _, _ := procGetDeviceCaps.Call(screen, logPixelsX)
	procReleaseDC.Call(0, screen)
	monitorUI.dpi = int32(dpi)
	if monitorUI.dpi == 0 {
		monitorUI.dpi = 96
	}

	hwnd, _, err := procCreateWindowEx.Call(wsExControlParent, wideArg("PingTickMonitor"),
		wideArg("PingTick Monitor"), wsOverlappedWindow, cwUseDefault, cwUseDefault,
		uintptr(scaled(1120)), uintptr(scaled(540)), 0, 0, instance, 0)
	if hwnd == 0 {
		log.Printf("Could not create monitor window: %v", err)
		return
	}
	monitorUI.hwnd = hwnd
	createMonitorControls(instance)
	layoutMonitorWindow()
	refreshMonitorRows()

	procSetTimer.Call(hwnd, refreshTimerID, refreshMillis, 0)
	procShowWindow.Call(hwnd, swShow)
	monitorWindow.Store(hwnd)

	var msg winMsg
	for {
		result, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) <= 0 {
			return
		}
		// IsDialogMessage provides Tab navigation and Enter for the default button.
		if handled, _, _ := procIsDialogMessage.Call(hwnd, uintptr(unsafe.Pointer(&msg))); handled != 0 {
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func createChild(parent, instance, exStyle uintptr, class, text string, style, id uintptr) uintptr {
	hwnd, _, _ := procCreateWindowEx.Call(exStyle, wideArg(class), wideArg(text), wsChild|wsVisible|style,
		0, 0, 0, 0, parent, id, instance, 0)
	sendMessage(hwnd, wmSetFont, monitorUI.font, 1)
	return hwnd
}

func createControl(instance uintptr, exStyle uintptr, class, text string, style, id uintptr) uintptr {
	return createChild(monitorUI.hwnd, instance, exStyle, class, text, style, id)
}

func createVirtualList(parent, instance uintptr) uintptr {
	list := createChild(parent, instance, wsExClientEdge, "SysListView32", "",
		wsTabStop|wsBorder|lvsReport|lvsSingleSel|lvsShowSelAlway|lvsOwnerData, 0)
	sendMessage(list, lvmSetExtendedStyle, 0, lvsExFullRowSel|lvsExGridLines|lvsExDoubleBuffer)
	return list
}

func addListColumn(list uintptr, index int, title string, width int32) {
	column := lvColumn{mask: lvcfText | lvcfWidth, width: scaled(width), text: wideString(title)}
	sendMessage(list, lvmInsertColumnW, uintptr(index), uintptr(unsafe.Pointer(&column)))
}

func createMonitorControls(instance uintptr) {
	const fontWeightNormal = 400
	font, _, _ := procCreateFont.Call(uintptr(int64(-scaled(12))), 0, 0, 0, fontWeightNormal, 0, 0, 0, 0, 0, 0, 0, 0, wideArg("Segoe UI"))
	monitorUI.font = font

	monitorUI.hostBox = createControl(instance, wsExClientEdge, "Edit", "", wsTabStop|esAutoHScroll, 0)
	sendMessage(monitorUI.hostBox, emSetCueBanner, 1, wideArg("IP, hostname or host:port"))
	monitorUI.addBtn = createControl(instance, 0, "Button", "Add", wsTabStop|bsDefPushButton, idAdd)
	monitorUI.editBtn = createControl(instance, 0, "Button", "Edit", wsTabStop, idEdit)
	monitorUI.cloneBtn = createControl(instance, 0, "Button", "Clone", wsTabStop, idClone)
	monitorUI.insightsBtn = createControl(instance, 0, "Button", "Insights", wsTabStop, idInsights)
	monitorUI.deleteBtn = createControl(instance, 0, "Button", "Delete Selected", wsTabStop, idDelete)
	monitorUI.webBtn = createControl(instance, 0, "Button", "Web Dashboard", wsTabStop, idWeb)
	monitorUI.statusBar = createControl(instance, 0, "Static", "", 0, 0)

	monitorUI.list = createVirtualList(monitorUI.hwnd, instance)
	for i := range monitorColumns {
		addListColumn(monitorUI.list, i, monitorColumns[i].title, monitorColumns[i].width)
	}
}

func moveControl(hwnd uintptr, x, y, width, height int32) {
	procMoveWindow.Call(hwnd, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 1)
}

func layoutMonitorWindow() {
	var client winRect
	procGetClientRect.Call(monitorUI.hwnd, uintptr(unsafe.Pointer(&client)))

	gap, rowHeight, statusHeight := scaled(8), scaled(26), scaled(20)
	x := gap
	place := func(hwnd uintptr, width int32) {
		moveControl(hwnd, x, gap, width, rowHeight)
		x += width + gap
	}
	place(monitorUI.hostBox, scaled(240))
	place(monitorUI.addBtn, scaled(64))
	place(monitorUI.editBtn, scaled(64))
	place(monitorUI.cloneBtn, scaled(64))
	place(monitorUI.insightsBtn, scaled(76))
	place(monitorUI.deleteBtn, scaled(116))
	place(monitorUI.webBtn, scaled(116))

	listTop := gap*2 + rowHeight
	moveControl(monitorUI.list, gap, listTop, client.right-gap*2, client.bottom-listTop-statusHeight-gap*2)
	moveControl(monitorUI.statusBar, gap, client.bottom-statusHeight-gap/2, client.right-gap*2, statusHeight)
}

func refreshMonitorRows() {
	monitorUI.rows = GetTargets()
	sendMessage(monitorUI.list, lvmSetItemCount, uintptr(len(monitorUI.rows)), lvsicfNoScroll)
	procInvalidateRect.Call(monitorUI.list, 0, 0)

	online, offline := 0, 0
	for _, t := range monitorUI.rows {
		switch t.Status {
		case "online":
			online++
		case "offline":
			offline++
		}
	}
	text := fmt.Sprintf("%d targets, %d online, %d offline. Closing this window keeps monitoring in the system tray.",
		len(monitorUI.rows), online, offline)
	if text != monitorUI.statusText {
		monitorUI.statusText = text
		procSetWindowText.Call(monitorUI.statusBar, wideArg(text))
	}
}

func showWindowMessage(owner uintptr, text string, flags uintptr) {
	procMessageBox.Call(owner, wideArg(text), wideArg("PingTick"), flags)
}

func showMonitorMessage(text string) {
	showWindowMessage(monitorUI.hwnd, text, mbOK|mbIconWarning)
}

// selectedTarget returns the selected row, telling the user if there is none.
func selectedTarget() *Target {
	selected := int32(sendMessage(monitorUI.list, lvmGetNextItem, ^uintptr(0), lvniSelected))
	if selected < 0 || int(selected) >= len(monitorUI.rows) {
		showMonitorMessage("Select a target first.")
		return nil
	}
	return monitorUI.rows[selected]
}

func addTargetFromWindow() {
	var buffer [256]uint16
	procGetWindowText.Call(monitorUI.hostBox, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))

	target, err := targetFromHostInput(syscall.UTF16ToString(buffer[:]))
	if err != nil {
		showMonitorMessage(err.Error())
		return
	}
	for _, existing := range monitorUI.rows {
		if existing.Host == target.Host && existing.Type == target.Type {
			showMonitorMessage(target.Host + " is already being monitored.")
			return
		}
	}

	procSetWindowText.Call(monitorUI.hostBox, wideArg(""))
	go func() {
		if err := AddTarget(target); err != nil {
			log.Printf("Could not add target %s: %v", target.Host, err)
		}
		requestMonitorRefresh()
	}()
}

func deleteSelectedTarget() {
	target := selectedTarget()
	if target == nil {
		return
	}

	question := fmt.Sprintf("Stop monitoring %s? Its history will be deleted.", target.Name)
	answer, _, _ := procMessageBox.Call(monitorUI.hwnd, wideArg(question), wideArg("PingTick"), mbYesNo|mbIconWarning)
	if answer != idYes {
		return
	}

	id := target.ID
	go func() {
		if err := DeleteTarget(id); err != nil {
			log.Printf("Could not delete target %s: %v", id, err)
		}
		requestMonitorRefresh()
	}()
}

func fillListCell(info *nmLvDispInfo) {
	item := &info.item
	if item.mask&lvifText == 0 || int(item.item) >= len(monitorUI.rows) || int(item.subItem) >= len(monitorColumns) {
		return
	}
	copyCellText(item, monitorColumns[item.subItem].value(monitorUI.rows[item.item]))
}

func copyCellText(item *lvItem, value string) {
	if item.textMax <= 0 {
		return
	}
	text, err := syscall.UTF16FromString(value)
	if err != nil {
		return
	}
	size := min(len(text), int(item.textMax))
	buffer := unsafe.Slice(item.text, size)
	copy(buffer, text[:size])
	buffer[size-1] = 0
}

// listRowSelected asks the list directly, because the item state in custom
// draw reports every row as selected when LVS_SHOWSELALWAYS is set.
func listRowSelected(list, row uintptr) bool {
	return sendMessage(list, lvmGetItemState, row, lvisSelected)&lvisSelected != 0
}

func handleListCustomDraw(draw *nmLvCustomDraw) uintptr {
	switch draw.drawStage {
	case cddsPrePaint:
		return cdrfNotifyItem
	case cddsItemPrePaint:
		if int(draw.itemSpec) >= len(monitorUI.rows) || listRowSelected(monitorUI.list, draw.itemSpec) {
			return 0
		}
		row := monitorUI.rows[draw.itemSpec]
		switch {
		case row.Status == "offline":
			draw.backColor = offlineRowColor
		case row.Status == "unknown":
			draw.backColor = unknownRowColor
		case certExpiringSoon(row):
			draw.backColor = expiringRowColor
		default:
			return 0
		}
		return cdrfNewFont
	}
	return 0
}

func monitorWindowProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmSize:
		if wParam != sizeMinimized && monitorUI.list != 0 {
			layoutMonitorWindow()
		}
		return 0

	case wmClose:
		procShowWindow.Call(hwnd, swHide)
		return 0

	case wmTimer:
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		minimized, _, _ := procIsIconic.Call(hwnd)
		if visible != 0 && minimized == 0 {
			refreshMonitorRows()
		}
		return 0

	case msgRefresh:
		refreshMonitorRows()
		return 0

	case msgShow:
		procShowWindow.Call(hwnd, swShow)
		if minimized, _, _ := procIsIconic.Call(hwnd); minimized != 0 {
			procShowWindow.Call(hwnd, swRestore)
		}
		procSetForeground.Call(hwnd)
		refreshMonitorRows()
		return 0

	case wmCommand:
		switch wParam & 0xFFFF {
		case idAdd:
			addTargetFromWindow()
		case idEdit:
			editTargetFromWindow()
		case idClone:
			cloneTargetFromWindow()
		case idInsights:
			showInsightsFromWindow()
		case idDelete:
			deleteSelectedTarget()
		case idWeb:
			openBrowser(activeURL)
		}
		return 0

	case wmNotify:
		header := fromLParam[nmHdr](lParam)
		if header.hwndFrom == monitorUI.list {
			switch header.code {
			case lvnGetDispInfoW:
				fillListCell(fromLParam[nmLvDispInfo](lParam))
				return 0
			case nmCustomDraw:
				return handleListCustomDraw(fromLParam[nmLvCustomDraw](lParam))
			}
		}
	}

	result, _, _ := procDefWindowProc.Call(hwnd, msg, wParam, lParam)
	return result
}
