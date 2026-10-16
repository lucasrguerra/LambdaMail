package httppresentation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"lambdamail/protocols/internal/domain/bimi"
)

// netResolver reads TXT records from the system resolver.
type netResolver struct{}

func (netResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, name)
}

// maxFetchBytes bounds what a check reads. A logo is at most 32 KB and a
// certificate chain about 10 KB.
const maxFetchBytes = 256 * 1024

// newBimiFetcher returns the HTTP client the console's checks use.
//
// The URLs it is given come from DNS and from a field an operator filled in,
// so it must not become a way to make this server call its own network: HTTPS
// only, and the address actually connected to (after DNS, so a name that
// rebinds to a private address is caught too) must be a public one.
func newBimiFetcher() bimi.Fetcher {
	dialer := &net.Dialer{
		Timeout: 6 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !publicAddress(ip) {
				return errors.New("refusing to connect to a non-public address")
			}
			return nil
		},
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: 6 * time.Second},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" {
				return errors.New("redirect to a non-HTTPS URL")
			}
			return nil
		},
	}

	return func(ctx context.Context, url string) (bimi.Response, error) {
		if !strings.HasPrefix(url, "https://") {
			return bimi.Response{}, fmt.Errorf("%q is not an https URL", url)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return bimi.Response{}, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return bimi.Response{}, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
		if err != nil {
			return bimi.Response{}, err
		}
		return bimi.Response{Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Body: body}, nil
	}
}

// publicAddress is false for anything that is not a routable public address.
func publicAddress(ip net.IP) bool {
	switch {
	case ip.IsLoopback(), ip.IsPrivate(), ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(),
		ip.IsMulticast(), ip.IsUnspecified(), ip.IsInterfaceLocalMulticast():
		return false
	}
	// Carrier-grade NAT, 100.64.0.0/10, which IsPrivate does not cover
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xC0 == 64 {
		return false
	}
	return true
}
