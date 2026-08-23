package netdns

import "testing"

// Comparing a desired record against what a resolver actually answers.
//
// Every case here comes from a real zone where the console reported a record
// as missing while it was published and correct. Telling an operator that a
// record they can see in their DNS does not exist is worse than saying
// nothing: they go looking for a problem that is not there.

func TestMxIgnoresThePriorityAndTrailingDot(t *testing.T) {
	// The spec carries the host in Value and the priority in its own field, so
	// the desired value is the host alone. A resolver answers "10 mail.x." -
	// comparing the two as strings said the MX of a domain that receives mail
	// every day did not exist.
	if !answerMatchesValue("MX", "10 mail.lucasrguerra.dev.br.", "mail.lucasrguerra.dev.br") {
		t.Error("a published MX was reported as missing")
	}
	if !answerMatchesValue("MX", "20 mail.lucasrguerra.dev.br.", "mail.lucasrguerra.dev.br") {
		t.Error("the priority should not decide whether the host matches")
	}
	// A different host is still a real difference.
	if answerMatchesValue("MX", "10 outro.example.test.", "mail.lucasrguerra.dev.br") {
		t.Error("a different mail host was accepted")
	}
}

// A TXT value longer than 255 bytes is published as several strings, and a
// resolver renders them quoted and separated. Joining them with a space - which
// is what stripping the quotes and collapsing whitespace did - inserted a space
// into the middle of a DKIM public key, so every RSA DKIM record on the zone
// was reported as missing.
func TestLongTxtIsJoinedWithoutASeparator(t *testing.T) {
	published := `"v=DKIM1; k=rsa; p=AAAA" "BBBB"`
	if !answerMatchesValue("TXT", published, "v=DKIM1; k=rsa; p=AAAABBBB") {
		t.Error("a split TXT record was reported as missing")
	}
}

func TestShortTxtStillMatches(t *testing.T) {
	if !answerMatchesValue("TXT", `"v=spf1 mx -all"`, "v=spf1 mx -all") {
		t.Error("a single-string TXT was reported as missing")
	}
}

// The spaces inside a policy string are meaningful and must survive.
func TestTxtKeepsItsOwnSpaces(t *testing.T) {
	if !answerMatchesValue("TXT", `"v=spf1 include:_spf.example.test -all"`,
		"v=spf1 include:_spf.example.test -all") {
		t.Error("a TXT with real spaces stopped matching")
	}
	if answerMatchesValue("TXT", `"v=spf1 mx -all"`, "v=spf1 mx ~all") {
		t.Error("a genuinely different policy was accepted")
	}
}

func TestCnameIgnoresTheTrailingDot(t *testing.T) {
	if !answerMatchesValue("CNAME", "mail.lucasrguerra.dev.br.", "mail.lucasrguerra.dev.br") {
		t.Error("a published CNAME was reported as missing")
	}
}

func TestCaseDoesNotDecide(t *testing.T) {
	if !answerMatchesValue("CNAME", "MAIL.LUCASRGUERRA.DEV.BR.", "mail.lucasrguerra.dev.br") {
		t.Error("a name in another case was reported as missing")
	}
}

func TestAValueIsCompared(t *testing.T) {
	if !answerMatchesValue("A", "203.0.113.10", "203.0.113.10") {
		t.Error("a matching A record was reported as missing")
	}
	if answerMatchesValue("A", "203.0.113.99", "203.0.113.10") {
		t.Error("a different address was accepted")
	}
}

// A record behind the provider's proxy is not published as the type it was
// created as: a proxied CNAME answers A, carrying the proxy's own addresses.
// Asking for the CNAME therefore comes back empty, and the console reported
// the record as missing while it was there and serving traffic.
func TestProxiedCnameIsQueriedAsTheTypeTheProxyAnswers(t *testing.T) {
	proxied := queryTypeFor("CNAME", true)
	if proxied != "A" {
		t.Errorf("a proxied CNAME is queried as %s; the proxy answers A", proxied)
	}
	// Without the proxy it is still a CNAME question.
	if plain := queryTypeFor("CNAME", false); plain != "CNAME" {
		t.Errorf("an ordinary CNAME is queried as %s", plain)
	}
	// Other types are unaffected: a proxied A is still an A.
	if a := queryTypeFor("A", true); a != "A" {
		t.Errorf("a proxied A is queried as %s", a)
	}
}
