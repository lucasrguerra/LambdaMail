package valueobject

import (
	"fmt"
	"strings"
	"time"
)

type DsnAction string

const (
	DsnActionFailed  DsnAction = "failed"
	DsnActionDelayed DsnAction = "delayed"
)

// defaultReportingMTA is the last resort when no mail host was configured. It
// is deliberately obviously wrong rather than plausible, so a misconfiguration
// shows up in the report instead of hiding in it.
const defaultReportingMTA = "mail.lambdamail.local"

// BuildDsnReport constructs an RFC 3464 compliant delivery status notification
// report payload.
//
// reportingMTA is this server's own mail host. It has to be the real one: a
// notification whose From and Reporting-MTA name a domain that does not
// resolve is itself undeliverable at the next hop, which loses exactly the
// message the sender most needs to see.
//
// Returns (payload, isLoopPrevented). If envelopeFrom is empty or "<>",
// isLoopPrevented is true and payload is nil.
func BuildDsnReport(action DsnAction, reportingMTA string, envelopeFrom string, envelopeTo string, messageID string, reason string) ([]byte, bool) {
	cleanFrom := strings.Trim(strings.TrimSpace(envelopeFrom), "<>")
	if cleanFrom == "" {
		return nil, true // Loop prevention per RFC 5321 section 4.5.5
	}

	reportingMTA = strings.TrimSpace(reportingMTA)
	if reportingMTA == "" {
		reportingMTA = defaultReportingMTA
	}

	boundary := fmt.Sprintf("=_Boundary_DSN_%d", time.Now().UnixNano())
	nowStr := time.Now().Format(time.RFC1123Z)

	status := "5.0.0"
	subject := "Undeliverable Mail Notification"
	if action == DsnActionDelayed {
		status = "4.4.1"
		subject = "Delivery Delay Notification"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("From: Mail Delivery Subsystem <postmaster@%s>\r\n", reportingMTA))
	sb.WriteString(fmt.Sprintf("To: <%s>\r\n", cleanFrom))
	sb.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	sb.WriteString(fmt.Sprintf("Date: %s\r\n", nowStr))
	sb.WriteString(fmt.Sprintf("Message-ID: <dsn-%s@%s>\r\n", messageID, reportingMTA))
	// RFC 3834 section 5: an automatic notification has to say so, or a
	// vacation responder on the other side answers it and the two bounce back
	// and forth forever.
	sb.WriteString("Auto-Submitted: auto-replied\r\n")
	sb.WriteString(fmt.Sprintf("Content-Type: multipart/report; report-type=delivery-status; boundary=\"%s\"\r\n\r\n", boundary))

	// Part 1: Human-readable narrative
	sb.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	if action == DsnActionFailed {
		sb.WriteString(fmt.Sprintf("This is an automated message from LambdaMail.\r\nYour message to <%s> could not be delivered.\r\nReason: %s\r\n\r\n", envelopeTo, reason))
	} else {
		sb.WriteString(fmt.Sprintf("This is an automated warning message from LambdaMail.\r\nDelivery to <%s> has been delayed.\r\nLambdaMail will continue attempting delivery.\r\nReason: %s\r\n\r\n", envelopeTo, reason))
	}

	// Part 2: Machine-readable delivery-status
	sb.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	sb.WriteString("Content-Type: message/delivery-status\r\n\r\n")
	sb.WriteString(fmt.Sprintf("Reporting-MTA: dns; %s\r\n", reportingMTA))
	sb.WriteString(fmt.Sprintf("Arrival-Date: %s\r\n\r\n", nowStr))

	sb.WriteString(fmt.Sprintf("Final-Recipient: rfc822; %s\r\n", envelopeTo))
	sb.WriteString(fmt.Sprintf("Action: %s\r\n", action))
	sb.WriteString(fmt.Sprintf("Status: %s\r\n", status))
	sb.WriteString(fmt.Sprintf("Diagnostic-Code: smtp; %s\r\n\r\n", reason))

	sb.WriteString(fmt.Sprintf("--%s--\r\n", boundary))

	return []byte(sb.String()), false
}
