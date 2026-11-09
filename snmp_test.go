package main

import (
	"net"
	"sync"
	"testing"

	"github.com/gosnmp/gosnmp"
)

// startFakeSNMPAgent answers only requests that use its community string.
func startFakeSNMPAgent(t *testing.T, ip string, port int, community string, value uint) int {
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
			request.Variables = []gosnmp.SnmpPDU{{Name: request.Variables[0].Name, Type: gosnmp.Gauge32, Value: value}}
			if response, err := request.MarshalMsg(); err == nil {
				conn.WriteToUDP(response, sender)
			}
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

func TestSNMPChecksRunningTogetherKeepTheirOwnResults(t *testing.T) {
	port := startFakeSNMPAgent(t, "127.0.0.2", 0, "community-a", 101)
	startFakeSNMPAgent(t, "127.0.0.3", port, "community-b", 202)

	original := snmpPort
	snmpPort = uint16(port)
	t.Cleanup(func() { snmpPort = original })

	devices := []struct {
		host, community string
		want            int64
	}{
		{"127.0.0.2", "community-a", 101},
		{"127.0.0.3", "community-b", 202},
	}

	var wait sync.WaitGroup
	for _, device := range devices {
		target := &Target{Name: device.host, Host: device.host, Type: "snmp", Interval: 60, SnmpCommunity: device.community, SnmpOID: "1.3.6.1.2.1.1.3.0"}
		if err := AddTarget(target); err != nil {
			t.Fatal(err)
		}
		id := target.ID
		t.Cleanup(func() { DeleteTarget(id) })

		wait.Add(1)
		go func(want int64) {
			defer wait.Done()
			for i := 0; i < 200; i++ {
				checkSNMP(target)
				mutex.RLock()
				status, got := targets[id].Status, targets[id].Latency
				mutex.RUnlock()
				if status != "online" || got != want {
					t.Errorf("%s check %d: status %s, value %d, want online %d", target.Host, i, status, got, want)
					return
				}
			}
		}(device.want)
	}
	wait.Wait()
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
