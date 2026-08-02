package httppresentation_test

import (
	"testing"

	tlsprovider "lambdamail/protocols/internal/infrastructure/tls"
	httppresentation "lambdamail/protocols/internal/presentation/http"
)

// The TLS panel is wired by a type assertion in the composition root, so a
// change to either side would silently leave the panel reporting
// "TLS_API_DISABLED" in production rather than failing the build.
func TestAcmeCertWatcherSatisfiesTlsStatusSource(t *testing.T) {
	var _ httppresentation.TlsStatusSource = (*tlsprovider.AcmeCertWatcher)(nil)
}
