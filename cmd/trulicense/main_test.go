package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeOp is an in-memory stand-in for the `op` CLI, so tests never touch a real
// vault. It records every invocation for argv assertions.
type fakeOp struct {
	items     map[string]opItem // by title
	calls     [][]string        // recorded argv
	stdins    []string          // recorded stdin
	missing   bool              // simulate op not on PATH
	listFails bool              // simulate `op item list` failing
	badToken  string            // this token is rejected by `vault get`
}

func newFakeOp() *fakeOp {
	return &fakeOp{items: map[string]opItem{}}
}

func (f *fakeOp) lookPath() error {
	if f.missing {
		return fmt.Errorf("the 1Password CLI (op) is not on PATH")
	}
	return nil
}

func (f *fakeOp) run(token, stdin string, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	f.stdins = append(f.stdins, stdin)

	switch {
	case len(args) >= 2 && args[0] == "vault" && args[1] == "get":
		if f.badToken != "" && token == f.badToken {
			return "", fmt.Errorf("authentication failed")
		}
		return `{"name":"` + vaultName + `"}`, nil

	case len(args) >= 2 && args[0] == "item" && args[1] == "get":
		item, ok := f.items[args[2]]
		if !ok {
			return "", fmt.Errorf("%q isn't an item in the %q vault", args[2], vaultName)
		}
		out, _ := json.Marshal(item)
		return string(out), nil

	case len(args) >= 2 && args[0] == "item" && args[1] == "list":
		if f.listFails {
			return "", fmt.Errorf("vault list failed")
		}
		list := make([]opItem, 0, len(f.items))
		for _, it := range f.items {
			list = append(list, it)
		}
		out, _ := json.Marshal(list)
		return string(out), nil

	case len(args) >= 2 && args[0] == "item" && args[1] == "create":
		var item opItem
		if err := json.Unmarshal([]byte(stdin), &item); err != nil {
			return "", fmt.Errorf("bad template: %w", err)
		}
		if _, exists := f.items[item.Title]; exists {
			return "", fmt.Errorf("item %q already exists", item.Title)
		}
		f.items[item.Title] = item
		return stdin, nil
	}
	return "", fmt.Errorf("unexpected op invocation: %v", args)
}

func (f *fakeOp) createdTitles() []string {
	var out []string
	for i, args := range f.calls {
		if len(args) >= 2 && args[0] == "item" && args[1] == "create" {
			var item opItem
			json.Unmarshal([]byte(f.stdins[i]), &item)
			out = append(out, item.Title)
		}
	}
	return out
}

// testDir returns a temp dir pre-seeded with a valid .op.json.
func testDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := writeToken(filepath.Join(dir, opTokenFile), "ops_test_token"); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	return dir
}

// runCLI invokes the CLI with a fake op and a canned stdin.
func runCLI(t *testing.T, f *fakeOp, dir string, stdinText string, args ...string) (string, string, error) {
	t.Helper()

	stdinFile := filepath.Join(dir, "stdin")
	if err := os.WriteFile(stdinFile, []byte(stdinText), 0600); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	in, err := os.Open(stdinFile)
	if err != nil {
		t.Fatalf("open stdin: %v", err)
	}
	defer in.Close()

	outFile, err := os.CreateTemp(dir, "stdout")
	if err != nil {
		t.Fatalf("temp stdout: %v", err)
	}
	defer outFile.Close()
	errFile, err := os.CreateTemp(dir, "stderr")
	if err != nil {
		t.Fatalf("temp stderr: %v", err)
	}
	defer errFile.Close()

	args = append(args, "-C", dir)
	runErr := run(args, f, in, outFile, errFile)

	stdout, _ := os.ReadFile(outFile.Name())
	stderr, _ := os.ReadFile(errFile.Name())
	return string(stdout), string(stderr), runErr
}

// seedMasterKey puts a keypair in the fake vault and writes master_key_pub.
func seedMasterKey(t *testing.T, f *fakeOp, dir string) ed25519.PrivateKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	f.items[masterKeyItem] = opItem{
		Title:    masterKeyItem,
		Category: "PASSWORD",
		Fields: []opField{
			{Label: "private_key", Type: "CONCEALED", Value: base64.StdEncoding.EncodeToString(priv)},
			{Label: "public_key", Type: "STRING", Value: base64.StdEncoding.EncodeToString(pub)},
		},
	}
	pubPath := filepath.Join(dir, pubKeyFile)
	if err := os.WriteFile(pubPath, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0644); err != nil {
		t.Fatalf("write pub: %v", err)
	}
	return priv
}

func TestKeygenCreatesMasterKeyAndPubFile(t *testing.T) {
	f := newFakeOp()
	dir := testDir(t)

	if _, _, err := runCLI(t, f, dir, "", "keygen"); err != nil {
		t.Fatalf("keygen: %v", err)
	}

	item, ok := f.items[masterKeyItem]
	if !ok {
		t.Fatalf("%q was not created", masterKeyItem)
	}
	privB64 := item.field("private_key")
	if privB64 == "" {
		t.Fatal("private_key field is empty")
	}
	// The public key on disk must match the private key in the vault.
	privRaw, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil {
		t.Fatalf("decode private_key: %v", err)
	}
	pubOnDisk, err := os.ReadFile(filepath.Join(dir, pubKeyFile))
	if err != nil {
		t.Fatalf("read %s: %v", pubKeyFile, err)
	}
	wantPub := base64.StdEncoding.EncodeToString(ed25519.PrivateKey(privRaw).Public().(ed25519.PublicKey))
	if strings.TrimSpace(string(pubOnDisk)) != wantPub {
		t.Errorf("%s does not match the vault keypair", pubKeyFile)
	}

	// The private key must never appear in argv.
	for _, args := range f.calls {
		for _, a := range args {
			if strings.Contains(a, privB64) {
				t.Fatal("private key leaked into the op command line")
			}
		}
	}
}

func TestKeygenIsIdempotent(t *testing.T) {
	f := newFakeOp()
	dir := testDir(t)
	seedMasterKey(t, f, dir)

	if _, _, err := runCLI(t, f, dir, "", "keygen"); err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if titles := f.createdTitles(); len(titles) != 0 {
		t.Errorf("keygen recreated items: %v", titles)
	}
}

func TestKeygenRefusesMismatchedPubFileWithoutForce(t *testing.T) {
	f := newFakeOp()
	dir := testDir(t)
	seedMasterKey(t, f, dir)

	// Replace master_key_pub with an unrelated key.
	otherPub, _, _ := ed25519.GenerateKey(nil)
	pubPath := filepath.Join(dir, pubKeyFile)
	if err := os.WriteFile(pubPath, []byte(base64.StdEncoding.EncodeToString(otherPub)+"\n"), 0644); err != nil {
		t.Fatalf("write pub: %v", err)
	}

	_, _, err := runCLI(t, f, dir, "", "keygen")
	if err == nil {
		t.Fatal("keygen overwrote a mismatched master_key_pub without -force")
	}
	if !strings.Contains(err.Error(), "-force") {
		t.Errorf("error = %q, want it to mention -force", err)
	}

	// With -force it proceeds.
	if _, _, err := runCLI(t, f, dir, "", "keygen", "-force"); err != nil {
		t.Fatalf("keygen -force: %v", err)
	}
}

func TestMissingOpBinaryFails(t *testing.T) {
	f := newFakeOp()
	f.missing = true
	dir := testDir(t)

	_, _, err := runCLI(t, f, dir, "", "keygen")
	if err == nil {
		t.Fatal("keygen succeeded without the op binary")
	}
	if len(f.calls) != 0 {
		t.Errorf("op was invoked despite being missing: %v", f.calls)
	}
}

func TestRejectedServiceTokenIsNotSaved(t *testing.T) {
	f := newFakeOp()
	f.badToken = "ops_wrong"
	dir := t.TempDir() // no .op.json

	_, _, err := runCLI(t, f, dir, "ops_wrong\n", "keygen")
	if err == nil {
		t.Fatal("keygen accepted a token that cannot reach the vault")
	}
	if _, statErr := os.Stat(filepath.Join(dir, opTokenFile)); statErr == nil {
		t.Error(".op.json was written for a rejected token")
	}
}

func TestAcceptedServiceTokenIsSavedPrivately(t *testing.T) {
	f := newFakeOp()
	dir := t.TempDir() // no .op.json

	if _, _, err := runCLI(t, f, dir, "ops_good\n", "keygen"); err != nil {
		t.Fatalf("keygen: %v", err)
	}
	path := filepath.Join(dir, opTokenFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf(".op.json was not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf(".op.json mode = %o, want 600", perm)
	}
	token, err := readToken(path)
	if err != nil || token != "ops_good" {
		t.Errorf("readToken = %q, %v", token, err)
	}
	// The token must never appear in argv.
	for _, args := range f.calls {
		for _, a := range args {
			if strings.Contains(a, "ops_good") {
				t.Fatal("service token leaked into the op command line")
			}
		}
	}
}

func TestIssueSignsAndArchives(t *testing.T) {
	f := newFakeOp()
	dir := testDir(t)
	priv := seedMasterKey(t, f, dir)

	stdout, _, err := runCLI(t, f, dir, "",
		"-email", "acme@example.com",
		"-hosts", "https://api.nuvolaris.io, http://miniops.me")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	token := strings.TrimSpace(stdout)
	if !strings.HasPrefix(token, licensePrefix) {
		t.Fatalf("stdout = %q, want a lic_ token", stdout)
	}

	// The token must verify against the seeded keypair.
	pub := priv.Public().(ed25519.PublicKey)
	body, sig := splitToken(t, token)
	if !ed25519.Verify(pub, body, sig) {
		t.Fatal("issued token does not verify against the vault key")
	}
	var p licensePayload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	if p.Sub != "acme@example.com" {
		t.Errorf("sub = %q", p.Sub)
	}
	want := []string{"https://api.nuvolaris.io", "http://miniops.me"}
	if len(p.Hosts) != len(want) {
		t.Fatalf("hosts = %v, want %v", p.Hosts, want)
	}
	for i := range want {
		if p.Hosts[i] != want[i] {
			t.Errorf("hosts[%d] = %q, want %q", i, p.Hosts[i], want[i])
		}
	}
	if p.Exp != "" {
		t.Errorf("exp = %q, want empty (never expires)", p.Exp)
	}

	// The archived item carries the expected fields.
	item, ok := f.items[licenseItemPrefix+"acme@example.com"]
	if !ok {
		t.Fatalf("license was not archived; vault holds %v", keysOf(f.items))
	}
	if got := item.field("email"); got != "acme@example.com" {
		t.Errorf("email field = %q", got)
	}
	if got := item.field("hosts"); got != "https://api.nuvolaris.io,http://miniops.me" {
		t.Errorf("hosts field = %q", got)
	}
	if got := item.field("license"); got != token {
		t.Errorf("license field does not match the printed token")
	}
	// The license body must be passed on stdin, never in argv.
	for _, args := range f.calls {
		for _, a := range args {
			if strings.Contains(a, token) {
				t.Fatal("license token leaked into the op command line")
			}
		}
	}
}

func TestIssueInteractivePrompts(t *testing.T) {
	f := newFakeOp()
	dir := testDir(t)
	seedMasterKey(t, f, dir)

	stdout, _, err := runCLI(t, f, dir, "user@example.com\nhttps://api.nuvolaris.io\n")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout), licensePrefix) {
		t.Fatalf("stdout = %q, want a lic_ token", stdout)
	}
	if _, ok := f.items[licenseItemPrefix+"user@example.com"]; !ok {
		t.Errorf("license not archived; vault holds %v", keysOf(f.items))
	}
}

func TestIssueRejectsInvalidHostsWithoutArchiving(t *testing.T) {
	cases := []struct{ name, hosts string }{
		{"no scheme", "api.nuvolaris.io"},
		{"wildcard", "https://*.example.com"},
		{"scheme only", "https://"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeOp()
			dir := testDir(t)
			seedMasterKey(t, f, dir)

			stdout, _, err := runCLI(t, f, dir, "\n",
				"-email", "a@b.c", "-hosts", tc.hosts)
			if err == nil {
				t.Fatal("invalid hosts were accepted")
			}
			if strings.Contains(stdout, licensePrefix) {
				t.Error("a token was emitted for invalid hosts")
			}
			if titles := f.createdTitles(); len(titles) != 0 {
				t.Errorf("something was archived for invalid hosts: %v", titles)
			}
		})
	}
}

func TestIssueRejectsInvalidEmail(t *testing.T) {
	f := newFakeOp()
	dir := testDir(t)
	seedMasterKey(t, f, dir)

	stdout, _, err := runCLI(t, f, dir, "", "-email", "not-an-email", "-hosts", "https://a.example.com")
	if err == nil {
		t.Fatal("invalid email was accepted")
	}
	if strings.Contains(stdout, licensePrefix) {
		t.Error("a token was emitted for an invalid email")
	}
}

func TestProgressiveSuffix(t *testing.T) {
	f := newFakeOp()
	dir := testDir(t)
	seedMasterKey(t, f, dir)

	for i, want := range []string{
		licenseItemPrefix + "a@b.c",
		licenseItemPrefix + "a@b.c 2",
		licenseItemPrefix + "a@b.c 3",
	} {
		if _, _, err := runCLI(t, f, dir, "", "-email", "a@b.c", "-hosts", "https://a.example.com"); err != nil {
			t.Fatalf("issue %d: %v", i+1, err)
		}
		if _, ok := f.items[want]; !ok {
			t.Fatalf("issue %d: %q missing; vault holds %v", i+1, want, keysOf(f.items))
		}
	}
	if len(f.items) != 4 { // 3 licenses + MasterKey
		t.Errorf("vault holds %d items, want 4: %v", len(f.items), keysOf(f.items))
	}
}

func TestProgressiveSuffixFillsGaps(t *testing.T) {
	f := newFakeOp()
	dir := testDir(t)
	seedMasterKey(t, f, dir)

	// Pre-seed "a@b.c" and "a@b.c 3": the gap at 2 must be reused.
	f.items[licenseItemPrefix+"a@b.c"] = opItem{Title: licenseItemPrefix + "a@b.c"}
	f.items[licenseItemPrefix+"a@b.c 3"] = opItem{Title: licenseItemPrefix + "a@b.c 3"}

	if _, _, err := runCLI(t, f, dir, "", "-email", "a@b.c", "-hosts", "https://a.example.com"); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, ok := f.items[licenseItemPrefix+"a@b.c 2"]; !ok {
		t.Errorf("gap at 2 was not reused; vault holds %v", keysOf(f.items))
	}
}

func TestProgressiveSuffixIsCaseInsensitive(t *testing.T) {
	f := newFakeOp()
	dir := testDir(t)
	seedMasterKey(t, f, dir)

	// An existing item with a differently-cased email shares the sequence.
	f.items[licenseItemPrefix+"A@B.C"] = opItem{Title: licenseItemPrefix + "A@B.C"}

	if _, _, err := runCLI(t, f, dir, "", "-email", "a@b.c", "-hosts", "https://a.example.com"); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, ok := f.items[licenseItemPrefix+"a@b.c 2"]; !ok {
		t.Errorf("want %q; vault holds %v", licenseItemPrefix+"a@b.c 2", keysOf(f.items))
	}
	if _, ok := f.items[licenseItemPrefix+"a@b.c"]; ok {
		t.Error("a second unsuffixed item was created for a differently-cased email")
	}
}

func TestProgressiveSuffixIsPerEmail(t *testing.T) {
	f := newFakeOp()
	dir := testDir(t)
	seedMasterKey(t, f, dir)

	f.items[licenseItemPrefix+"a@b.c"] = opItem{Title: licenseItemPrefix + "a@b.c"}
	f.items[licenseItemPrefix+"a@b.c 3"] = opItem{Title: licenseItemPrefix + "a@b.c 3"}

	if _, _, err := runCLI(t, f, dir, "", "-email", "x@y.z", "-hosts", "https://a.example.com"); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, ok := f.items[licenseItemPrefix+"x@y.z"]; !ok {
		t.Errorf("a fresh email must start unsuffixed; vault holds %v", keysOf(f.items))
	}
}

func TestFailedListAbortsArchiveButStillPrintsToken(t *testing.T) {
	f := newFakeOp()
	dir := testDir(t)
	seedMasterKey(t, f, dir)
	f.listFails = true

	stdout, _, err := runCLI(t, f, dir, "", "-email", "a@b.c", "-hosts", "https://a.example.com")
	if err == nil {
		t.Fatal("a failed listing must exit non-zero")
	}
	if !strings.Contains(err.Error(), "NOT archived") {
		t.Errorf("error = %q, want it to say the license was not archived", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout), licensePrefix) {
		t.Error("the token must still be printed when archiving fails")
	}
	if titles := f.createdTitles(); len(titles) != 0 {
		t.Errorf("an item was created despite the failed listing: %v", titles)
	}
}

func TestVerifyCommand(t *testing.T) {
	f := newFakeOp()
	dir := testDir(t)
	seedMasterKey(t, f, dir)

	stdout, _, err := runCLI(t, f, dir, "", "-email", "a@b.c", "-hosts", "https://a.example.com")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	token := strings.TrimSpace(stdout)

	out, _, err := runCLI(t, f, dir, "", "verify", token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !strings.Contains(out, "valid") || !strings.Contains(out, "a@b.c") {
		t.Errorf("verify output = %q", out)
	}

	// A tampered token must fail.
	tampered := token[:len(token)-2] + "AA"
	if _, _, err := runCLI(t, f, dir, "", "verify", tampered); err == nil {
		t.Error("verify accepted a tampered token")
	}
}

func TestNormalizeHosts(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  []string
		valid bool
	}{
		{"comma separated", "https://a.com,https://b.com", []string{"https://a.com", "https://b.com"}, true},
		{"space separated", "https://a.com https://b.com", []string{"https://a.com", "https://b.com"}, true},
		{"lowercased", "HTTPS://A.COM", []string{"https://a.com"}, true},
		{"trailing slash stripped", "https://a.com/", []string{"https://a.com"}, true},
		{"path stripped", "https://a.com/api/v1", []string{"https://a.com"}, true},
		{"port kept", "https://a.com:8443", []string{"https://a.com:8443"}, true},
		{"duplicates collapsed", "https://a.com,https://a.com/", []string{"https://a.com"}, true},
		{"no scheme rejected", "a.com", nil, false},
		{"ftp rejected", "ftp://a.com", nil, false},
		{"wildcard rejected", "https://*.a.com", nil, false},
		{"empty rejected", "", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeHosts(tc.in)
			if tc.valid != (err == nil) {
				t.Fatalf("normalizeHosts(%q) error = %v, want valid=%v", tc.in, err, tc.valid)
			}
			if !tc.valid {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func splitToken(t *testing.T, token string) ([]byte, []byte) {
	t.Helper()
	parts := strings.SplitN(strings.TrimPrefix(token, licensePrefix), ".", 2)
	if len(parts) != 2 {
		t.Fatalf("malformed token %q", token)
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	return body, sig
}

func keysOf(m map[string]opItem) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
