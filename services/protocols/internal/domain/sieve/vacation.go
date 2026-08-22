package sieve

import "strings"

// shouldAutoReply decides whether a message may be answered automatically.
//
// This is the part of a vacation responder that matters. Replying to the wrong
// thing is how a mailbox answers a mailing list in front of everyone, or ends
// up in a loop with another autoresponder that replies to the reply. RFC 3834
// is the rule: answer a message a person actually sent, and nothing else.
func shouldAutoReply(msg Message) bool {
	// An empty envelope sender is a bounce or a notification. Replying to one
	// sends mail to nobody, or worse, back into a loop.
	if strings.TrimSpace(msg.Sender) == "" {
		return false
	}

	// Never answer yourself: the Sent copy and any self-addressed message.
	if msg.Recipient != "" && strings.EqualFold(msg.Sender, msg.Recipient) {
		return false
	}

	local := strings.ToLower(msg.Sender)
	if at := strings.Index(local, "@"); at > 0 {
		local = local[:at]
	}
	// Addresses that exist to carry automated mail. Answering one is at best
	// noise and at worst a loop.
	//
	// The prefix is matched against every separator a real sender uses, not
	// just "+". Google sends its DMARC reports from
	// noreply-dmarc-support@google.com: checking only for the exact word or a
	// "+" suffix let that through, and this mailbox would have auto-replied to
	// every daily report it receives.
	for _, reserved := range []string{
		"mailer-daemon", "mailerdaemon", "postmaster", "no-reply", "noreply",
		"donotreply", "do-not-reply", "bounce", "bounces", "notification",
		"notifications", "automated", "auto-reply", "autoreply",
	} {
		if local == reserved {
			return false
		}
		for _, separator := range []string{"+", "-", ".", "_"} {
			if strings.HasPrefix(local, reserved+separator) {
				return false
			}
		}
	}

	// Headers that say, in one way or another, "this was not typed by a
	// person" or "do not answer this".
	if len(msg.header("auto-submitted")) > 0 {
		for _, v := range msg.header("auto-submitted") {
			if !strings.EqualFold(strings.TrimSpace(v), "no") {
				return false
			}
		}
	}
	for _, header := range []string{
		"list-id", "list-help", "list-unsubscribe", "list-post",
		"x-auto-response-suppress", "x-autoreply", "x-autorespond",
	} {
		if len(msg.header(header)) > 0 {
			return false
		}
	}
	for _, v := range msg.header("precedence") {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "bulk", "list", "junk":
			return false
		}
	}

	return true
}
