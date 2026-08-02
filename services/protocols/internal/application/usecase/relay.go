package usecase

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

const relayDialTimeout = 15 * time.Second

// RelayConfig is the smarthost of PLAN.md section 10.4, used when the host's
// outbound port 25 is blocked. In this mode the relay takes responsibility for
// transport security to the final destination, so DANE and MTA-STS are not
// evaluated here - they would only describe the wrong hop.
type RelayConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	// SpfMechanism is the SPF term that authorises this relay, as the
	// provider documents it ("include:spf.brevo.com"). It has to be given
	// because it cannot be derived - see SpfInclude.
	SpfMechanism string
	// RootCAs trusts an internal relay presenting a certificate from a
	// private CA. Left nil, the system trust store is used. There is
	// deliberately no option to skip verification: the credentials sent over
	// this connection are worth protecting.
	RootCAs *x509.CertPool
}

// Configured reports whether delivery should go through the relay.
func (c RelayConfig) Configured() bool {
	return c.Host != ""
}

// Address renders the dial target, defaulting to the submission port.
func (c RelayConfig) Address() string {
	port := c.Port
	if port == 0 {
		port = 587
	}
	return net.JoinHostPort(c.Host, strconv.Itoa(port))
}

// SpfInclude renders the mechanism to add to the domain's SPF record. Without
// it the relay's own IP is not authorised to send for the domain and every
// message it forwards fails SPF (PLAN.md section 10.4).
//
// SpfMechanism is used when set, and it normally has to be: the provider's
// documented include is rarely derivable from the relay hostname. Brevo is the
// example that proves it - the relay is smtp-relay.brevo.com, but brevo.com
// publishes the SPF of their *corporate* mail (Google Workspace and Zendesk),
// while the sending ranges live at spf.brevo.com. Reducing the host to its
// organisational domain would authorise Google to send for the domain and not
// the relay, so every message it forwarded would fail SPF - and against a
// "-all" policy, be rejected outright.
//
// Falling back to that reduction is still better than publishing nothing, so
// it remains for relays whose hostname does happen to match, but a deployment
// should set RELAY_SPF_INCLUDE to whatever its provider documents.
func (c RelayConfig) SpfInclude() string {
	if !c.Configured() {
		return ""
	}
	if mechanism := strings.TrimSpace(c.SpfMechanism); mechanism != "" {
		// Accepted with or without the "include:" prefix, and any other SPF
		// term ("a:", "ip4:", "mx:") is passed through untouched.
		if strings.Contains(mechanism, ":") {
			return mechanism
		}
		return "include:" + mechanism
	}
	host := strings.TrimSuffix(strings.ToLower(c.Host), ".")
	labels := strings.Split(host, ".")
	if len(labels) > 2 {
		host = strings.Join(labels[len(labels)-2:], ".")
	}
	return "include:" + host
}

// authFor selects the SASL mechanism to use against the relay. PLAIN is
// preferred, and CRAM-MD5 is deliberately not offered: it requires the server
// to store a reversible secret.
func (c RelayConfig) authFor(serverName string) smtp.Auth {
	if c.Username == "" {
		return nil
	}
	return smtp.PlainAuth("", c.Username, c.Password, serverName)
}

// relayTLSConfig requires a valid certificate. Unlike opportunistic delivery
// to an arbitrary MX, the relay is a host we chose and configured, so there is
// no reason to accept an unverified certificate - and the credentials sent
// over this connection make it worth refusing.
func relayTLSConfig(relay RelayConfig) *tls.Config {
	return &tls.Config{
		ServerName: relay.Host,
		MinVersion: tls.VersionTLS12,
		RootCAs:    relay.RootCAs,
	}
}

// deliverViaRelay hands one message to the smarthost.
func (w *OutboundWorkerUseCase) deliverViaRelay(from, to string, payload []byte) error {
	relay := w.relay
	addr := relay.Address()

	conn, err := net.DialTimeout("tcp", addr, relayDialTimeout)
	if err != nil {
		return fmt.Errorf("dial relay %s: %w", addr, err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, relay.Host)
	if err != nil {
		return fmt.Errorf("relay handshake: %w", err)
	}
	defer client.Close()

	if err := client.Hello(w.localHost); err != nil {
		return fmt.Errorf("relay EHLO failed: %w", err)
	}

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(relayTLSConfig(relay)); err != nil {
			return fmt.Errorf("relay STARTTLS failed: %w", err)
		}
	} else if relay.Username != "" {
		// Sending the relay credentials over a cleartext link would leak them
		// to anyone on the path, which is worse than deferring the message.
		return fmt.Errorf("relay %s does not offer STARTTLS and credentials must not be sent in the clear", addr)
	}

	if auth := relay.authFor(relay.Host); auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("relay authentication failed: %w", err)
		}
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("relay MAIL FROM failed: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("relay RCPT TO failed: %w", err)
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("relay DATA command failed: %w", err)
	}
	if _, err := writer.Write(payload); err != nil {
		writer.Close()
		return fmt.Errorf("relay write DATA failed: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("relay close DATA failed: %w", err)
	}

	return client.Quit()
}
