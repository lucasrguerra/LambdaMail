package valueobject

import "errors"

// TLSARecord models a DANE TLSA RR (RFC 6698/7671). Usage 2 (PKIX-CA) is rejected
// by explicit ADR - see PLAN.md section 5.1: Let's Encrypt rotates intermediates
// without notice, making it unpredictable. Only usage 3 (DANE-EE) is supported.
type TLSARecord struct {
	usage        uint8
	selector     uint8
	matchingType uint8
	data         string
}

var (
	ErrTLSAUsageUnsupported = errors.New("tlsa record: only usage 3 (DANE-EE) is supported")
	ErrTLSADataWrongLength  = errors.New("tlsa record: matching type 1 (SHA-256) requires a 64-hex-char digest")
)

func NewTLSARecord(usage, selector, matchingType uint8, data string) (TLSARecord, error) {
	if usage != 3 {
		return TLSARecord{}, ErrTLSAUsageUnsupported
	}
	if matchingType == 1 && len(data) != 64 {
		return TLSARecord{}, ErrTLSADataWrongLength
	}
	return TLSARecord{usage: usage, selector: selector, matchingType: matchingType, data: data}, nil
}

func (r TLSARecord) Usage() uint8        { return r.usage }
func (r TLSARecord) Selector() uint8     { return r.selector }
func (r TLSARecord) MatchingType() uint8 { return r.matchingType }
func (r TLSARecord) Data() string        { return r.data }
