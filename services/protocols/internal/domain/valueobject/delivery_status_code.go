package valueobject

import (
	"errors"
	"fmt"
)

// DeliveryStatusCode is an RFC 3463 enhanced mail status code (class.subject.detail).
type DeliveryStatusCode struct {
	class, subject, detail int
}

var ErrDeliveryStatusInvalidClass = errors.New("delivery status code: class must be 2 (success), 4 (transient) or 5 (permanent)")

func NewDeliveryStatusCode(class, subject, detail int) (DeliveryStatusCode, error) {
	if class != 2 && class != 4 && class != 5 {
		return DeliveryStatusCode{}, ErrDeliveryStatusInvalidClass
	}
	return DeliveryStatusCode{class: class, subject: subject, detail: detail}, nil
}

func (c DeliveryStatusCode) String() string {
	return fmt.Sprintf("%d.%d.%d", c.class, c.subject, c.detail)
}
