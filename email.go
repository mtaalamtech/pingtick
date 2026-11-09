package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/smtp"
	"time"
)

var (
	// smtpTimeout limits a whole delivery so a silent server cannot hang a check.
	smtpTimeout = 15 * time.Second
	// smtpRootCAs is nil in the app, which uses the Windows certificate store.
	smtpRootCAs *x509.CertPool
)

func smtpTLSConfig(host string) *tls.Config {
	return &tls.Config{ServerName: host, RootCAs: smtpRootCAs}
}

// dialSMTP connects and completes encryption, so a server whose certificate
// fails verification is rejected before any credentials are sent.
func dialSMTP(host, addr, protocol string) (*smtp.Client, error) {
	conn, err := net.DialTimeout("tcp", addr, smtpTimeout)
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Now().Add(smtpTimeout))

	if protocol == "ssl" {
		conn = tls.Client(conn, smtpTLSConfig(host))
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if protocol != "ssl" && protocol != "none" {
		if err := client.StartTLS(smtpTLSConfig(host)); err != nil {
			client.Close()
			return nil, err
		}
	}
	return client, nil
}

func authenticateSMTP(client *smtp.Client, user, pass, host, protocol string) error {
	err := client.Auth(LoginAuth(user, pass))
	if err == nil {
		return nil
	}
	plainErr := client.Auth(smtp.PlainAuth("", user, pass, host))
	if plainErr == nil || protocol == "none" {
		return nil
	}
	return err
}

func sendEmailAlert(host, port, user, pass, from, to, protocol, text string) error {
	subject := "Subject: PingTick Monitoring Alert\n"
	body := "MIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n" +
		"<h2>PingTick Alert Notification</h2><p>" + text + "</p>"
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\n%s%s", from, to, subject, body))

	client, err := dialSMTP(host, net.JoinHostPort(host, port), protocol)
	if err != nil {
		return err
	}
	defer client.Close()

	if err := authenticateSMTP(client, user, pass, host, protocol); err != nil {
		return err
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(msg); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
