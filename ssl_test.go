package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const day = 24 * time.Hour

// TestMain uses a temporary database so tests never touch pingtick.db.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pingtick-test")
	if err != nil {
		log.Fatal(err)
	}
	db, err = sql.Open("sqlite", filepath.Join(dir, "test.db"))
	if err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE targets (id TEXT PRIMARY KEY, name TEXT, host TEXT, type TEXT, interval INTEGER, tag TEXT, category TEXT,
			snmp_community TEXT, snmp_oid TEXT, wmi_user TEXT, wmi_pass TEXT, wmi_namespace TEXT, wmi_query TEXT)`,
		`CREATE TABLE latency_history (target_id TEXT)`,
	} {
		if _, err := db.Exec(q); err != nil {
			log.Fatal(err)
		}
	}
	code := m.Run()
	db.Close()
	os.RemoveAll(dir)
	os.Exit(code)
}

// captureAlerts replaces the sender and returns the list of alerts sent.
func captureAlerts(t *testing.T, delivered *bool) *[]string {
	t.Helper()
	sent := &[]string{}
	original := sendSSLAlert
	sendSSLAlert = func(msg string) bool {
		*sent = append(*sent, msg)
		return delivered == nil || *delivered
	}
	t.Cleanup(func() { sendSSLAlert = original })
	return sent
}

func TestSSLWarningLevel(t *testing.T) {
	tests := []struct {
		left  time.Duration
		level int
		ok    bool
	}{
		{31 * day, 0, false},
		{30 * day, 30, true},
		{29 * day, 30, true},
		{14 * day, 14, true},
		{8 * day, 14, true},
		{7 * day, 7, true},
		{2 * day, 7, true},
		{1 * day, 1, true},
		{time.Hour, 1, true},
		{0, 0, true},
		{-5 * day, 0, true},
	}
	for _, tt := range tests {
		level, ok := sslWarningLevel(tt.left)
		if level != tt.level || ok != tt.ok {
			t.Errorf("sslWarningLevel(%v) = %d, %v; want %d, %v", tt.left, level, ok, tt.level, tt.ok)
		}
	}
}

func TestWarnCertExpiryAlertsOncePerThreshold(t *testing.T) {
	sent := captureAlerts(t, nil)
	target := &Target{ID: "once", Name: "Site", Host: "example.test"}
	notAfter := time.Now().Add(20 * day)

	warnCertExpiry(target, notAfter)
	warnCertExpiry(target, notAfter)
	if len(*sent) != 1 {
		t.Fatalf("got %d alerts at the 30 day threshold, want 1: %q", len(*sent), *sent)
	}

}

func TestWarnCertExpiryAlertsAgainAtLowerThreshold(t *testing.T) {
	sent := captureAlerts(t, nil)
	target := &Target{ID: "lower", Name: "Site", Host: "example.test"}
	notAfter := time.Now().Add(5 * day)
	key := fmt.Sprintf("%s%d", sslAlertPrefix(target.ID), notAfter.Unix())

	// This certificate was last reported at the 14 day threshold.
	if err := saveSetting(key, "14"); err != nil {
		t.Fatal(err)
	}
	warnCertExpiry(target, notAfter)
	warnCertExpiry(target, notAfter)
	if len(*sent) != 1 || !strings.Contains((*sent)[0], "expires in") {
		t.Fatalf("alerts after crossing a threshold = %q", *sent)
	}
	if got := getSetting(key); got != "7" {
		t.Fatalf("stored level = %q, want 7", got)
	}
}

func TestWarnCertExpiryIgnoresHealthyCertificate(t *testing.T) {
	sent := captureAlerts(t, nil)
	warnCertExpiry(&Target{ID: "healthy", Host: "example.test"}, time.Now().Add(90*day))
	if len(*sent) != 0 {
		t.Fatalf("healthy certificate alerted: %q", *sent)
	}
}

func TestWarnCertExpiryRetriesFailedDelivery(t *testing.T) {
	delivered := false
	sent := captureAlerts(t, &delivered)
	target := &Target{ID: "retry", Name: "Site", Host: "example.test"}
	notAfter := time.Now().Add(3 * day)

	warnCertExpiry(target, notAfter)
	delivered = true
	warnCertExpiry(target, notAfter)
	warnCertExpiry(target, notAfter)
	if len(*sent) != 2 {
		t.Fatalf("got %d attempts, want 1 failed and 1 delivered: %q", len(*sent), *sent)
	}
}

func TestWarnCertExpiryTracksEachCertificateSeparately(t *testing.T) {
	sent := captureAlerts(t, nil)
	target := &Target{ID: "pair", Name: "Site", Host: "example.test"}
	first := time.Now().Add(10 * day)
	second := time.Now().Add(25 * day)

	for i := 0; i < 3; i++ {
		warnCertExpiry(target, first)
		warnCertExpiry(target, second)
	}
	if len(*sent) != 2 {
		t.Fatalf("alternating certificates sent %d alerts, want 2: %q", len(*sent), *sent)
	}
}

func TestDeleteTargetRemovesSSLAlertState(t *testing.T) {
	captureAlerts(t, nil)
	target := &Target{ID: "removed", Host: "example.test"}
	warnCertExpiry(target, time.Now().Add(3*day))

	if err := DeleteTarget(target.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM settings WHERE key LIKE ?", sslAlertPrefix(target.ID)+"%").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("%d alert rows left after delete", count)
	}
}

func TestCheckSSLReportsExpiredCertificate(t *testing.T) {
	sent := captureAlerts(t, nil)
	addr := serveCertificate(t, time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour))

	checkSSL(&Target{ID: "expired", Name: "Old", Host: addr, Type: "ssl"})
	if len(*sent) != 1 || !strings.Contains((*sent)[0], "EXPIRED") {
		t.Fatalf("expired certificate alerts = %q", *sent)
	}
}

func TestCheckSSLDoesNotCallUntrustedCertificateExpired(t *testing.T) {
	sent := captureAlerts(t, nil)
	addr := serveCertificate(t, time.Now().Add(-time.Hour), time.Now().Add(90*day))

	checkSSL(&Target{ID: "untrusted", Name: "Self", Host: addr, Type: "ssl"})
	if len(*sent) != 0 {
		t.Fatalf("untrusted but unexpired certificate alerted: %q", *sent)
	}
}

// serveCertificate runs a local TLS listener with a self-signed certificate.
func serveCertificate(t *testing.T, notBefore, notAfter time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.(*tls.Conn).Handshake()
			}()
		}
	}()
	return listener.Addr().String()
}
