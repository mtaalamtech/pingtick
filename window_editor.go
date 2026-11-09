package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const (
	wsCaption         = 0x00C00000
	wsSysMenu         = 0x00080000
	wsVScroll         = 0x00200000
	wsExDlgModalFrame = 0x00000001

	esPassword      = 0x0020
	esNumber        = 0x2000
	ssCenterImage   = 0x0200
	cbsDropDown     = 0x0002
	cbsDropDownList = 0x0003
	cbAddString     = 0x0143
	cbGetCurSel     = 0x0147
	cbSetCurSel     = 0x014E
	cbnSelChange    = 1

	idOK         = 1
	idCancel     = 2
	idEditorType = 201
)

var targetTypeChoices = []struct{ value, label string }{
	{"ping", "Raw ICMP Ping Check"},
	{"port", "TCP Port Handshake Check"},
	{"ssl", "SSL Cert Expiry Monitor"},
	{"dns", "DNS Resolution Benchmark"},
	{"snmp", "SNMP Custom OID Monitor"},
	{"bandwidth", "SNMP Bandwidth Tracker (Mbps)"},
	{"wmi", "WMI Bandwidth Tracker (Windows Remote)"},
}

// targetForm holds the text of the edit dialog before it has been validated.
type targetForm struct {
	Name, Type, Host, Interval, Category, Tag string
	SnmpCommunity, SnmpOID                    string
	WmiUser, WmiPass, WmiNamespace, WmiQuery  string
}

func targetFormGroup(targetType string) string {
	switch targetType {
	case "snmp", "bandwidth":
		return "snmp"
	case "wmi":
		return "wmi"
	}
	return ""
}

func targetFromForm(form targetForm) (*Target, error) {
	target := &Target{
		Name:     strings.TrimSpace(form.Name),
		Type:     form.Type,
		Host:     strings.TrimSpace(form.Host),
		Category: strings.TrimSpace(form.Category),
		Tag:      strings.TrimSpace(form.Tag),
	}
	if target.Name == "" {
		return nil, fmt.Errorf("enter a name for this target")
	}
	if !isKnownTargetType(target.Type) {
		return nil, fmt.Errorf("choose a check type")
	}
	if target.Host == "" || strings.ContainsAny(target.Host, " \t") {
		return nil, fmt.Errorf("enter one IP address or hostname")
	}
	if target.Type == "port" {
		host, port, err := net.SplitHostPort(target.Host)
		number, numberErr := strconv.Atoi(port)
		if err != nil || host == "" || numberErr != nil || number < 1 || number > 65535 {
			return nil, fmt.Errorf("a TCP port check needs host:port, for example server:3306")
		}
	}

	interval, err := strconv.Atoi(strings.TrimSpace(form.Interval))
	if err != nil || interval < 1 {
		return nil, fmt.Errorf("the interval must be a whole number of seconds, 1 or more")
	}
	target.Interval = interval

	if target.Category == "" {
		target.Category = "General"
	}
	switch targetFormGroup(target.Type) {
	case "snmp":
		target.SnmpCommunity = strings.TrimSpace(form.SnmpCommunity)
		target.SnmpOID = strings.TrimSpace(form.SnmpOID)
	case "wmi":
		target.WmiUser = strings.TrimSpace(form.WmiUser)
		target.WmiPass = form.WmiPass
		target.WmiNamespace = strings.TrimSpace(form.WmiNamespace)
		target.WmiQuery = strings.TrimSpace(form.WmiQuery)
	}
	return target, nil
}

type editorField struct {
	label, input uintptr
	group        string
	combo        bool
}

// editorUI is only touched from the window's own thread.
var editorUI struct {
	hwnd, typeBox, hostLabel, okBtn, cancelBtn uintptr
	name, host, interval, category, tag        uintptr
	snmpCommunity, snmpOID                     uintptr
	wmiUser, wmiPass, wmiNamespace, wmiQuery   uintptr
	fields                                     []editorField
	targetID                                   string
	done                                       bool
}

func windowText(hwnd uintptr) string {
	length, _, _ := procGetWindowTextLength.Call(hwnd)
	buffer := make([]uint16, length+1)
	procGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	return syscall.UTF16ToString(buffer)
}

func settingChoices(key, fallback string) []string {
	value := getSetting(key)
	if strings.TrimSpace(value) == "" {
		value = fallback
	}
	var choices []string
	for _, choice := range strings.Split(value, ",") {
		if choice = strings.TrimSpace(choice); choice != "" {
			choices = append(choices, choice)
		}
	}
	return choices
}

func selectedEditorType() string {
	index := int(int32(sendMessage(editorUI.typeBox, cbGetCurSel, 0, 0)))
	if index < 0 || index >= len(targetTypeChoices) {
		return ""
	}
	return targetTypeChoices[index].value
}

func editTargetFromWindow() {
	if target := selectedTarget(); target != nil {
		runTargetEditor("Edit Target", target, target.ID)
	}
}

func cloneTargetFromWindow() {
	if target := selectedTarget(); target != nil {
		clone := *target
		clone.Name = "Clone " + target.Name
		runTargetEditor("Clone Target", &clone, "")
	}
}

func addEditorField(label, class string, exStyle, style uintptr, value, group string) uintptr {
	instance := monitorUI.instance
	labelHwnd := createChild(editorUI.hwnd, instance, 0, "Static", label, ssCenterImage, 0)
	isCombo := class == "ComboBox"
	id := uintptr(0)
	if style&cbsDropDownList == cbsDropDownList && isCombo {
		id = idEditorType
	}
	input := createChild(editorUI.hwnd, instance, exStyle, class, value, wsTabStop|style, id)
	editorUI.fields = append(editorUI.fields, editorField{label: labelHwnd, input: input, group: group, combo: isCombo})
	return input
}

func addEditorChoices(box uintptr, choices []string, current string) {
	for _, choice := range choices {
		sendMessage(box, cbAddString, 0, wideArg(choice))
	}
	procSetWindowText.Call(box, wideArg(current))
}

// runTargetEditor shows the dialog and returns once it has been closed. An
// empty targetID creates a new target.
func runTargetEditor(title string, source *Target, targetID string) {
	owner := monitorUI.hwnd
	hwnd, _, _ := procCreateWindowEx.Call(wsExControlParent|wsExDlgModalFrame, wideArg("PingTickEditor"), wideArg(title),
		wsCaption|wsSysMenu, cwUseDefault, cwUseDefault, uintptr(scaled(480)), uintptr(scaled(420)), owner, 0, monitorUI.instance, 0)
	if hwnd == 0 {
		showMonitorMessage("Could not open the editor.")
		return
	}

	editorUI.hwnd = hwnd
	editorUI.fields = nil
	editorUI.targetID = targetID
	editorUI.done = false

	editBox := func(label string, style uintptr, value, group string) uintptr {
		return addEditorField(label, "Edit", wsExClientEdge, esAutoHScroll|style, value, group)
	}
	editorUI.name = editBox("Name", 0, source.Name, "")
	editorUI.typeBox = addEditorField("Check type", "ComboBox", 0, wsVScroll|cbsDropDownList, "", "")
	editorUI.host = editBox("Host", 0, source.Host, "")
	editorUI.hostLabel = editorUI.fields[len(editorUI.fields)-1].label
	editorUI.interval = editBox("Interval (seconds)", esNumber, strconv.Itoa(source.Interval), "")
	editorUI.category = addEditorField("Category", "ComboBox", 0, wsVScroll|cbsDropDown, "", "")
	editorUI.tag = addEditorField("Tag", "ComboBox", 0, wsVScroll|cbsDropDown, "", "")
	editorUI.snmpCommunity = editBox("SNMP community", 0, source.SnmpCommunity, "snmp")
	editorUI.snmpOID = editBox("SNMP OID", 0, source.SnmpOID, "snmp")
	editorUI.wmiUser = editBox("WMI user", 0, source.WmiUser, "wmi")
	editorUI.wmiPass = editBox("WMI password", esPassword, source.WmiPass, "wmi")
	editorUI.wmiNamespace = editBox("WMI namespace", 0, source.WmiNamespace, "wmi")
	editorUI.wmiQuery = editBox("WMI query", 0, source.WmiQuery, "wmi")

	for i, choice := range targetTypeChoices {
		sendMessage(editorUI.typeBox, cbAddString, 0, wideArg(choice.label))
		if choice.value == source.Type {
			sendMessage(editorUI.typeBox, cbSetCurSel, uintptr(i), 0)
		}
	}
	addEditorChoices(editorUI.category, settingChoices("target_categories", "Infrastructure, External, Database, Services"), source.Category)
	addEditorChoices(editorUI.tag, settingChoices("target_tags", "Production, Staging, QA, Development"), source.Tag)

	saveLabel := "Save"
	if targetID == "" {
		saveLabel = "Create"
	}
	editorUI.okBtn = createChild(hwnd, monitorUI.instance, 0, "Button", saveLabel, wsTabStop|bsDefPushButton, idOK)
	editorUI.cancelBtn = createChild(hwnd, monitorUI.instance, 0, "Button", "Cancel", wsTabStop, idCancel)

	layoutTargetEditor()
	centerOverOwner(hwnd, owner)

	procEnableWindow.Call(owner, 0)
	procShowWindow.Call(hwnd, swShow)
	procSetFocus.Call(editorUI.name)

	var msg winMsg
	for !editorUI.done {
		result, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) <= 0 {
			break
		}
		if handled, _, _ := procIsDialogMessage.Call(hwnd, uintptr(unsafe.Pointer(&msg))); handled != 0 {
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}

	// The owner must be enabled first or Windows activates another application.
	procEnableWindow.Call(owner, 1)
	procDestroyWindow.Call(hwnd)
	editorUI.hwnd = 0
	refreshMonitorRows()
}

func centerOverOwner(hwnd, owner uintptr) {
	var box, ownerBox winRect
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&box)))
	procGetWindowRect.Call(owner, uintptr(unsafe.Pointer(&ownerBox)))
	width, height := box.right-box.left, box.bottom-box.top
	moveControl(hwnd, ownerBox.left+(ownerBox.right-ownerBox.left-width)/2, ownerBox.top+(ownerBox.bottom-ownerBox.top-height)/2, width, height)
}

// layoutTargetEditor shows only the fields for the chosen check type and
// resizes the dialog to fit them.
func layoutTargetEditor() {
	targetType := selectedEditorType()
	group := targetFormGroup(targetType)

	hostLabel := "Host"
	if targetType == "port" {
		hostLabel = "Host:Port"
	}
	procSetWindowText.Call(editorUI.hostLabel, wideArg(hostLabel))

	gap, rowHeight, labelWidth, inputWidth := scaled(10), scaled(24), scaled(125), scaled(310)
	inputX := gap*2 + labelWidth
	y := gap + scaled(2)
	for _, field := range editorUI.fields {
		visible := uintptr(swHide)
		if field.group == "" || field.group == group {
			visible = swShow
			inputHeight := rowHeight
			if field.combo {
				inputHeight = scaled(220) // includes the drop-down list
			}
			moveControl(field.label, gap, y, labelWidth, rowHeight)
			moveControl(field.input, inputX, y, inputWidth, inputHeight)
			y += rowHeight + gap
		}
		procShowWindow.Call(field.label, visible)
		procShowWindow.Call(field.input, visible)
	}

	buttonWidth, buttonHeight := scaled(85), scaled(28)
	clientWidth := inputX + inputWidth + gap
	y += scaled(4)
	moveControl(editorUI.okBtn, clientWidth-gap*2-buttonWidth*2, y, buttonWidth, buttonHeight)
	moveControl(editorUI.cancelBtn, clientWidth-gap-buttonWidth, y, buttonWidth, buttonHeight)
	clientHeight := y + buttonHeight + gap

	var box, client winRect
	procGetWindowRect.Call(editorUI.hwnd, uintptr(unsafe.Pointer(&box)))
	procGetClientRect.Call(editorUI.hwnd, uintptr(unsafe.Pointer(&client)))
	frameWidth := box.right - box.left - client.right
	frameHeight := box.bottom - box.top - client.bottom
	moveControl(editorUI.hwnd, box.left, box.top, clientWidth+frameWidth, clientHeight+frameHeight)
	procInvalidateRect.Call(editorUI.hwnd, 0, 1)
}

func saveTargetEditor() {
	target, err := targetFromForm(targetForm{
		Name:          windowText(editorUI.name),
		Type:          selectedEditorType(),
		Host:          windowText(editorUI.host),
		Interval:      windowText(editorUI.interval),
		Category:      windowText(editorUI.category),
		Tag:           windowText(editorUI.tag),
		SnmpCommunity: windowText(editorUI.snmpCommunity),
		SnmpOID:       windowText(editorUI.snmpOID),
		WmiUser:       windowText(editorUI.wmiUser),
		WmiPass:       windowText(editorUI.wmiPass),
		WmiNamespace:  windowText(editorUI.wmiNamespace),
		WmiQuery:      windowText(editorUI.wmiQuery),
	})
	if err == nil {
		if editorUI.targetID == "" {
			err = AddTarget(target)
		} else {
			target.ID = editorUI.targetID
			err = UpdateTarget(target)
		}
	}
	if err != nil {
		showWindowMessage(editorUI.hwnd, err.Error(), mbOK|mbIconWarning)
		return
	}
	editorUI.done = true
}

func editorWindowProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmClose:
		editorUI.done = true
		return 0

	case wmCommand:
		switch wParam & 0xFFFF {
		case idOK:
			saveTargetEditor()
			return 0
		case idCancel:
			editorUI.done = true
			return 0
		case idEditorType:
			if (wParam>>16)&0xFFFF == cbnSelChange {
				layoutTargetEditor()
			}
			return 0
		}
	}

	result, _, _ := procDefWindowProc.Call(hwnd, msg, wParam, lParam)
	return result
}
