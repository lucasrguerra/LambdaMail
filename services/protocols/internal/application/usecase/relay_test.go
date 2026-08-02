package usecase

import (
	"bufio"
	"context"
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
	"testing"
	"time"

	"lambdamail/protocols/internal/domain/entity"
)

// fakeRelay serves one SMTP conversation with STARTTLS and AUTH, recording
// what the client sent.
type fakeRelay struct {
	addr        string
	authSeen    chan string
	mailFrom    chan string
	offerTLS    bool
	certificate tls.Certificate
}

func startFakeRelay(t *testing.T, offerTLS bool) *fakeRelay {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	relay := &fakeRelay{
		addr:     ln.Addr().String(),
		authSeen: make(chan string, 1),
		mailFrom: make(chan string, 1),
		offerTLS: offerTLS,
	}
	if offerTLS {
		relay.certificate = relayTestCertificate(t)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go relay.serve(conn)
		}
	}()

	return relay
}

func (f *fakeRelay) serve(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	r := bufio.NewReader(conn)
	conn.Write([]byte("220 relay.test ESMTP\r\n"))

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))

		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			if f.offerTLS {
				conn.Write([]byte("250-relay.test\r\n250-STARTTLS\r\n250 AUTH PLAIN LOGIN\r\n"))
			} else {
				conn.Write([]byte("250-relay.test\r\n250 AUTH PLAIN LOGIN\r\n"))
			}

		case cmd == "STARTTLS":
			conn.Write([]byte("220 Ready to start TLS\r\n"))
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{f.certificate}})
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			r = bufio.NewReader(conn)

		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			payload := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line)[len("AUTH PLAIN"):], " "))
			if decoded, err := base64.StdEncoding.DecodeString(payload); err == nil {
				select {
				case f.authSeen <- strings.ReplaceAll(string(decoded), "\x00", "|"):
				default:
				}
			}
			conn.Write([]byte("235 2.7.0 Authentication successful\r\n"))

		case strings.HasPrefix(cmd, "MAIL FROM"):
			select {
			case f.mailFrom <- strings.TrimSpace(line):
			default:
			}
			conn.Write([]byte("250 Ok\r\n"))

		case cmd == "DATA":
			conn.Write([]byte("354 Send it\r\n"))
			for {
				body, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimSpace(body) == "." {
					break
				}
			}
			conn.Write([]byte("250 2.0.0 Queued\r\n"))

		case cmd == "QUIT":
			conn.Write([]byte("221 Bye\r\n"))
			return

		default:
			conn.Write([]byte("250 Ok\r\n"))
		}
	}
}

func relayWorker(t *testing.T, relay RelayConfig) (*OutboundWorkerUseCase, *fakeOutboundRepo) {
	t.Helper()

	repo := &fakeOutboundRepo{}
	repo.Enqueue(context.Background(), newTestJob())

	worker := NewOutboundWorkerUseCase(
		repo,
		// A resolver that would fail loudly if it were consulted: with a
		// relay configured, MX resolution must not happen at all.
		&fakeMXResolver{hosts: []string{"127.0.0.1:1"}},
		&fakeBlobReader{payload: []byte("From: a\r\nTo: b\r\n\r\nTest")},
		nil, nil, "mail.local",
	)
	worker.SetRelay(relay)
	return worker, repo
}

// PLAN.md section 10.4: with a smarthost every message leaves through the
// relay, whatever the destination domain is.
func TestOutboundWorker_DeliversThroughRelayInsteadOfMX(t *testing.T) {
	relay := startFakeRelay(t, true)
	host, portStr, _ := net.SplitHostPort(relay.addr)

	worker, repo := relayWorker(t, RelayConfig{
		Host:     host,
		Port:     atoiOrZero(portStr),
		Username: "relay-user",
		Password: "relay-pass",
		RootCAs:  relayTestRoots,
	})

	if _, err := worker.ProcessBatch(context.Background(), "w1", 10); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	job := repo.jobs[0]
	if job.Status != entity.OutboundJobStatusDelivered {
		t.Fatalf("status = %s, want DELIVERED (last error: %s)", job.Status, job.LastError)
	}
	if job.TlsPolicyUsed != entity.TLSModeRelay {
		t.Errorf("TlsPolicyUsed = %q, want relay", job.TlsPolicyUsed)
	}

	select {
	case credentials := <-relay.authSeen:
		if !strings.Contains(credentials, "relay-user") || !strings.Contains(credentials, "relay-pass") {
			t.Errorf("relay credentials = %q", credentials)
		}
	case <-time.After(2 * time.Second):
		t.Error("the worker never authenticated against the relay")
	}

	select {
	case from := <-relay.mailFrom:
		if !strings.Contains(from, "user@domain.test") {
			t.Errorf("MAIL FROM = %q, want the original envelope sender", from)
		}
	case <-time.After(2 * time.Second):
		t.Error("the relay never received MAIL FROM")
	}
}

// Sending the relay credentials over a cleartext link would leak them; the
// message is deferred instead.
func TestOutboundWorker_RefusesRelayCredentialsWithoutStartTLS(t *testing.T) {
	relay := startFakeRelay(t, false)
	host, portStr, _ := net.SplitHostPort(relay.addr)

	worker, repo := relayWorker(t, RelayConfig{
		Host: host, Port: atoiOrZero(portStr),
		Username: "relay-user", Password: "relay-pass",
	})

	if _, err := worker.ProcessBatch(context.Background(), "w1", 10); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	job := repo.jobs[0]
	if job.Status != entity.OutboundJobStatusDeferred {
		t.Errorf("status = %s, want DEFERRED", job.Status)
	}
	if !strings.Contains(job.LastError, "STARTTLS") {
		t.Errorf("LastError = %q, want it to name the missing STARTTLS", job.LastError)
	}
}

// An unreachable relay defers rather than bounces: the relay may come back.
func TestOutboundWorker_UnreachableRelayDefers(t *testing.T) {
	worker, repo := relayWorker(t, RelayConfig{Host: "127.0.0.1", Port: 1})

	if _, err := worker.ProcessBatch(context.Background(), "w1", 10); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	if job := repo.jobs[0]; job.Status != entity.OutboundJobStatusDeferred {
		t.Errorf("status = %s, want DEFERRED", job.Status)
	}
}

func TestRelayConfig_AddressDefaultsToSubmissionPort(t *testing.T) {
	if got := (RelayConfig{Host: "relay.example.net"}).Address(); got != "relay.example.net:587" {
		t.Errorf("Address = %q, want relay.example.net:587", got)
	}
	if got := (RelayConfig{Host: "relay.example.net", Port: 2525}).Address(); got != "relay.example.net:2525" {
		t.Errorf("Address = %q, want relay.example.net:2525", got)
	}
}

// PLAN.md section 10.4: the published SPF must authorise the relay, otherwise
// the hard fail rejects everything the relay forwards.
func TestRelayConfig_SpfInclude(t *testing.T) {
	cases := map[string]string{
		"smtp.sendgrid.net": "include:sendgrid.net",
		"relay.example.net": "include:example.net",
		"example.net":       "include:example.net",
		"":                  "",
	}

	for host, want := range cases {
		if got := (RelayConfig{Host: host}).SpfInclude(); got != want {
			t.Errorf("SpfInclude(%q) = %q, want %q", host, got, want)
		}
	}
}

func atoiOrZero(s string) int {
	value := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		value = value*10 + int(c-'0')
	}
	return value
}

// relayTestCertificate issues a certificate for 127.0.0.1 and registers it as
// a trusted root for the test, so the relay connection is verified for real
// rather than with verification switched off.
func relayTestCertificate(t *testing.T) tls.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "relay.test"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"127.0.0.1", "relay.test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}

	relayTestRoots = x509.NewCertPool()
	relayTestRoots.AddCert(parsed)

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// relayTestRoots holds the CA pool for the certificate issued above.
var relayTestRoots *x509.CertPool

// The provider's documented include is not derivable from the relay hostname,
// and guessing it wrong is worse than useless against a "-all" policy.
//
// Brevo is the case that proves it: the relay is smtp-relay.brevo.com, but
// brevo.com publishes the SPF of their corporate mail (Google Workspace,
// Zendesk) while the sending ranges live at spf.brevo.com. Reducing the host
// to its organisational domain authorises Google to send for the domain and
// not the relay.
func TestRelayConfig_SpfIncludeUsesTheConfiguredMechanism(t *testing.T) {
	relay := RelayConfig{Host: "smtp-relay.brevo.com", SpfMechanism: "include:spf.brevo.com"}
	if got := relay.SpfInclude(); got != "include:spf.brevo.com" {
		t.Errorf("SpfInclude() = %q, want the configured mechanism", got)
	}

	// The reduction would have produced this, which is the wrong answer.
	if got := (RelayConfig{Host: "smtp-relay.brevo.com"}).SpfInclude(); got != "include:brevo.com" {
		t.Errorf("fallback = %q, want include:brevo.com", got)
	}
}

func TestRelayConfig_SpfMechanismAcceptsBareDomainsAndOtherTerms(t *testing.T) {
	cases := map[string]string{
		"spf.brevo.com":         "include:spf.brevo.com",
		"include:spf.brevo.com": "include:spf.brevo.com",
		"ip4:198.51.100.0/24":   "ip4:198.51.100.0/24",
		"a:relay.example.test":  "a:relay.example.test",
	}
	for mechanism, want := range cases {
		got := RelayConfig{Host: "relay.example.test", SpfMechanism: mechanism}.SpfInclude()
		if got != want {
			t.Errorf("SpfInclude(%q) = %q, want %q", mechanism, got, want)
		}
	}
}

// An unconfigured relay publishes nothing, or the SPF record would authorise
// a host that is not being used.
func TestRelayConfig_SpfIncludeEmptyWithoutRelay(t *testing.T) {
	if got := (RelayConfig{SpfMechanism: "include:spf.brevo.com"}).SpfInclude(); got != "" {
		t.Errorf("SpfInclude() = %q with no relay host, want empty", got)
	}
}
