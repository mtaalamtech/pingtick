package main

import (
	"net"
	"sync"
	"testing"

	"github.com/gosnmp/gosnmp"
)

type fakeSNMPReply struct {
	kind  gosnmp.Asn1BER
	value any
	err   gosnmp.SNMPError
}

// startFakeSNMPAgent answers only requests that use its community string.
func startFakeSNMPAgent(t *testing.T, ip string, port int, community string, reply fakeSNMPReply) int {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(ip), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	go func() {
		decoder := &gosnmp.GoSNMP{Version: gosnmp.Version2c, Community: community}
		buffer := make([]byte, 1500)
		for {
			size, sender, err := conn.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			request, err := decoder.SnmpDecodePacket(buffer[:size])
			if err != nil || request.Community != community || len(request.Variables) == 0 {
				continue
			}
			request.PDUType = gosnmp.GetResponse
			request.Error = reply.err
			request.Variables = []gosnmp.SnmpPDU{{Name: request.Variables[0].Name, Type: reply.kind, Value: reply.value}}
			if response, err := request.MarshalMsg(); err == nil {
				conn.WriteToUDP(response, sender)
			}
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

func useSNMPPort(t *testing.T, port int) {
	t.Helper()
	original := snmpPort
	snmpPort = uint16(port)
	t.Cleanup(func() { snmpPort = original })
}

func addSNMPTarget(t *testing.T, targetType, host, community string) *Target {
	t.Helper()
	target := &Target{Name: host, Host: host, Type: targetType, Interval: 60, SnmpCommunity: community, SnmpOID: "1.3.6.1.2.1.1.3.0"}
	if err := AddTarget(target); err != nil {
		t.Fatal(err)
	}
	id := target.ID
	t.Cleanup(func() { DeleteTarget(id) })
	return target
}

func snmpTargetResult(id string) (string, int64) {
	mutex.RLock()
	defer mutex.RUnlock()
	return targets[id].Status, targets[id].Latency
}

func TestSNMPChecksRunningTogetherKeepTheirOwnResults(t *testing.T) {
	port := startFakeSNMPAgent(t, "127.0.0.2", 0, "community-a", fakeSNMPReply{kind: gosnmp.Gauge32, value: uint(101)})
	startFakeSNMPAgent(t, "127.0.0.3", port, "community-b", fakeSNMPReply{kind: gosnmp.Gauge32, value: uint(202)})
	useSNMPPort(t, port)

	devices := []struct {
		host, community string
		want            int64
	}{
		{"127.0.0.2", "community-a", 101},
		{"127.0.0.3", "community-b", 202},
	}

	var wait sync.WaitGroup
	for _, device := range devices {
		target := addSNMPTarget(t, "snmp", device.host, device.community)
		wait.Add(1)
		go func(want int64) {
			defer wait.Done()
			for i := 0; i < 200; i++ {
				checkSNMP(target)
				if status, got := snmpTargetResult(target.ID); status != "online" || got != want {
					t.Errorf("%s check %d: status %s, value %d, want online %d", target.Host, i, status, got, want)
					return
				}
			}
		}(device.want)
	}
	wait.Wait()
}

func TestSNMPCheckInterpretsReplies(t *testing.T) {
	tests := []struct {
		name       string
		targetType string
		reply      fakeSNMPReply
		status     string
		value      int64
	}{
		{"oid does not exist", "snmp", fakeSNMPReply{kind: gosnmp.NoSuchObject}, "offline", 0},
		{"instance does not exist", "snmp", fakeSNMPReply{kind: gosnmp.NoSuchInstance}, "offline", 0},
		{"device reports an error", "snmp", fakeSNMPReply{kind: gosnmp.Null, err: gosnmp.GenErr}, "offline", 0},
		{"number sent as text", "snmp", fakeSNMPReply{kind: gosnmp.OctetString, value: "42"}, "online", 42},
		{"decimal sent as text", "snmp", fakeSNMPReply{kind: gosnmp.OctetString, value: " 23.7 "}, "online", 23},
		{"plain text", "snmp", fakeSNMPReply{kind: gosnmp.OctetString, value: "Linux router"}, "online", 1},
		{"text that looks like nan", "snmp", fakeSNMPReply{kind: gosnmp.OctetString, value: "nan"}, "online", 1},
		{"real zero", "snmp", fakeSNMPReply{kind: gosnmp.Gauge32, value: uint(0)}, "online", 0},
		{"integer", "snmp", fakeSNMPReply{kind: gosnmp.Integer, value: 55}, "online", 55},
		{"bandwidth from text", "bandwidth", fakeSNMPReply{kind: gosnmp.OctetString, value: "not a counter"}, "offline", 0},
		{"first bandwidth reading", "bandwidth", fakeSNMPReply{kind: gosnmp.Counter32, value: uint(1000)}, "online", 0},
		{"missing bandwidth oid", "bandwidth", fakeSNMPReply{kind: gosnmp.NoSuchObject}, "offline", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := startFakeSNMPAgent(t, "127.0.0.4", 0, "public", tt.reply)
			useSNMPPort(t, port)
			target := addSNMPTarget(t, tt.targetType, "127.0.0.4", "public")

			checkSNMP(target)
			if status, value := snmpTargetResult(target.ID); status != tt.status || value != tt.value {
				t.Fatalf("status %s, value %d, want %s %d", status, value, tt.status, tt.value)
			}
		})
	}
}

func TestSNMPNumberKeepsNegativeValues(t *testing.T) {
	number, ok := snmpNumber(gosnmp.SnmpPDU{Value: -5})
	if !ok || int64(number) != -5 {
		t.Fatalf("number = %d, %v", int64(number), ok)
	}
	number, ok = snmpNumber(gosnmp.SnmpPDU{Value: []byte("-12")})
	if !ok || int64(number) != -12 {
		t.Fatalf("text number = %d, %v", int64(number), ok)
	}
}

func TestSNMPClientDefaultsToPublicCommunity(t *testing.T) {
	client := newSNMPClient(&Target{Host: "10.0.0.1"})
	if client.Community != "public" || client.Target != "10.0.0.1" || client.Port != 161 || client.Version != gosnmp.Version2c {
		t.Fatalf("client = %+v", client)
	}
	if client == newSNMPClient(&Target{Host: "10.0.0.1"}) {
		t.Fatal("clients are shared")
	}
}
