package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/go-ping/ping"
	"github.com/google/uuid"
	"github.com/gosnmp/gosnmp"
	"github.com/yusufpapurcu/wmi"
)

// Target represents a monitoring target
type Target struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Host         string    `json:"host"`
	Type         string    `json:"type"`     // ping, ssl, port, dns, snmp, bandwidth, wmi
	Interval     int       `json:"interval"` // in seconds
	Tag          string    `json:"tag"`      // group or tag name
	Category     string    `json:"category"` // category classification
	Status       string    `json:"status"`   // online, offline, unknown
	Latency      int64     `json:"latency"`  // in ms (or days left for SSL)
	LastCheck    time.Time `json:"last_check"`
	ReplyIP      string    `json:"reply_ip"`
	TTL          int       `json:"ttl"`
	SucceedCount int       `json:"succeed_count"`
	FailedCount  int       `json:"failed_count"`
	TotalSent    int       `json:"total_sent"`
	MinLatency   int64     `json:"min_latency"`
	MaxLatency   int64     `json:"max_latency"`

	SnmpCommunity string `json:"snmp_community"`
	SnmpOID       string `json:"snmp_oid"`

	WmiUser      string `json:"wmi_user"`
	WmiPass      string `json:"wmi_pass"`
	WmiNamespace string `json:"wmi_namespace"`
	WmiQuery     string `json:"wmi_query"`

	prevSnmpValue uint64    `json:"-"`
	prevSnmpTime  time.Time `json:"-"`
	checking      bool      // a check is in flight; guarded by mutex
}

type LatencyHistory struct {
	TargetID   string `json:"target_id,omitempty"`
	TargetName string `json:"target_name,omitempty"`
	Timestamp  string `json:"timestamp"`
	Latency    int64  `json:"latency"`
	Status     string `json:"status"`
	ReplyIP    string `json:"reply_ip"`
	TTL        int    `json:"ttl"`
}

type TargetReport struct {
	Date       string  `json:"date"`
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Category   string  `json:"category"`
	Host       string  `json:"host"`
	Current    string  `json:"current"`
	Checks     int     `json:"checks"`
	Up         int     `json:"up"`
	Down       int     `json:"down"`
	UptimePct  float64 `json:"uptime_pct"`
	AvgLatency float64 `json:"avg_latency"`
}

var (
	targets = make(map[string]*Target)
	mutex   = &sync.RWMutex{}
)

// StartMonitoring initializes the background monitoring loops
func StartMonitoring() {
	log.Println("Starting background monitoring service...")

	// Load targets from DB
	loadTargetsFromDB()

	// Start worker loop
	go monitorLoop()
	go pruneHistoryLoop()
}

func loadTargetsFromDB() {
	mutex.Lock()
	defer mutex.Unlock()

	rows, err := db.Query("SELECT id, name, host, type, interval, COALESCE(tag, ''), COALESCE(category, ''), COALESCE(snmp_community, 'public'), COALESCE(snmp_oid, ''), COALESCE(wmi_user, ''), COALESCE(wmi_pass, ''), COALESCE(wmi_namespace, ''), COALESCE(wmi_query, '') FROM targets")
	if err != nil {
		log.Printf("Failed to load targets from DB: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var t Target
		var host, tType, tag, category, snmpComm, snmpOID, wmiUser, wmiPass, wmiNS, wmiQuery string
		if err := rows.Scan(&t.ID, &t.Name, &host, &tType, &t.Interval, &tag, &category, &snmpComm, &snmpOID, &wmiUser, &wmiPass, &wmiNS, &wmiQuery); err != nil {
			log.Printf("Target row err: %v", err)
			continue
		}
		t.Host = host
		t.Type = tType
		t.Tag = tag
		t.Category = category
		t.SnmpCommunity = snmpComm
		t.SnmpOID = snmpOID
		t.WmiUser = wmiUser
		t.WmiPass = wmiPass
		t.WmiNamespace = wmiNS
		t.WmiQuery = wmiQuery
		if t.Tag == "" {
			t.Tag = "General"
		}
		if t.Category == "" {
			t.Category = "General"
		}
		t.Status = "unknown"
		targets[t.ID] = &t
	}
	if err = rows.Err(); err != nil {
		log.Printf("Targets rows iteration error: %v", err)
	}
}

func AddTarget(t *Target) error {
	t.ID = uuid.New().String()
	t.Status = "unknown"

	_, err := db.Exec("INSERT INTO targets (id, name, host, type, interval, tag, category, snmp_community, snmp_oid, wmi_user, wmi_pass, wmi_namespace, wmi_query) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		t.ID, t.Name, t.Host, t.Type, t.Interval, t.Tag, t.Category, t.SnmpCommunity, t.SnmpOID, t.WmiUser, t.WmiPass, t.WmiNamespace, t.WmiQuery)
	if err != nil {
		return err
	}

	mutex.Lock()
	targets[t.ID] = t
	mutex.Unlock()
	return nil
}

func UpdateTarget(t *Target) error {
	_, err := db.Exec("UPDATE targets SET name = ?, host = ?, type = ?, interval = ?, tag = ?, category = ?, snmp_community = ?, snmp_oid = ?, wmi_user = ?, wmi_pass = ?, wmi_namespace = ?, wmi_query = ? WHERE id = ?",
		t.Name, t.Host, t.Type, t.Interval, t.Tag, t.Category, t.SnmpCommunity, t.SnmpOID, t.WmiUser, t.WmiPass, t.WmiNamespace, t.WmiQuery, t.ID)
	if err != nil {
		return err
	}

	mutex.Lock()
	if existing, exists := targets[t.ID]; exists {
		t.Status = existing.Status
		t.Latency = existing.Latency
		t.LastCheck = time.Time{} // Force check
		t.checking = existing.checking
		if t.Host == existing.Host && t.Type == existing.Type {
			t.ReplyIP = existing.ReplyIP
			t.TTL = existing.TTL
			t.SucceedCount = existing.SucceedCount
			t.FailedCount = existing.FailedCount
			t.TotalSent = existing.TotalSent
			t.MinLatency = existing.MinLatency
			t.MaxLatency = existing.MaxLatency
			t.prevSnmpValue = existing.prevSnmpValue
			t.prevSnmpTime = existing.prevSnmpTime
		}
		targets[t.ID] = t
	}
	mutex.Unlock()
	return nil
}

func DeleteTarget(id string) error {
	_, err := db.Exec("DELETE FROM targets WHERE id = ?", id)
	if err != nil {
		return err
	}

	mutex.Lock()
	delete(targets, id)
	mutex.Unlock()

	// Clean up old history for deleted target
	db.Exec("DELETE FROM latency_history WHERE target_id = ?", id)
	db.Exec("DELETE FROM settings WHERE key LIKE ?", sslAlertPrefix(id)+"%")
	return nil
}

func GetTargets() []*Target {
	mutex.RLock()
	defer mutex.RUnlock()
	var list []*Target
	for _, t := range targets {
		snapshot := *t
		list = append(list, &snapshot)
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].Name != list[j].Name {
			return list[i].Name < list[j].Name
		}
		return list[i].ID < list[j].ID // keeps a selected row stable between refreshes
	})

	return list
}

func monitorLoop() {
	for {
		mutex.Lock()
		var targetsToCheck []*Target
		for _, t := range targets {
			if !t.checking && time.Since(t.LastCheck).Seconds() >= float64(t.Interval) {
				t.checking = true
				targetsToCheck = append(targetsToCheck, t)
			}
		}
		mutex.Unlock()

		for _, t := range targetsToCheck {
			go runCheck(t)
		}

		time.Sleep(1 * time.Second)
	}
}

// runCheck runs one check and then marks the target as free for the next one.
func runCheck(t *Target) {
	defer func() {
		mutex.Lock()
		if current, exists := targets[t.ID]; exists {
			current.checking = false
		}
		mutex.Unlock()
	}()
	checkTarget(t)
}

// retentionPeriod returns the configured retention. Only known values are
// returned because the result is placed directly into SQL date modifiers.
func retentionPeriod() string {
	switch v := getSetting("data_retention"); v {
	case "1 month", "3 months", "6 months", "1 year":
		return v
	}
	return "3 months"
}

func pruneHistoryLoop() {
	for {
		query := fmt.Sprintf(`DELETE FROM latency_history WHERE timestamp < datetime('now', '-%s')`, retentionPeriod())
		if _, err := db.Exec(query); err != nil {
			log.Printf("Failed to prune history: %v", err)
		}
		time.Sleep(1 * time.Hour)
	}
}

func checkTarget(t *Target) {
	if t.Type == "ping" {
		pinger, err := ping.NewPinger(t.Host)
		if err != nil {
			log.Printf("Pinger failed for %s: %v", t.Host, err)
			updateTargetStatus(t.ID, "offline", 0, "", 0)
			return
		}

		pinger.SetPrivileged(true)
		pinger.Count = 1
		pinger.Timeout = time.Second * 3

		var replyIP string
		var ttl int
		pinger.OnRecv = func(pkt *ping.Packet) {
			replyIP = pkt.IPAddr.String()
			ttl = pkt.Ttl
		}

		err = pinger.Run()
		if err != nil {
			updateTargetStatus(t.ID, "offline", 0, "", 0)
			return
		}

		stats := pinger.Statistics()
		if stats.PacketsRecv > 0 {
			if replyIP == "" && stats.IPAddr != nil {
				replyIP = stats.IPAddr.String()
			}
			updateTargetStatus(t.ID, "online", stats.AvgRtt.Milliseconds(), replyIP, ttl)
		} else {
			updateTargetStatus(t.ID, "offline", 0, "", 0)
		}
	} else if t.Type == "ssl" {
		checkSSL(t)
	} else if t.Type == "port" {
		checkPort(t)
	} else if t.Type == "dns" {
		checkDNS(t)
	} else if t.Type == "snmp" || t.Type == "bandwidth" {
		checkSNMP(t)
	} else if t.Type == "wmi" {
		checkWMI(t)
	}
}

func checkWMI(t *Target) {
	type Win32_PerfFormattedData_Tcpip_NetworkInterface struct {
		BytesTotalPersec uint64
	}

	var dst []Win32_PerfFormattedData_Tcpip_NetworkInterface

	query := t.WmiQuery
	if query == "" {
		query = "SELECT BytesTotalPersec FROM Win32_PerfFormattedData_Tcpip_NetworkInterface"
	}

	namespace := t.WmiNamespace
	if namespace == "" {
		namespace = `root\cimv2`
	}

	err := wmi.Query(query, &dst, t.Host, namespace, t.WmiUser, t.WmiPass)
	if err != nil {
		updateTargetStatus(t.ID, "offline", 0, "", 0)
		return
	}

	if len(dst) > 0 {
		var totalBytes uint64 = 0
		for _, nic := range dst {
			totalBytes += nic.BytesTotalPersec
		}
		mbps := float64(totalBytes) * 8.0 / 1000000.0
		updateTargetStatus(t.ID, "online", int64(mbps), "", 0)
	} else {
		updateTargetStatus(t.ID, "online", 0, "", 0)
	}
}

// snmpPort is a variable so tests can use an unprivileged port.
var snmpPort uint16 = 161

// newSNMPClient returns a client for one check. Checks run concurrently, so
// they must never share a client or its connection.
func newSNMPClient(t *Target) *gosnmp.GoSNMP {
	community := t.SnmpCommunity
	if community == "" {
		community = "public"
	}
	return &gosnmp.GoSNMP{
		Target:             t.Host,
		Port:               snmpPort,
		Transport:          "udp",
		Community:          community,
		Version:            gosnmp.Version2c,
		Timeout:            2 * time.Second,
		Retries:            3,
		ExponentialTimeout: true,
		MaxOids:            gosnmp.MaxOids,
	}
}

func checkSNMP(t *Target) {
	client := newSNMPClient(t)
	err := client.Connect()
	if err != nil {
		updateTargetStatus(t.ID, "offline", 0, "", 0)
		return
	}
	defer client.Conn.Close()

	if t.SnmpOID == "" {
		// Just test connection
		updateTargetStatus(t.ID, "online", 1, "", 0)
		return
	}

	oids := []string{t.SnmpOID}
	result, err := client.Get(oids)
	if err != nil || len(result.Variables) == 0 {
		updateTargetStatus(t.ID, "offline", 0, "", 0)
		return
	}

	val := result.Variables[0].Value

	// Try to get numeric value
	var numVal uint64
	switch v := val.(type) {
	case uint:
		numVal = uint64(v)
	case uint32:
		numVal = uint64(v)
	case uint64:
		numVal = v
	case int:
		numVal = uint64(v)
	case int32:
		numVal = uint64(v)
	case int64:
		numVal = uint64(v)
	case string:
		p, _ := strconv.ParseUint(v, 10, 64)
		numVal = p
	default:
		// Not parsed
	}

	if t.Type == "bandwidth" {
		now := time.Now()
		if !t.prevSnmpTime.IsZero() && numVal >= t.prevSnmpValue {
			diffBytes := numVal - t.prevSnmpValue
			diffTime := now.Sub(t.prevSnmpTime).Seconds()
			if diffTime > 0 {
				mbps := (float64(diffBytes) * 8.0) / 1000000.0 / diffTime
				updateTargetStatus(t.ID, "online", int64(mbps), "", 0)
			}
		} else {
			updateTargetStatus(t.ID, "online", 0, "", 0)
		}

		mutex.Lock()
		if stored, exists := targets[t.ID]; exists {
			stored.prevSnmpValue = numVal
			stored.prevSnmpTime = now
		}
		mutex.Unlock()
	} else {
		if numVal == 0 {
			updateTargetStatus(t.ID, "online", 1, "", 0)
		} else {
			updateTargetStatus(t.ID, "online", int64(numVal), "", 0)
		}
	}
}

func checkDNS(t *Target) {
	start := time.Now()
	addrs, err := net.LookupHost(t.Host)
	duration := time.Since(start).Milliseconds()
	if err != nil {
		updateTargetStatus(t.ID, "offline", 0, "", 0)
	} else {
		ip := ""
		if len(addrs) > 0 {
			ip = addrs[0]
		}
		updateTargetStatus(t.ID, "online", duration, ip, 0)
	}
}

func checkPort(t *Target) {
	conn, err := net.DialTimeout("tcp", t.Host, 4*time.Second)
	if err != nil {
		updateTargetStatus(t.ID, "offline", 0, "", 0)
		return
	}
	defer conn.Close()

	// measure latency
	start := time.Now()
	conn2, err2 := net.DialTimeout("tcp", t.Host, 4*time.Second)
	if err2 == nil {
		conn2.Close()
		duration := time.Since(start).Milliseconds()
		// Also try to resolve IP for port targets if possible
		ip := ""
		if remoteAddr, ok := conn2.RemoteAddr().(*net.TCPAddr); ok {
			ip = remoteAddr.IP.String()
		} else if remoteAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
			ip = remoteAddr.IP.String()
		}
		updateTargetStatus(t.ID, "online", duration, ip, 0)
	} else {
		updateTargetStatus(t.ID, "online", 1, "", 0) // connected but subsequent failed
	}
}

func updateTargetStatus(id string, status string, latencyMs int64, replyIP string, ttl int) {
	mutex.Lock()
	targetExists := false
	var targetCopy Target
	if t, exists := targets[id]; exists {
		t.Status = status
		t.Latency = latencyMs
		t.LastCheck = time.Now()

		if replyIP != "" {
			t.ReplyIP = replyIP
		}
		if ttl > 0 {
			t.TTL = ttl
		}

		t.TotalSent++
		if status == "online" {
			t.SucceedCount++
			if t.Type != "ping" && t.Type != "port" {
				// Don't log min/max bounds for SSL days logic
			} else {
				if t.MinLatency == 0 || latencyMs < t.MinLatency {
					t.MinLatency = latencyMs
				}
				if latencyMs > t.MaxLatency {
					t.MaxLatency = latencyMs
				}
			}
		} else {
			t.FailedCount++
		}

		targetCopy = *t
		targetExists = true
	}
	mutex.Unlock()

	if targetExists {
		// Log history
		_, err := db.Exec("INSERT INTO latency_history (target_id, latency, status, reply_ip, ttl) VALUES (?, ?, ?, ?, ?)",
			targetCopy.ID, targetCopy.Latency, targetCopy.Status, targetCopy.ReplyIP, ttl)
		if err != nil {
			log.Printf("Failed to record stats to db: %v", err)
		}

		// Check alerting settings
		checkAlerting(&targetCopy)
	}
}

func GetTargetHistory(id string) ([]LatencyHistory, error) {
	// Fetch up to 500 most recent records within retention period for the UI chart
	query := fmt.Sprintf(`
		SELECT timestamp, latency, status, COALESCE(reply_ip, ''), COALESCE(ttl, 0) 
		FROM latency_history 
		WHERE target_id = ? AND timestamp >= datetime('now', '-%s') 
		ORDER BY timestamp DESC LIMIT 500`, retentionPeriod())

	rows, err := db.Query(query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []LatencyHistory
	for rows.Next() {
		var h LatencyHistory
		var tString string
		if err := rows.Scan(&tString, &h.Latency, &h.Status, &h.ReplyIP, &h.TTL); err == nil {
			h.Timestamp = tString
			history = append(history, h)
		}
	}
	if err = rows.Err(); err != nil {
		return history, err
	}

	// Reverse to ASC for the chart
	for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
		history[i], history[j] = history[j], history[i]
	}

	return history, nil
}

func GetFullTargetHistory(id string) ([]LatencyHistory, error) {
	// Full history for CSV drops descending
	rows, err := db.Query("SELECT timestamp, latency, status, COALESCE(reply_ip, ''), COALESCE(ttl, 0) FROM latency_history WHERE target_id = ? ORDER BY timestamp DESC LIMIT 5000", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []LatencyHistory
	for rows.Next() {
		var h LatencyHistory
		var tString string
		if err := rows.Scan(&tString, &h.Latency, &h.Status, &h.ReplyIP, &h.TTL); err == nil {
			h.Timestamp = tString
			history = append(history, h)
		}
	}
	if err = rows.Err(); err != nil {
		return history, err
	}
	return history, nil
}

func GetGlobalHistory() ([]LatencyHistory, error) {
	// Full global history for the UI tab max 500
	rows, err := db.Query(`
		SELECT h.target_id, COALESCE(t.name, h.target_id), h.timestamp, h.latency, h.status, COALESCE(h.reply_ip, ''), COALESCE(h.ttl, 0)
		FROM latency_history h
		LEFT JOIN targets t ON h.target_id = t.id
		ORDER BY h.timestamp DESC 
		LIMIT 500
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []LatencyHistory
	for rows.Next() {
		var h LatencyHistory
		var tString string
		var tName string
		var tId string
		if err := rows.Scan(&tId, &tName, &tString, &h.Latency, &h.Status, &h.ReplyIP, &h.TTL); err == nil {
			h.TargetID = tId
			h.TargetName = tName
			h.Timestamp = tString
			history = append(history, h)
		}
	}
	if err = rows.Err(); err != nil {
		return history, err
	}
	return history, nil
}

var errInvalidReportDate = errors.New("start and end must be dates in YYYY-MM-DD format")

func isReportDate(value string) bool {
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func GetGlobalReport(period, customStart, customEnd string) ([]TargetReport, error) {
	var timeArgs []any
	timeFilter := `substr(h.timestamp, 1, 10) = substr(datetime('now', 'localtime'), 1, 10)`
	if period == "yesterday" {
		timeFilter = `substr(h.timestamp, 1, 10) = substr(datetime('now', '-1 day', 'localtime'), 1, 10)`
	} else if period == "this_week" {
		timeFilter = `substr(h.timestamp, 1, 10) >= substr(datetime('now', '-7 days', 'localtime'), 1, 10)`
	} else if period == "custom" && customStart != "" && customEnd != "" {
		if !isReportDate(customStart) || !isReportDate(customEnd) {
			return nil, errInvalidReportDate
		}
		timeFilter = `substr(h.timestamp, 1, 10) BETWEEN ? AND ?`
		timeArgs = []any{customStart, customEnd}
	}

	query := fmt.Sprintf(`
		SELECT 
			substr(h.timestamp, 1, 10) as d,
			COALESCE(t.name, h.target_id),
			COALESCE(t.type, 'unknown'),
			COALESCE(t.tag, 'General'),
			COALESCE(t.host, '-'),
			h.target_id,
			COUNT(h.timestamp) as checks,
			SUM(CASE WHEN h.status='online' THEN 1 ELSE 0 END) as up_count,
			SUM(CASE WHEN h.status='offline' THEN 1 ELSE 0 END) as down_count,
			SUM(h.latency) as total_latency
		FROM latency_history h
		LEFT JOIN targets t ON h.target_id = t.id
		WHERE %s
		GROUP BY t.id, d
		ORDER BY d DESC, COALESCE(t.name, h.target_id) ASC
	`, timeFilter)

	rows, err := db.Query(query, timeArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reports []TargetReport
	for rows.Next() {
		var r TargetReport
		var totalLat float64
		var targetId string
		if err := rows.Scan(&r.Date, &r.Name, &r.Type, &r.Category, &r.Host, &targetId, &r.Checks, &r.Up, &r.Down, &totalLat); err == nil {
			r.Current = "offline"
			mutex.RLock()
			if memT, exists := targets[targetId]; exists {
				r.Current = memT.Status
			}
			mutex.RUnlock()

			if r.Checks > 0 {
				r.UptimePct = float64(r.Up) / float64(r.Checks) * 100.0
			}
			if r.Up > 0 {
				r.AvgLatency = totalLat / float64(r.Up)
			}
			reports = append(reports, r)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return reports, nil
}
