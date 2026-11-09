package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testSMTPUser = "alerts@example.test"
	testSMTPPass = "test-password"
)

// fakeSMTP is a small mail server that records what clients send it.
type fakeSMTP struct {
	cert        tls.Certificate
	implicitTLS bool

	mu         sync.Mutex
	password   string
	message    string
	recipients []string
}

func (s *fakeSMTP) accepted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.recipients...)
}

func (s *fakeSMTP) recorded() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.password, s.message
}

func (s *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{s.cert}}
	if s.implicitTLS {
		conn = tls.Server(conn, tlsConfig)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	reply := func(text string) { conn.Write([]byte(text + "\r\n")) }

	reply("220 fake ready")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(command, "EHLO"):
			if _, secure := conn.(*tls.Conn); secure {
				reply("250-fake\r\n250 AUTH LOGIN")
			} else {
				reply("250-fake\r\n250 STARTTLS")
			}
		case command == "STARTTLS":
			reply("220 go ahead")
			secure := tls.Server(conn, tlsConfig)
			if secure.Handshake() != nil {
				return
			}
			conn = secure
			reader = bufio.NewReader(conn)
		case strings.HasPrefix(command, "AUTH LOGIN"):
			reply("334 UGFzc3dvcmQ6") // "Password:"
			encoded, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			password, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
			s.mu.Lock()
			s.password = string(password)
			s.mu.Unlock()
			reply("235 ok")
		case strings.HasPrefix(command, "RCPT TO"):
			if strings.Contains(command, "REFUSED") {
				reply("550 no such user")
				continue
			}
			s.mu.Lock()
			s.recipients = append(s.recipients, strings.ToLower(command[strings.Index(command, "<")+1:strings.Index(command, ">")]))
			s.mu.Unlock()
			reply("250 ok")
		case command == "DATA":
			reply("354 send it")
			var message strings.Builder
			for {
				data, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if data == ".\r\n" {
					break
				}
				message.WriteString(data)
			}
			s.mu.Lock()
			s.message = message.String()
			s.mu.Unlock()
			reply("250 queued")
		case command == "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

func testSMTPCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "fake smtp"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func startFakeSMTP(t *testing.T, implicitTLS bool) (*fakeSMTP, string, *x509.CertPool) {
	t.Helper()
	cert, pool := testSMTPCertificate(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	server := &fakeSMTP{cert: cert, implicitTLS: implicitTLS}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(conn)
		}
	}()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	return server, port, pool
}

func useSMTPRoots(t *testing.T, pool *x509.CertPool) {
	t.Helper()
	original := smtpRootCAs
	smtpRootCAs = pool
	t.Cleanup(func() { smtpRootCAs = original })
}

func TestSendEmailAlertDeliversOverVerifiedConnection(t *testing.T) {
	for _, protocol := range []string{"starttls", "ssl"} {
		server, port, pool := startFakeSMTP(t, protocol == "ssl")
		useSMTPRoots(t, pool)

		if err := sendEmailAlert("127.0.0.1", port, testSMTPUser, testSMTPPass, testSMTPUser, "ops@example.test", protocol, "target down"); err != nil {
			t.Fatalf("%s: %v", protocol, err)
		}
		password, message := server.recorded()
		if password != testSMTPPass || !strings.Contains(message, "target down") {
			t.Fatalf("%s: password %q, message %q", protocol, password, message)
		}
	}
}

func TestSplitRecipients(t *testing.T) {
	got := splitRecipients(" a@x.test, b@x.test;c@x.test\r\n d@x.test ,, ")
	want := []string{"a@x.test", "b@x.test", "c@x.test", "d@x.test"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("recipients = %q", got)
	}
	if got := splitRecipients(" , ; "); len(got) != 0 {
		t.Fatalf("recipients = %q", got)
	}
}

func TestSendEmailAlertReachesEveryRecipient(t *testing.T) {
	server, port, pool := startFakeSMTP(t, false)
	useSMTPRoots(t, pool)

	err := sendEmailAlert("127.0.0.1", port, testSMTPUser, testSMTPPass, testSMTPUser, "one@x.test, two@x.test; three@x.test", "starttls", "target down")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(server.accepted(), "|"); got != "one@x.test|two@x.test|three@x.test" {
		t.Fatalf("accepted = %q", got)
	}
	if _, message := server.recorded(); !strings.Contains(message, "To: one@x.test, two@x.test, three@x.test\r\n") {
		t.Fatalf("message = %q", message)
	}
}

func TestDeliverEmailContinuesPastRefusedRecipient(t *testing.T) {
	server, port, pool := startFakeSMTP(t, false)
	useSMTPRoots(t, pool)

	rejected, err := deliverEmail("127.0.0.1", port, testSMTPUser, testSMTPPass, testSMTPUser, "one@x.test, refused@x.test, two@x.test", "starttls", "target down")
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 1 || rejected[0] != "refused@x.test" {
		t.Fatalf("rejected = %q", rejected)
	}
	if got := strings.Join(server.accepted(), "|"); got != "one@x.test|two@x.test" {
		t.Fatalf("accepted = %q", got)
	}
	if _, message := server.recorded(); !strings.Contains(message, "target down") {
		t.Fatal("message was not delivered to the accepted recipients")
	}
}

func TestDeliverEmailFailsWhenEveryRecipientIsRefused(t *testing.T) {
	server, port, pool := startFakeSMTP(t, false)
	useSMTPRoots(t, pool)

	if _, err := deliverEmail("127.0.0.1", port, testSMTPUser, testSMTPPass, testSMTPUser, "refused@x.test", "starttls", "x"); err == nil {
		t.Fatal("expected an error")
	}
	if _, message := server.recorded(); message != "" {
		t.Fatalf("message was sent: %q", message)
	}
}

func TestDeliverEmailNeedsARecipient(t *testing.T) {
	if _, err := deliverEmail("127.0.0.1", "1", testSMTPUser, testSMTPPass, testSMTPUser, " , ", "starttls", "x"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestSendEmailAlertRejectsUntrustedServerBeforeSendingPassword(t *testing.T) {
	for _, protocol := range []string{"starttls", "ssl"} {
		server, port, _ := startFakeSMTP(t, protocol == "ssl")
		useSMTPRoots(t, x509.NewCertPool()) // trusts nothing

		if err := sendEmailAlert("127.0.0.1", port, testSMTPUser, testSMTPPass, testSMTPUser, "ops@example.test", protocol, "target down"); err == nil {
			t.Fatalf("%s: untrusted server was accepted", protocol)
		}
		time.Sleep(50 * time.Millisecond)
		if password, _ := server.recorded(); password != "" {
			t.Fatalf("%s: password reached an untrusted server", protocol)
		}
	}
}

func TestSendEmailAlertRejectsWrongHostName(t *testing.T) {
	_, port, pool := startFakeSMTP(t, false)
	useSMTPRoots(t, pool)

	client, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Fatal(err)
	}
	client.Close()

	// The certificate is for 127.0.0.1, so "localhost" must not match it.
	if err := sendEmailAlert("localhost", port, testSMTPUser, testSMTPPass, testSMTPUser, "ops@example.test", "starttls", "x"); err == nil {
		t.Fatal("certificate for another host was accepted")
	}
}

func TestSendEmailAlertGivesUpOnSilentServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
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
			t.Cleanup(func() { conn.Close() }) // accepts and never replies
		}
	}()

	original := smtpTimeout
	smtpTimeout = 200 * time.Millisecond
	t.Cleanup(func() { smtpTimeout = original })

	_, port, _ := net.SplitHostPort(listener.Addr().String())
	start := time.Now()
	if err := sendEmailAlert("127.0.0.1", port, testSMTPUser, testSMTPPass, testSMTPUser, "ops@example.test", "starttls", "x"); err == nil {
		t.Fatal("silent server did not produce an error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("took %v to give up", elapsed)
	}
}

func TestTestNotificationReportsMissingEmailSettings(t *testing.T) {
	if err := sendTestNotification("email", "hello"); err == nil {
		t.Fatal("expected an error when no SMTP settings are saved")
	}
}
