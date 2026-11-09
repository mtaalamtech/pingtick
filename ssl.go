package main

import (
	"crypto/tls"
	"log"
	"net"
	"time"
)

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

	// If it expires in less than 7 days, we might want to flag it, but for now just latency field will hold "days left" for SSL
	updateTargetStatus(t.ID, "online", daysUntilExpiry, ip, 0)
}
