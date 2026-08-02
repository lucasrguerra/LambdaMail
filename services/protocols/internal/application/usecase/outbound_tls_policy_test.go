package usecase

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"lambdamail/protocols/internal/domain/entity"
)

// startPlaintextMX serves an SMTP conversation that never offers STARTTLS,
// which is what a downgrade attack looks like from our side.
func startPlaintextMX(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				r := bufio.NewReader(c)
				c.Write([]byte("220 plain.mx ESMTP\r\n"))
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					cmd := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(cmd, "EHLO"):
						// Deliberately no STARTTLS in the extension list.
						c.Write([]byte("250-plain.mx\r\n250 SIZE 10240000\r\n"))
					case cmd == "DATA":
						c.Write([]byte("354 End data with <CR><LF>.<CR><LF>\r\n"))
						for {
							body, err := r.ReadString('\n')
							if err != nil {
								return
							}
							if strings.TrimSpace(body) == "." {
								break
							}
						}
						c.Write([]byte("250 2.0.0 Ok: queued\r\n"))
					case strings.HasPrefix(cmd, "QUIT"):
						c.Write([]byte("221 Bye\r\n"))
						return
					default:
						c.Write([]byte("250 Ok\r\n"))
					}
				}
			}(conn)
		}
	}()

	return ln.Addr().String()
}

type stubPolicyResolver struct {
	policy entity.TLSPolicy
	err    error
}

func (s stubPolicyResolver) Resolve(_ context.Context, _ string, _ string) (entity.TLSPolicy, error) {
	return s.policy, s.err
}

func runWorkerWithPolicy(t *testing.T, mxAddr string, resolver stubPolicyResolver) *entity.OutboundJob {
	t.Helper()

	repo := &fakeOutboundRepo{}
	repo.Enqueue(context.Background(), newTestJob())

	worker := NewOutboundWorkerUseCase(
		repo,
		&fakeMXResolver{hosts: []string{mxAddr}},
		&fakeBlobReader{payload: []byte("From: a\r\nTo: b\r\n\r\nTest")},
		nil, nil, "mail.local",
	)
	worker.SetPolicyResolver(resolver)

	if _, err := worker.ProcessBatch(context.Background(), "w1", 10); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	return repo.jobs[0]
}

// PLAN.md section 6.2: when the destination's policy demands validated TLS
// and it cannot be had, the message is deferred - never delivered in the
// clear. This is the anti-downgrade guarantee.
func TestOutboundWorker_DefersRatherThanDowngradeUnderEnforcePolicy(t *testing.T) {
	addr := startPlaintextMX(t)

	got := runWorkerWithPolicy(t, addr, stubPolicyResolver{
		policy: entity.NewMtaStsPolicy([]string{"*"}, true),
	})

	if got.Status != entity.OutboundJobStatusDeferred {
		t.Errorf("status = %s, want DEFERRED", got.Status)
	}
	if got.LastSmtpCode != "451" {
		t.Errorf("LastSmtpCode = %q, want 451", got.LastSmtpCode)
	}
	if !strings.Contains(got.LastError, "4.7.5") {
		t.Errorf("LastError should carry the 4.7.5 status: %q", got.LastError)
	}
	if got.TlsPolicyUsed != "none" {
		t.Errorf("TlsPolicyUsed = %q, want none", got.TlsPolicyUsed)
	}
}

// The same MX, with no policy published, is delivered to over plaintext:
// refusing here would lose legitimate mail (RFC 7435).
func TestOutboundWorker_DeliversOpportunisticallyWithoutPolicy(t *testing.T) {
	addr := startPlaintextMX(t)

	got := runWorkerWithPolicy(t, addr, stubPolicyResolver{
		policy: entity.NewTLSPolicy(false, false),
	})

	if got.Status != entity.OutboundJobStatusDelivered {
		t.Errorf("status = %s, want DELIVERED (last error: %s)", got.Status, got.LastError)
	}
	if got.TlsPolicyUsed != entity.TLSModeOpportunistic {
		t.Errorf("TlsPolicyUsed = %q, want opportunistic", got.TlsPolicyUsed)
	}
}

// An MX outside the policy's mx set must not be used at all
// (RFC 8461 section 4.1).
func TestOutboundWorker_RefusesMxOutsidePolicy(t *testing.T) {
	addr := startPlaintextMX(t)

	got := runWorkerWithPolicy(t, addr, stubPolicyResolver{
		policy: entity.NewMtaStsPolicy([]string{"only.allowed.test"}, true),
	})

	if got.Status != entity.OutboundJobStatusDeferred {
		t.Errorf("status = %s, want DEFERRED", got.Status)
	}
	if !strings.Contains(got.LastError, "not listed in the MTA-STS policy") {
		t.Errorf("LastError = %q, want it to name the policy mismatch", got.LastError)
	}
}

// A policy that exists but cannot be evaluated is a reason to wait, not to
// downgrade.
func TestOutboundWorker_DefersWhenPolicyCannotBeResolved(t *testing.T) {
	addr := startPlaintextMX(t)

	got := runWorkerWithPolicy(t, addr, stubPolicyResolver{
		err: fmt.Errorf("TLSA records are not DNSSEC-validated"),
	})

	if got.Status != entity.OutboundJobStatusDeferred {
		t.Errorf("status = %s, want DEFERRED", got.Status)
	}
	if !strings.Contains(got.LastError, "DNSSEC") {
		t.Errorf("LastError = %q, want the resolution failure to be recorded", got.LastError)
	}
}

// A testing-mode policy reports but must not block delivery.
func TestOutboundWorker_TestingModePolicyStillDelivers(t *testing.T) {
	addr := startPlaintextMX(t)

	got := runWorkerWithPolicy(t, addr, stubPolicyResolver{
		policy: entity.NewMtaStsPolicy([]string{"only.allowed.test"}, false),
	})

	if got.Status != entity.OutboundJobStatusDelivered {
		t.Errorf("status = %s, want DELIVERED under mode: testing (last error: %s)", got.Status, got.LastError)
	}
	if got.TlsPolicyUsed != entity.TLSModeMtaSts {
		t.Errorf("TlsPolicyUsed = %q, want mta-sts", got.TlsPolicyUsed)
	}
}
