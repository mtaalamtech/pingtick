package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-ping/ping"
)

const (
	maxScanAddresses = 4096 // a /20
	scanWorkers      = 64
	scanPingTimeout  = 1 * time.Second
	scanTCPTimeout   = 400 * time.Millisecond
	scanDNSTimeout   = 1500 * time.Millisecond
	createNoWindow   = 0x08000000
)

// scanTCPPorts finds hosts that drop ping, which Windows does by default.
var scanTCPPorts = []int{445, 3389, 80, 443, 22}

var arpEntry = regexp.MustCompile(`(\d{1,3}(?:\.\d{1,3}){3})\s+((?:[0-9a-fA-F]{2}-){5}[0-9a-fA-F]{2})`)

var errScanRunning = errors.New("a scan is already running")

type ScanHost struct {
	IP         string `json:"ip"`
	Hostname   string `json:"hostname"`
	MAC        string `json:"mac"`
	Latency    int64  `json:"latency"`
	DetectedBy string `json:"detected_by"`
}

type ScanState struct {
	Running bool       `json:"running"`
	Range   string     `json:"range"`
	Total   int        `json:"total"`
	Done    int        `json:"done"`
	Hosts   []ScanHost `json:"hosts"`
}

var (
	scanMu     sync.Mutex
	scanState  ScanState
	scanCancel context.CancelFunc
)

// parseScanRange accepts 192.168.1.0/24, 192.168.1.1-254,
// 192.168.1.1-192.168.2.10 or a single IPv4 address.
func parseScanRange(input string) ([]netip.Addr, error) {
	input = strings.TrimSpace(input)
	first, last, err := scanBounds(input)
	if err != nil {
		return nil, err
	}
	if last < first {
		return nil, errors.New("the end of the range is before the start")
	}
	if uint64(last)-uint64(first)+1 > maxScanAddresses {
		return nil, fmt.Errorf("range is too large; the limit is %d addresses", maxScanAddresses)
	}

	addrs := make([]netip.Addr, 0, last-first+1)
	for n := uint64(first); n <= uint64(last); n++ {
		addrs = append(addrs, addrFromUint32(uint32(n)))
	}
	return addrs, nil
}

func scanBounds(input string) (first, last uint32, err error) {
	if strings.Contains(input, "/") {
		prefix, err := netip.ParsePrefix(input)
		if err != nil || !prefix.Addr().Is4() {
			return 0, 0, errors.New("invalid IPv4 network")
		}
		first = uint32FromAddr(prefix.Masked().Addr())
		last = first | uint32(uint64(1)<<(32-prefix.Bits())-1)
		if prefix.Bits() <= 30 { // skip the network and broadcast addresses
			first++
			last--
		}
		return first, last, nil
	}

	left, right, isRange := strings.Cut(input, "-")
	start, err := parseIPv4(left)
	if err != nil {
		return 0, 0, err
	}
	first = uint32FromAddr(start)
	if !isRange {
		return first, first, nil
	}

	right = strings.TrimSpace(right)
	if !strings.Contains(right, ".") {
		octet, err := strconv.Atoi(right)
		if err != nil || octet < 0 || octet > 255 {
			return 0, 0, errors.New("invalid end of range")
		}
		return first, first&^0xff | uint32(octet), nil
	}
	end, err := parseIPv4(right)
	if err != nil {
		return 0, 0, err
	}
	return first, uint32FromAddr(end), nil
}

func parseIPv4(s string) (netip.Addr, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil || !addr.Is4() {
		return netip.Addr{}, errors.New("invalid IPv4 address")
	}
	return addr, nil
}

func uint32FromAddr(a netip.Addr) uint32 {
	b := a.As4()
	return binary.BigEndian.Uint32(b[:])
}

func addrFromUint32(n uint32) netip.Addr {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], n)
	return netip.AddrFrom4(b)
}

func startScan(input string) error {
	addrs, err := parseScanRange(input)
	if err != nil {
		return err
	}

	scanMu.Lock()
	defer scanMu.Unlock()
	if scanState.Running {
		return errScanRunning
	}
	ctx, cancel := context.WithCancel(context.Background())
	scanCancel = cancel
	scanState = ScanState{Running: true, Range: strings.TrimSpace(input), Total: len(addrs)}
	go runScan(ctx, cancel, addrs)
	return nil
}

func stopScan() {
	scanMu.Lock()
	defer scanMu.Unlock()
	if scanCancel != nil {
		scanCancel()
	}
}

func scanSnapshot() ScanState {
	scanMu.Lock()
	defer scanMu.Unlock()
	state := scanState
	state.Hosts = append([]ScanHost(nil), scanState.Hosts...)
	sort.Slice(state.Hosts, func(i, j int) bool {
		a, _ := netip.ParseAddr(state.Hosts[i].IP)
		b, _ := netip.ParseAddr(state.Hosts[j].IP)
		return a.Less(b)
	})
	return state
}

func runScan(ctx context.Context, cancel context.CancelFunc, addrs []netip.Addr) {
	defer cancel()

	jobs := make(chan netip.Addr)
	var wg sync.WaitGroup
	for i := 0; i < min(scanWorkers, len(addrs)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for addr := range jobs {
				host, alive := probeHost(ctx, addr)
				scanMu.Lock()
				scanState.Done++
				if alive {
					scanState.Hosts = append(scanState.Hosts, host)
				}
				scanMu.Unlock()
			}
		}()
	}

feed:
	for _, addr := range addrs {
		select {
		case jobs <- addr:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()

	macs := readARPTable()
	scanMu.Lock()
	for i := range scanState.Hosts {
		scanState.Hosts[i].MAC = macs[scanState.Hosts[i].IP]
	}
	scanState.Running = false
	scanMu.Unlock()
}

func probeHost(ctx context.Context, addr netip.Addr) (ScanHost, bool) {
	if ctx.Err() != nil {
		return ScanHost{}, false
	}

	ip := addr.String()
	host := ScanHost{IP: ip, DetectedBy: "ICMP"}
	latency, ok := pingOnce(ip)
	if ok {
		host.Latency = latency
	} else {
		port, latency, open := probeTCP(ctx, ip)
		if !open {
			return ScanHost{}, false
		}
		host.Latency = latency
		host.DetectedBy = "TCP " + strconv.Itoa(port)
	}

	lookup, cancel := context.WithTimeout(ctx, scanDNSTimeout)
	defer cancel()
	if names, err := net.DefaultResolver.LookupAddr(lookup, ip); err == nil && len(names) > 0 {
		host.Hostname = strings.TrimSuffix(names[0], ".")
	}
	return host, true
}

func pingOnce(ip string) (int64, bool) {
	pinger, err := ping.NewPinger(ip)
	if err != nil {
		return 0, false
	}
	pinger.SetPrivileged(true)
	pinger.Count = 1
	pinger.Timeout = scanPingTimeout

	var rtt time.Duration
	received := false
	pinger.OnRecv = func(pkt *ping.Packet) {
		rtt = pkt.Rtt
		received = true
	}
	if err := pinger.Run(); err != nil || !received {
		return 0, false
	}
	return rtt.Milliseconds(), true
}

// probeTCP reports the first port that shows a host exists. A refused
// connection counts, because only a live machine can refuse it.
func probeTCP(ctx context.Context, ip string) (port int, latency int64, open bool) {
	ctx, cancel := context.WithTimeout(ctx, scanTCPTimeout)
	defer cancel()

	type result struct {
		port    int
		latency int64
		alive   bool
	}
	results := make(chan result, len(scanTCPPorts))
	for _, p := range scanTCPPorts {
		go func(p int) {
			start := time.Now()
			var dialer net.Dialer
			conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(p)))
			if conn != nil {
				conn.Close()
			}
			alive := err == nil || strings.Contains(err.Error(), "refused")
			results <- result{p, time.Since(start).Milliseconds(), alive}
		}(p)
	}

	for range scanTCPPorts {
		if r := <-results; r.alive {
			return r.port, r.latency, true
		}
	}
	return 0, 0, false
}

func readARPTable() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "arp", "-a")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	output, err := cmd.Output()
	if err != nil {
		log.Printf("Could not read ARP table: %v", err)
		return nil
	}

	macs := make(map[string]string)
	for _, match := range arpEntry.FindAllStringSubmatch(string(output), -1) {
		mac := strings.ToUpper(match[2])
		if mac != "FF-FF-FF-FF-FF-FF" {
			macs[match[1]] = mac
		}
	}
	return macs
}

// localScanRanges suggests this computer's private networks. Public addresses
// are never included, so the scanner cannot reveal one on screen.
func localScanRanges() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	ranges := []string{}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil || !ipNet.IP.IsPrivate() {
				continue
			}
			bits, _ := ipNet.Mask.Size()
			prefix := netip.PrefixFrom(netip.AddrFrom4([4]byte(ipNet.IP.To4())), max(bits, 24))
			ranges = append(ranges, prefix.Masked().String())
		}
	}
	return ranges
}

func handleScan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		json.NewEncoder(w).Encode(struct {
			ScanState
			LocalRanges []string `json:"local_ranges"`
		}{scanSnapshot(), localScanRanges()})

	case http.MethodPost:
		var body struct {
			Range string `json:"range"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}
		if err := startScan(body.Range); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, errScanRunning) {
				status = http.StatusConflict
			}
			http.Error(w, err.Error(), status)
			return
		}
		w.Write([]byte(`{"status": "started"}`))

	case http.MethodDelete:
		stopScan()
		w.Write([]byte(`{"status": "stopping"}`))

	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}
