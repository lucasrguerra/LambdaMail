package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/google/uuid"

	"lambdamail/protocols/internal/infrastructure/dkim"
	"lambdamail/protocols/internal/infrastructure/postgres"
	"lambdamail/protocols/internal/infrastructure/vault"
	httppresentation "lambdamail/protocols/internal/presentation/http"
)

// Rotation has to leave a key the signer can actually open. An earlier
// implementation retired the live key and inserted a placeholder as ACTIVE,
// which made FindActiveKeys fail for the whole domain - the console's "rotate"
// button silently stopped the domain being able to sign anything.
func TestDkimRotationLeavesAUsableKey(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped in -short mode")
	}
	ctx := context.Background()

	runtimeDir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(15449).
		RuntimePath(runtimeDir).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	pool, err := postgres.NewPool(ctx, "postgres://postgres:postgres@localhost:15449/postgres?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	applyMigrations(t, ctx, pool)

	const domainName = "rotate.example"
	domainID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO domains (id, name, punycode_name) VALUES ($1, $2, $3)`,
		domainID, domainName, domainName); err != nil {
		t.Fatal(err)
	}

	v, err := vault.New("a-master-key-for-rotation-tests")
	if err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewDkimRepository(pool, v)

	generate := func(algorithm string) ([]byte, string, error) {
		generated, err := dkim.Generate(algorithm)
		if err != nil {
			return nil, "", err
		}
		return generated.PrivateKeyPEM, generated.PublicKeyBase64, nil
	}

	// A key is in place before the rotation, as it would be in production.
	firstPEM, firstPub, err := generate("rsa2048")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Insert(ctx, domainName, "old", "rsa2048", firstPEM, firstPub); err != nil {
		t.Fatal(err)
	}

	router := httppresentation.NewRouter(nil, func() error { return nil })
	router.SetAdminDkimAPI(repo, generate, webmailSecret)
	server := httptest.NewServer(router)
	defer server.Close()

	rotate := func(token, selector string) *http.Response {
		body, _ := json.Marshal(map[string]string{
			"domain": domainName, "selector": selector, "algorithm": "rsa2048",
		})
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/admin/dkim/rotate", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// A user token must not reach an admin endpoint.
	if resp := rotate(mintSession(t, "user@rotate.example", "user", time.Hour), "nope"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a user token rotated a key: status %d", resp.StatusCode)
	}
	resp := rotate("", "nope")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an anonymous caller rotated a key: status %d", resp.StatusCode)
	}

	adminToken := mintSession(t, "admin@rotate.example", "admin", time.Hour)
	if resp := rotate(adminToken, "new"); resp.StatusCode != http.StatusOK {
		t.Fatalf("rotate: status %d", resp.StatusCode)
	}

	// The point of the whole exercise: the signer can open the new key.
	keys, err := repo.FindActiveKeys(ctx, domainName)
	if err != nil {
		t.Fatalf("the rotated key cannot be opened: %v", err)
	}
	if len(keys) != 1 || keys[0].Selector != "new" {
		t.Fatalf("active keys = %+v, want exactly the new selector", keys)
	}
	if _, err := dkim.ParsePrivateKey(keys[0].PrivateKeyPEM); err != nil {
		t.Fatalf("the rotated key is not a usable private key: %v", err)
	}

	// The old key is retired with an overlap, not deleted: mail already sent
	// under it is still being verified.
	var status string
	var retireAfter *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, retire_after FROM dkim_keys WHERE selector = 'old'`).Scan(&status, &retireAfter); err != nil {
		t.Fatal(err)
	}
	if status != "RETIRING" || retireAfter == nil || !retireAfter.After(time.Now()) {
		t.Errorf("old key status=%q retire_after=%v, want RETIRING with a future overlap", status, retireAfter)
	}

	// A selector that would be invalid in DNS is refused.
	if resp := rotate(adminToken, "bad selector!"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an invalid selector was accepted: status %d", resp.StatusCode)
	}
}
