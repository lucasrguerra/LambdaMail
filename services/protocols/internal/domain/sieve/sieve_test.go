package sieve

import (
	"strings"
	"testing"
	"time"
)

// The rules a mailbox owner writes, actually applied to the mail that arrives.
//
// Until now nothing ran them. ManageSieve stored scripts, the settings screen
// wrote them, and every message was delivered as though no rule existed - so a
// vacation responder that was switched on never replied, and a filter that
// said "file this in Faturas" never filed anything anywhere.

func evaluate(t *testing.T, script string, msg Message) Outcome {
	t.Helper()
	parsed, err := Parse(script)
	if err != nil {
		t.Fatalf("parse: %v\nscript:\n%s", err, script)
	}
	return Evaluate(parsed, msg)
}

// The exact shape the settings screen writes.
const uiRule = `if header :contains "Subject" "Fatura" { fileinto "Faturas"; }`

func TestRuleFilesTheMessageInItsFolder(t *testing.T) {
	out := evaluate(t, uiRule, Message{Headers: map[string][]string{
		"subject": {"Fatura de agosto"},
	}})
	if out.Folder != "Faturas" {
		t.Errorf("filed into %q, want Faturas", out.Folder)
	}
	if out.Discard {
		t.Error("the message was discarded")
	}
}

func TestRuleThatDoesNotMatchLeavesTheMessageAlone(t *testing.T) {
	out := evaluate(t, uiRule, Message{Headers: map[string][]string{
		"subject": {"Almoço amanhã"},
	}})
	if out.Folder != "" {
		t.Errorf("an unmatched rule filed into %q", out.Folder)
	}
}

// Header matching is case-insensitive on the name, per RFC 5228: a header is
// "Subject" or "subject" depending on who sent the mail.
func TestHeaderNameIsCaseInsensitive(t *testing.T) {
	out := evaluate(t, `if header :contains "SUBJECT" "fatura" { fileinto "Faturas"; }`,
		Message{Headers: map[string][]string{"subject": {"Fatura de agosto"}}})
	if out.Folder != "Faturas" {
		t.Errorf("folder %q", out.Folder)
	}
}

func TestContainsIsCaseInsensitiveOnTheValue(t *testing.T) {
	out := evaluate(t, uiRule, Message{Headers: map[string][]string{
		"subject": {"FATURA DE AGOSTO"},
	}})
	if out.Folder != "Faturas" {
		t.Errorf("folder %q; :contains should not depend on case", out.Folder)
	}
}

func TestIsRequiresTheWholeValue(t *testing.T) {
	script := `if header :is "Subject" "Fatura" { fileinto "Faturas"; }`
	if out := evaluate(t, script, Message{Headers: map[string][]string{"subject": {"Fatura"}}}); out.Folder != "Faturas" {
		t.Errorf("exact match failed: %q", out.Folder)
	}
	if out := evaluate(t, script, Message{Headers: map[string][]string{"subject": {"Fatura de agosto"}}}); out.Folder != "" {
		t.Errorf(":is matched a longer value: %q", out.Folder)
	}
}

// :matches is Sieve's wildcard form - * and ? - and is what the interface
// should offer instead of asking a human for a regular expression.
func TestMatchesUsesWildcardsNotRegex(t *testing.T) {
	script := `if header :matches "From" "*@google.com" { fileinto "Relatórios"; }`
	if out := evaluate(t, script, Message{Headers: map[string][]string{
		"from": {"noreply-dmarc-support@google.com"},
	}}); out.Folder != "Relatórios" {
		t.Errorf("wildcard did not match: %q", out.Folder)
	}
	if out := evaluate(t, script, Message{Headers: map[string][]string{
		"from": {"someone@example.test"},
	}}); out.Folder != "" {
		t.Errorf("wildcard matched the wrong sender: %q", out.Folder)
	}
}

// A wildcard pattern must not be read as a regular expression: "." is a
// literal dot in Sieve, and treating it as "any character" would file mail
// the reader never asked to file.
func TestWildcardDotIsLiteral(t *testing.T) {
	out := evaluate(t, `if header :matches "From" "a.b@example.test" { fileinto "X"; }`,
		Message{Headers: map[string][]string{"from": {"axb@example.test"}}})
	if out.Folder != "" {
		t.Errorf("the dot was treated as a regex wildcard: %q", out.Folder)
	}
}

func TestAnyofMatchesWhenOneTestPasses(t *testing.T) {
	script := `if anyof (header :contains "Subject" "fatura", header :contains "From" "banco") { fileinto "Financeiro"; }`
	out := evaluate(t, script, Message{Headers: map[string][]string{
		"subject": {"Almoço"}, "from": {"aviso@banco.test"},
	}})
	if out.Folder != "Financeiro" {
		t.Errorf("anyof did not match: %q", out.Folder)
	}
}

func TestAllofRequiresEveryTest(t *testing.T) {
	script := `if allof (header :contains "Subject" "fatura", header :contains "From" "banco") { fileinto "Financeiro"; }`
	if out := evaluate(t, script, Message{Headers: map[string][]string{
		"subject": {"Fatura"}, "from": {"aviso@banco.test"},
	}}); out.Folder != "Financeiro" {
		t.Errorf("allof with both matching: %q", out.Folder)
	}
	if out := evaluate(t, script, Message{Headers: map[string][]string{
		"subject": {"Fatura"}, "from": {"amigo@example.test"},
	}}); out.Folder != "" {
		t.Errorf("allof matched with only one test true: %q", out.Folder)
	}
}

func TestElsifAndElse(t *testing.T) {
	script := `
if header :contains "Subject" "fatura" { fileinto "Financeiro"; }
elsif header :contains "Subject" "convite" { fileinto "Eventos"; }
else { fileinto "Outros"; }`

	cases := map[string]string{
		"Fatura de agosto": "Financeiro",
		"Convite de festa": "Eventos",
		"Qualquer coisa":   "Outros",
	}
	for subject, want := range cases {
		out := evaluate(t, script, Message{Headers: map[string][]string{"subject": {subject}}})
		if out.Folder != want {
			t.Errorf("%q filed into %q, want %q", subject, out.Folder, want)
		}
	}
}

// The first rule that files the message wins, so two rules cannot disagree
// about where one message ends up.
func TestFirstMatchingRuleDecidesTheFolder(t *testing.T) {
	script := `
if header :contains "Subject" "fatura" { fileinto "Financeiro"; }
if header :contains "Subject" "agosto" { fileinto "Arquivo"; }`
	out := evaluate(t, script, Message{Headers: map[string][]string{"subject": {"Fatura de agosto"}}})
	if out.Folder != "Financeiro" {
		t.Errorf("folder %q, want the first rule's Financeiro", out.Folder)
	}
}

func TestStopEndsEvaluation(t *testing.T) {
	script := `
if header :contains "Subject" "fatura" { fileinto "Financeiro"; stop; }
if header :contains "Subject" "agosto" { discard; }`
	out := evaluate(t, script, Message{Headers: map[string][]string{"subject": {"Fatura de agosto"}}})
	if out.Discard {
		t.Error("evaluation continued past stop and discarded the message")
	}
}

func TestDiscardIsHonoured(t *testing.T) {
	out := evaluate(t, `if header :contains "Subject" "spam" { discard; }`,
		Message{Headers: map[string][]string{"subject": {"spam barato"}}})
	if !out.Discard {
		t.Error("discard was ignored")
	}
}

func TestAddflagMarksTheMessage(t *testing.T) {
	out := evaluate(t, `if header :contains "From" "chefe" { addflag "\\Flagged"; }`,
		Message{Headers: map[string][]string{"from": {"chefe@example.test"}}})
	if len(out.Flags) != 1 || out.Flags[0] != `\Flagged` {
		t.Errorf("flags %v", out.Flags)
	}
}

// --- vacation ------------------------------------------------------------

const vacationScript = `require ["vacation"];
vacation :subject "Fora do escritório" "Estou de férias até dia 30.";`

func TestVacationProducesAReply(t *testing.T) {
	out := evaluate(t, vacationScript, Message{
		Headers: map[string][]string{"from": {"amigo@example.test"}, "subject": {"Oi"}},
		Sender:  "amigo@example.test",
	})
	if out.Vacation == nil {
		t.Fatal("no vacation reply was produced")
	}
	if out.Vacation.Subject != "Fora do escritório" {
		t.Errorf("subject %q", out.Vacation.Subject)
	}
	if !strings.Contains(out.Vacation.Body, "férias") {
		t.Errorf("body %q", out.Vacation.Body)
	}
}

// A vacation reply to a mailing list, a bounce or another autoresponder is how
// two servers end up replying to each other forever. RFC 3834 says an
// automatic reply goes only to a real person's message.
func TestVacationStaysSilentForAutomatedMail(t *testing.T) {
	quiet := []Message{
		{Sender: "", Headers: map[string][]string{}}, // bounce: empty envelope sender
		{Sender: "a@b.test", Headers: map[string][]string{"auto-submitted": {"auto-replied"}}},
		{Sender: "a@b.test", Headers: map[string][]string{"precedence": {"bulk"}}},
		{Sender: "a@b.test", Headers: map[string][]string{"list-id": {"<list.example.test>"}}},
		{Sender: "a@b.test", Headers: map[string][]string{"x-auto-response-suppress": {"All"}}},
		{Sender: "MAILER-DAEMON@b.test", Headers: map[string][]string{}},
	}
	for i, msg := range quiet {
		if out := evaluate(t, vacationScript, msg); out.Vacation != nil {
			t.Errorf("case %d: replied to automated mail", i)
		}
	}
}

func TestVacationDoesNotReplyToYourself(t *testing.T) {
	out := evaluate(t, vacationScript, Message{
		Sender:    "me@example.test",
		Recipient: "me@example.test",
		Headers:   map[string][]string{"from": {"me@example.test"}},
	})
	if out.Vacation != nil {
		t.Error("the responder replied to the mailbox's own message")
	}
}

// --- robustness ----------------------------------------------------------

// A script this parser cannot read must not stop the mail. Delivery is the
// thing that matters; a rule that does not run is a lesser failure than a
// message that never arrives.
func TestUnparseableScriptIsReportedNotFatal(t *testing.T) {
	if _, err := Parse(`if header :contains "Subject" {{{`); err == nil {
		t.Error("a malformed script should be reported as an error")
	}
}

func TestEmptyScriptDoesNothing(t *testing.T) {
	out := evaluate(t, "", Message{Headers: map[string][]string{"subject": {"x"}}})
	if out.Folder != "" || out.Discard || out.Vacation != nil {
		t.Errorf("an empty script did something: %+v", out)
	}
}

func TestCommentsAndBlankLinesAreIgnored(t *testing.T) {
	script := `
# uma regra qualquer
if header :contains "Subject" "fatura" { fileinto "Financeiro"; }   # fim
`
	out := evaluate(t, script, Message{Headers: map[string][]string{"subject": {"Fatura"}}})
	if out.Folder != "Financeiro" {
		t.Errorf("folder %q", out.Folder)
	}
}

// A folder name is used to route mail, so it must survive intact - including
// the accents a Portuguese folder name has.
func TestFolderNamesKeepTheirAccents(t *testing.T) {
	out := evaluate(t, `if header :contains "Subject" "x" { fileinto "Relatórios"; }`,
		Message{Headers: map[string][]string{"subject": {"x"}}})
	if out.Folder != "Relatórios" {
		t.Errorf("folder %q", out.Folder)
	}
}

// A script with thousands of rules must not become a way to stall delivery.
func TestEvaluationIsBounded(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		b.WriteString(`if header :contains "Subject" "nada" { fileinto "X"; }` + "\n")
	}
	if _, err := Parse(b.String()); err == nil {
		t.Error("an unreasonably large script should be refused rather than evaluated")
	}
}

// :matches is anchored: the pattern describes the whole value, not a piece of
// it. Without that, ":matches" would behave like ":contains" and a rule
// written to catch one exact address would quietly catch every address that
// contained it.
func TestMatchesIsAnchoredNotASubstring(t *testing.T) {
	script := `if header :matches "Subject" "fatura" { fileinto "X"; }`
	if out := evaluate(t, script, Message{Headers: map[string][]string{
		"subject": {"fatura"},
	}}); out.Folder != "X" {
		t.Errorf("the exact value did not match: %q", out.Folder)
	}
	if out := evaluate(t, script, Message{Headers: map[string][]string{
		"subject": {"minha fatura de agosto"},
	}}); out.Folder != "" {
		t.Errorf(":matches behaved like :contains: %q", out.Folder)
	}
}

// ? is exactly one character, which nothing else in the grammar provides.
func TestMatchesSingleCharacterWildcard(t *testing.T) {
	script := `if header :matches "Subject" "fatura-?" { fileinto "X"; }`
	if out := evaluate(t, script, Message{Headers: map[string][]string{
		"subject": {"fatura-1"},
	}}); out.Folder != "X" {
		t.Errorf("? did not match one character: %q", out.Folder)
	}
	if out := evaluate(t, script, Message{Headers: map[string][]string{
		"subject": {"fatura-12"},
	}}); out.Folder != "" {
		t.Errorf("? matched two characters: %q", out.Folder)
	}
	if out := evaluate(t, script, Message{Headers: map[string][]string{
		"subject": {"fatura-"},
	}}); out.Folder != "" {
		t.Errorf("? matched no character at all: %q", out.Folder)
	}
}

// A star in the middle has to match the run between two fixed parts.
func TestMatchesStarInTheMiddle(t *testing.T) {
	script := `if header :matches "Subject" "Relatório * de agosto" { fileinto "X"; }`
	if out := evaluate(t, script, Message{Headers: map[string][]string{
		"subject": {"Relatório mensal de agosto"},
	}}); out.Folder != "X" {
		t.Errorf("star in the middle did not match: %q", out.Folder)
	}
	if out := evaluate(t, script, Message{Headers: map[string][]string{
		"subject": {"Relatório mensal de julho"},
	}}); out.Folder != "" {
		t.Errorf("matched despite a different tail: %q", out.Folder)
	}
}

// A pattern of stars against a long value is where a naive matcher goes
// exponential. It must stay fast.
func TestMatchesDoesNotBlowUpOnManyStars(t *testing.T) {
	value := strings.Repeat("a", 3000) + "b"
	script := `if header :matches "Subject" "` + strings.Repeat("*a", 20) + `*c" { fileinto "X"; }`
	done := make(chan Outcome, 1)
	go func() {
		done <- evaluate(t, script, Message{Headers: map[string][]string{"subject": {value}}})
	}()
	select {
	case out := <-done:
		if out.Folder != "" {
			t.Errorf("matched what it should not: %q", out.Folder)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wildcard matching took more than three seconds; it is backtracking exponentially")
	}
}

// A backslash escapes a wildcard, per RFC 5228 section 2.7.1: "\*" is a real
// asterisk, not "any run of characters".
//
// This matters because the rules screen generates the wildcards itself. When
// someone writes a rule about a subject containing "50% *", the screen escapes
// their asterisk so it stays literal - and if this engine ignored the escape,
// that rule would match every subject starting with "50% " instead.
func TestEscapedWildcardIsALiteralCharacter(t *testing.T) {
	script := `if header :matches "Subject" "50% \\* desconto*" { fileinto "X"; }`

	if out := evaluate(t, script, Message{Headers: map[string][]string{
		"subject": {"50% * desconto de agosto"},
	}}); out.Folder != "X" {
		t.Errorf("the literal asterisk did not match: %q", out.Folder)
	}
	// The escaped asterisk must not behave as a wildcard.
	if out := evaluate(t, script, Message{Headers: map[string][]string{
		"subject": {"50% enorme desconto de agosto"},
	}}); out.Folder != "" {
		t.Errorf("the escaped asterisk was treated as a wildcard: %q", out.Folder)
	}
}

func TestEscapedQuestionMarkIsALiteralCharacter(t *testing.T) {
	script := `if header :matches "Subject" "Onde\\?" { fileinto "X"; }`
	if out := evaluate(t, script, Message{Headers: map[string][]string{
		"subject": {"Onde?"},
	}}); out.Folder != "X" {
		t.Errorf("the literal question mark did not match: %q", out.Folder)
	}
	if out := evaluate(t, script, Message{Headers: map[string][]string{
		"subject": {"OndeX"},
	}}); out.Folder != "" {
		t.Errorf("the escaped question mark matched any character: %q", out.Folder)
	}
}
