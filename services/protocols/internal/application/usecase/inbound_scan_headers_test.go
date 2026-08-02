package usecase

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/valueobject"
)

type capturingBlobStorage struct {
	stored []byte
}

func (c *capturingBlobStorage) Store(_ context.Context, r io.Reader) (port.BlobRef, error) {
	b, err := io.ReadAll(r)
	c.stored = b
	return port.BlobRef{ID: uuid.New(), SizeBytes: int64(len(b))}, err
}

type stubScanner struct {
	result *valueobject.ScanResult
}

func (s *stubScanner) Scan(_ context.Context, _ port.ScanInput) (*valueobject.ScanResult, error) {
	return s.result, nil
}

// The scan verdict is only actionable by the user - and by their Sieve rules -
// if it reaches the stored message. PLAN.md section 6.1 persists the EML
// together with the verdicts, so the spam headers the scanner produced must be
// present in the delivered message.
func TestUseCase_Handle_WritesScanHeadersIntoStoredMessage(t *testing.T) {
	blobs := &capturingBlobStorage{}
	scanner := &stubScanner{result: &valueobject.ScanResult{
		Verdict: valueobject.ScanVerdictSpamJunk,
		Score:   9.5,
		HeadersToAdd: map[string]string{
			"X-Spam-Flag":  "YES",
			"X-Spam-Score": "9.50",
		},
	}}

	uc := NewProcessInboundEmailUseCase(nil, blobs, &recordingMessageRepository{})
	uc.SetScanner(scanner)

	err := uc.Handle(context.Background(), ProcessInboundEmailInput{
		Sender:             "spammer@example.test",
		Recipients:         []port.MailboxRecord{{ID: uuid.New()}},
		RecipientAddresses: []string{"victim@example.test"},
		Body:               bytes.NewBufferString("Subject: buy now\r\n\r\nbody"),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	stored := string(blobs.stored)
	for _, want := range []string{"X-Spam-Flag: YES", "X-Spam-Score: 9.50"} {
		if !strings.Contains(stored, want) {
			t.Errorf("stored message is missing %q:\n%s", want, stored)
		}
	}
	if !strings.Contains(stored, "Subject: buy now") {
		t.Errorf("original headers were lost:\n%s", stored)
	}
	// Added headers must precede the original ones and stay inside the header
	// block, i.e. before the first empty line.
	headerBlock, _, found := strings.Cut(stored, "\r\n\r\n")
	if !found {
		t.Fatalf("stored message has no header/body separator:\n%s", stored)
	}
	if !strings.Contains(headerBlock, "X-Spam-Flag: YES") {
		t.Errorf("scan headers leaked into the body:\n%s", stored)
	}
}

func TestUseCase_Handle_LeavesMessageUntouchedWithoutScanHeaders(t *testing.T) {
	blobs := &capturingBlobStorage{}
	uc := NewProcessInboundEmailUseCase(nil, blobs, &recordingMessageRepository{})

	original := "Subject: hi\r\n\r\nbody"
	err := uc.Handle(context.Background(), ProcessInboundEmailInput{
		Recipients:         []port.MailboxRecord{{ID: uuid.New()}},
		RecipientAddresses: []string{"a@example.test"},
		Body:               bytes.NewBufferString(original),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if string(blobs.stored) != original {
		t.Errorf("message was rewritten without a scanner: got %q, want %q", blobs.stored, original)
	}
}
