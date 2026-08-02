package usecase

import "fmt"

// SmtpRejection is a use case's decision to refuse a message, carrying the
// reply the SMTP layer must send back.
//
// The decision belongs to the application layer - only it knows that a virus
// is permanent and a greylist is temporary - but the wire format belongs to
// the presentation layer. This type is the seam: the use case names the
// outcome, the adapter renders it. Encoding the reply in the error *text* and
// re-parsing it downstream would make a reworded message silently change the
// code a remote MTA receives, turning a permanent refusal into a retry loop.
type SmtpRejection struct {
	// Code is the three-digit reply code (RFC 5321 section 4.2.1).
	Code int
	// EnhancedCode is the class.subject.detail triplet of RFC 3463.
	EnhancedCode [3]int
	// Message is the human-readable text, without any code prefix.
	Message string
}

func (e *SmtpRejection) Error() string {
	return fmt.Sprintf("%d %d.%d.%d %s",
		e.Code, e.EnhancedCode[0], e.EnhancedCode[1], e.EnhancedCode[2], e.Message)
}

// IsPermanent reports whether the sender must give up rather than retry.
func (e *SmtpRejection) IsPermanent() bool {
	return e.Code >= 500 && e.Code < 600
}

// rejectDmarc refuses a message whose sender publishes p=reject and whose
// DMARC evaluation failed.
func rejectDmarc() *SmtpRejection {
	return &SmtpRejection{Code: 550, EnhancedCode: [3]int{5, 7, 1}, Message: "DMARC policy rejects this message"}
}

// rejectVirus refuses an infected message. It is permanent: the same bytes
// will still be infected on a retry.
func rejectVirus(virusName string) *SmtpRejection {
	return &SmtpRejection{Code: 554, EnhancedCode: [3]int{5, 7, 1}, Message: "Virus detected: " + virusName}
}

// rejectSpam refuses a message that scored above the reject threshold.
func rejectSpam() *SmtpRejection {
	return &SmtpRejection{Code: 554, EnhancedCode: [3]int{5, 7, 1}, Message: "Spam threshold exceeded"}
}

// deferGreylisted asks the sender to try again. It is deliberately temporary:
// greylisting works precisely because a legitimate MTA retries and a spam
// cannon does not.
func deferGreylisted() *SmtpRejection {
	return &SmtpRejection{Code: 451, EnhancedCode: [3]int{4, 7, 1}, Message: "Greylisted, please try again later"}
}
