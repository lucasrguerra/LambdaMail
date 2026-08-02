package tlsprovider

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// AcmeCertWatcher serves the certificates Traefik obtained, reading them from
// the acme.json it maintains (PLAN.md section 8.1).
//
// Three properties of that file drive this design (section 8.2):
//
//  1. Traefik replaces acme.json by writing a temporary file and renaming it.
//     A watcher bound to the original inode goes permanently blind after the
//     first renewal, so this watcher polls the path instead and compares a
//     content hash. The symptom of getting this wrong only appears ninety
//     days later, in production, as an expired certificate.
//  2. Traefik only holds certificates for hosts it routes. A missing entry is
//     a deployment problem, reported loudly rather than papered over.
//  3. The file layout varies by Traefik version and resolver name, so the
//     parser iterates every resolver and tolerates unknown fields.
type AcmeCertWatcher struct {
	path        string
	defaultHost string

	// certificates is swapped atomically so that in-flight handshakes keep
	// using a consistent map.
	certificates atomic.Pointer[map[string]*tls.Certificate]

	lastHash atomic.Pointer[[32]byte]
	// lastReloadAt is the last time the store was successfully read and found
	// usable, whether or not its content had changed. It is what PLAN.md risk
	// R2 monitors: acme.json normally sits untouched for sixty days, so a
	// timestamp that only advanced on change could not distinguish a healthy
	// idle watcher from one that has gone blind.
	lastReloadAt atomic.Pointer[time.Time]
	// lastChangeAt is the last time the certificates themselves were swapped.
	lastChangeAt atomic.Pointer[time.Time]

	// unknownSNI counts handshakes for names we hold no certificate for; it
	// backs the tls_unknown_sni_total metric of PLAN.md section 8.4.
	unknownSNI atomic.Int64
}

var (
	ErrNoCertificatesLoaded = errors.New("acme watcher: no certificates loaded")
	ErrNoCertificateForSNI  = errors.New("acme watcher: no certificate for the requested server name")
)

// acmeFile mirrors the parts of acme.json this code reads. Every other field
// is ignored on purpose so a Traefik upgrade does not break certificate
// loading.
type acmeFile map[string]struct {
	Certificates []struct {
		Domain struct {
			Main string   `json:"main"`
			SANs []string `json:"sans"`
		} `json:"domain"`
		Certificate string `json:"certificate"`
		Key         string `json:"key"`
	} `json:"certificates"`
}

// NewAcmeCertWatcher reads the store once so that a broken configuration
// fails at startup rather than at the first connection.
func NewAcmeCertWatcher(dir, filename, defaultHost string) (*AcmeCertWatcher, error) {
	if filename == "" {
		filename = "acme.json"
	}
	w := &AcmeCertWatcher{
		path:        filepath.Join(dir, filename),
		defaultHost: strings.ToLower(defaultHost),
	}
	if err := w.reload(); err != nil {
		return nil, err
	}
	return w, nil
}

// Watch reloads on a fixed interval. Polling is the mechanism, not a fallback:
// see the rename note on the type.
func (w *AcmeCertWatcher) Watch(stop <-chan struct{}, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if err := w.reload(); err != nil {
				// Keeping the previous store is deliberate: a truncated file
				// caught mid-write must not leave the listeners with nothing
				// (PLAN.md section 8.4).
				log.Printf("WARNING: acme watcher reload failed, keeping the certificates already loaded: %v", err)
			}
		}
	}
}

func (w *AcmeCertWatcher) reload() error {
	raw, err := os.ReadFile(w.path)
	if err != nil {
		return fmt.Errorf("read %s: %w", w.path, err)
	}

	now := time.Now()

	hash := sha256.Sum256(raw)
	if previous := w.lastHash.Load(); previous == nil || *previous != hash {
		certificates, err := parseAcmeStore(raw, now)
		if err != nil {
			return err
		}
		if len(certificates) == 0 {
			return fmt.Errorf("acme watcher: %s holds no usable certificate", w.path)
		}

		w.certificates.Store(&certificates)
		w.lastHash.Store(&hash)
		w.lastChangeAt.Store(&now)
	}

	// Expiry is checked on every pass, not only on change: a certificate ages
	// out while the file it lives in stays byte-identical.
	w.reportExpiry(now)

	w.lastReloadAt.Store(&now)
	return nil
}

// reportExpiry raises the alerts of PLAN.md section 8.4 for certificates that
// are close to expiring. Renewal is Traefik's job, so all this side can do is
// make the failure loud before it becomes an outage.
func (w *AcmeCertWatcher) reportExpiry(now time.Time) {
	host, expiry, ok := w.EarliestExpiry()
	if !ok {
		return
	}

	switch remaining := expiry.Sub(now); {
	case remaining <= 0:
		log.Printf("CRITICAL: the certificate for %s expired at %s and Traefik has not replaced it", host, expiry.Format(time.RFC3339))
	case remaining < 24*time.Hour:
		log.Printf("CRITICAL: the certificate for %s expires in %s", host, remaining.Round(time.Minute))
	case remaining < 7*24*time.Hour:
		log.Printf("WARNING: the certificate for %s expires in %s", host, remaining.Round(time.Hour))
	}
}

// EarliestExpiry reports the soonest expiry across the loaded certificates,
// which is the one that decides when the deployment breaks.
func (w *AcmeCertWatcher) EarliestExpiry() (string, time.Time, bool) {
	loaded := w.certificates.Load()
	if loaded == nil {
		return "", time.Time{}, false
	}

	var (
		soonestHost string
		soonest     time.Time
	)
	for host, cert := range *loaded {
		if cert.Leaf == nil {
			continue
		}
		if soonest.IsZero() || cert.Leaf.NotAfter.Before(soonest) {
			soonest, soonestHost = cert.Leaf.NotAfter, host
		}
	}
	if soonest.IsZero() {
		return "", time.Time{}, false
	}
	return soonestHost, soonest, true
}

// parseAcmeStore walks every resolver in the file. A single unusable entry -
// an expired certificate, or one whose key does not match - is skipped rather
// than failing the whole load, so one stale domain cannot take the server down.
func parseAcmeStore(raw []byte, now time.Time) (map[string]*tls.Certificate, error) {
	var store acmeFile
	if err := json.Unmarshal(raw, &store); err != nil {
		return nil, fmt.Errorf("parse acme store: %w", err)
	}

	certificates := map[string]*tls.Certificate{}

	for resolverName, resolver := range store {
		for _, entry := range resolver.Certificates {
			certPEM, err := base64.StdEncoding.DecodeString(entry.Certificate)
			if err != nil {
				log.Printf("acme watcher: skipping %s in resolver %q: certificate is not valid base64", entry.Domain.Main, resolverName)
				continue
			}
			keyPEM, err := base64.StdEncoding.DecodeString(entry.Key)
			if err != nil {
				log.Printf("acme watcher: skipping %s in resolver %q: key is not valid base64", entry.Domain.Main, resolverName)
				continue
			}

			pair, err := tls.X509KeyPair(certPEM, keyPEM)
			if err != nil {
				log.Printf("acme watcher: skipping %s in resolver %q: %v", entry.Domain.Main, resolverName, err)
				continue
			}

			// X509KeyPair leaves Leaf nil. Parsing it here is what makes the
			// expiry checks below and in reportExpiry possible at all.
			leaf, err := x509.ParseCertificate(pair.Certificate[0])
			if err != nil {
				log.Printf("acme watcher: skipping %s in resolver %q: leaf certificate is unparseable: %v", entry.Domain.Main, resolverName, err)
				continue
			}
			// An expired entry is worse than a missing one: it would be served
			// in a handshake every peer then rejects. Skipping it lets the SNI
			// fallback or the self-signed provider take over instead
			// (PLAN.md section 8.2, trap 3).
			if now.After(leaf.NotAfter) {
				log.Printf("acme watcher: skipping %s in resolver %q: the certificate expired at %s", entry.Domain.Main, resolverName, leaf.NotAfter.Format(time.RFC3339))
				continue
			}
			pair.Leaf = leaf

			for _, name := range append([]string{entry.Domain.Main}, entry.Domain.SANs...) {
				if name != "" {
					certificates[strings.ToLower(name)] = &pair
				}
			}
		}
	}

	return certificates, nil
}

// GetCertificate satisfies tls.Config.GetCertificate. It matches the exact
// name, then the wildcard covering it, then the mail host, which is what a
// client connecting by IP or with no SNI should receive.
func (w *AcmeCertWatcher) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	loaded := w.certificates.Load()
	if loaded == nil {
		return nil, ErrNoCertificatesLoaded
	}
	certificates := *loaded

	name := ""
	if hello != nil {
		name = strings.ToLower(strings.TrimSuffix(hello.ServerName, "."))
	}

	if cert, ok := certificates[name]; ok && name != "" {
		return cert, nil
	}
	if cert, ok := certificates[wildcardOf(name)]; ok {
		return cert, nil
	}
	if cert, ok := certificates[w.defaultHost]; ok {
		w.unknownSNI.Add(1)
		return cert, nil
	}

	w.unknownSNI.Add(1)
	return nil, fmt.Errorf("%w: %q", ErrNoCertificateForSNI, name)
}

// wildcardOf turns "mail.example.com" into "*.example.com".
func wildcardOf(name string) string {
	if _, rest, found := strings.Cut(name, "."); found && rest != "" {
		return "*." + rest
	}
	return ""
}

// LastReload reports when the store was last read successfully, changed or
// not. A timestamp that stops advancing is the signal that the watcher went
// blind (PLAN.md risk R2).
func (w *AcmeCertWatcher) LastReload() time.Time {
	if t := w.lastReloadAt.Load(); t != nil {
		return *t
	}
	return time.Time{}
}

// LastChange reports when the served certificates were last swapped, which is
// how a renewal that never arrived is detected.
func (w *AcmeCertWatcher) LastChange() time.Time {
	if t := w.lastChangeAt.Load(); t != nil {
		return *t
	}
	return time.Time{}
}

// UnknownSNICount backs the tls_unknown_sni_total metric.
func (w *AcmeCertWatcher) UnknownSNICount() int64 {
	return w.unknownSNI.Load()
}

// HasCertificateFor reports whether a certificate exists for a host. The
// preflight uses it to catch the missing-router trap of PLAN.md section 8.2.
func (w *AcmeCertWatcher) HasCertificateFor(host string) bool {
	loaded := w.certificates.Load()
	if loaded == nil {
		return false
	}
	host = strings.ToLower(host)
	certificates := *loaded
	if _, ok := certificates[host]; ok {
		return true
	}
	_, ok := certificates[wildcardOf(host)]
	return ok
}
