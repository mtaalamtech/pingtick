package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"time"
)

// sslWarningDays lists the days before expiry at which one alert is sent.
var sslWarningDays = []int{30, 14, 7, 1}

// sendSSLAlert reports whether the alert reached at least one channel.
var sendSSLAlert = sendNotifications

// CheckSSL checks if the SSL certificate for a host is valid and when it expires.
func checkSSL(t *Target) {
	if t.Type != "ssl" {
		return
	}

	host := t.Host
	// If no port specified, default to 443
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = host + ":443"
	}

	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", host, nil)
	if err != nil {
		log.Printf("SSL check failed for %s: %v", t.Host, err)
		updateTargetStatus(t.ID, "offline", 0, "", 0)
		if cert := expiredCertificate(err); cert != nil {
			warnCertExpiry(t, cert.NotAfter)
		}
		return
	}
	defer conn.Close()

	if len(conn.ConnectionState().PeerCertificates) == 0 {
		updateTargetStatus(t.ID, "offline", 0, "", 0)
		return
	}

	cert := conn.ConnectionState().PeerCertificates[0]
	daysUntilExpiry := int64(time.Until(cert.NotAfter).Hours() / 24)

	// Try resolve IP as well
	ip := ""
	if remoteAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		ip = remoteAddr.IP.String()
	}

	// For SSL targets the latency field holds the days left.
	updateTargetStatus(t.ID, "online", daysUntilExpiry, ip, 0)
	warnCertExpiry(t, cert.NotAfter)
}

// expiredCertificate returns the server's certificate when a failed handshake
// was caused by that certificate having expired, and nil otherwise.
func expiredCertificate(err error) *x509.Certificate {
	var verifyErr *tls.CertificateVerificationError
	if !errors.As(err, &verifyErr) || len(verifyErr.UnverifiedCertificates) == 0 {
		return nil
	}
	cert := verifyErr.UnverifiedCertificates[0]
	if time.Now().After(cert.NotAfter) {
		return cert
	}
	return nil
}

// sslWarningLevel returns the threshold in days that has been reached, or 0
// once the certificate has expired. ok is false if no threshold is reached.
func sslWarningLevel(left time.Duration) (level int, ok bool) {
	if left <= 0 {
		return 0, true
	}
	for i := len(sslWarningDays) - 1; i >= 0; i-- {
		if left <= time.Duration(sslWarningDays[i])*24*time.Hour {
			return sslWarningDays[i], true
		}
	}
	return 0, false
}

// certExpiringSoon reports whether a target's row should be highlighted.
func certExpiringSoon(t *Target) bool {
	return t.Type == "ssl" && t.Status == "online" && t.Latency < int64(sslWarningDays[0])
}

// warnCertExpiry sends one alert per threshold for each certificate. Progress
// is stored per certificate so a restart does not repeat an alert, and hosts
// behind a load balancer serving different certificates do not alternate
// between alerting and resetting. A failed delivery is retried on the next
// check because nothing is recorded until an alert has been delivered.
func warnCertExpiry(t *Target, notAfter time.Time) {
	left := time.Until(notAfter)
	level, ok := sslWarningLevel(left)
	if !ok {
		return
	}

	key := fmt.Sprintf("%s%d", sslAlertPrefix(t.ID), notAfter.Unix())
	if last, err := strconv.Atoi(getSetting(key)); err == nil && level >= last {
		return
	}

	msg := sslWarningMessage(t, notAfter, left)
	if !sendSSLAlert(msg) {
		return
	}
	log.Println(msg)
	if err := saveSetting(key, strconv.Itoa(level)); err != nil {
		log.Printf("Failed to record SSL alert for %s: %v", t.Host, err)
	}
}

func sslAlertPrefix(targetID string) string {
	return "ssl_alert:" + targetID + ":"
}

func sslWarningMessage(t *Target, notAfter time.Time, left time.Duration) string {
	date := notAfter.Local().Format("2006-01-02")
	if left <= 0 {
		return fmt.Sprintf("🚨 SSL Alert: The certificate for %s (%s) has EXPIRED. It expired on %s.", t.Name, t.Host, date)
	}

	remaining := "less than a day"
	switch days := int(left.Hours() / 24); {
	case days == 1:
		remaining = "1 day"
	case days > 1:
		remaining = fmt.Sprintf("%d days", days)
	}
	return fmt.Sprintf("⚠️ SSL Warning: The certificate for %s (%s) expires in %s, on %s.", t.Name, t.Host, remaining, date)
}
