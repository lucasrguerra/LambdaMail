package valueobject

import "errors"

// QuotaLimit is a strictly positive byte quota (see PLAN.md section 3, Mailbox invariant).
type QuotaLimit struct {
	bytes int64
}

var ErrQuotaLimitNotPositive = errors.New("quota limit: must be a positive number of bytes")

func NewQuotaLimit(bytes int64) (QuotaLimit, error) {
	if bytes <= 0 {
		return QuotaLimit{}, ErrQuotaLimitNotPositive
	}
	return QuotaLimit{bytes: bytes}, nil
}

func (q QuotaLimit) Bytes() int64 { return q.bytes }

// Exceeds reports whether usedBytes violates the "used_bytes <= quota_bytes" invariant.
func (q QuotaLimit) Exceeds(usedBytes int64) bool { return usedBytes > q.bytes }
