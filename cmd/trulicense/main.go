// Command trulicense issues Trustable licenses.
//
// The Ed25519 signing key lives in 1Password (vault TrustableLicenses, item
// "MasterKey Trustable") and is never written to disk. Issued licenses are
// archived in the same vault. See spec/14-license.md.
package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

const (
	licensePrefix     = "lic_"
	licenseDateFormat = "2006-01-02"
	pubKeyFile        = "master_key_pub"
	licenseItemPrefix = "Trustable License: "
)

// licensePayload mirrors the server-side payload in license.go.
type licensePayload struct {
	V     int      `json:"v"`
	Sub   string   `json:"sub"`
	Hosts []string `json:"hosts"`
	Iat   string   `json:"iat,omitempty"`
	Exp   string   `json:"exp,omitempty"`
}

func main() {
	if err := run(os.Args[1:], execOpRunner{}, os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string, runner opRunner, stdin *os.File, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("trulicense", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		email  = fs.String("email", "", "customer email (skips the prompt)")
		hosts  = fs.String("hosts", "", "comma- or space-separated apihosts (skips the prompt)")
		exp    = fs.String("exp", "", "expiry date YYYY-MM-DD (default: never expires)")
		force  = fs.Bool("force", false, "keygen: overwrite a mismatched "+pubKeyFile)
		outDir = fs.String("C", ".", "directory holding .op.json and "+pubKeyFile)
	)
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage:
  trulicense keygen                       bootstrap: op, service token, MasterKey, `+pubKeyFile+`
  trulicense                              interactive: asks email + hosts, prints and archives a license
  trulicense -email a@b.c -hosts h1,h2    non-interactive, same effect
  trulicense verify <token>               print payload + validity using the embedded public key

flags:
`)
		fs.PrintDefaults()
	}

	// Split the subcommand from the flags so both orders parse.
	sub := ""
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, rest = args[0], args[1:]
	}
	// flag stops parsing at the first non-flag argument, so hoist any flags
	// ahead of the positionals. This lets `trulicense verify <token> -C dir`
	// work as readily as `trulicense verify -C dir <token>`.
	rest = hoistFlags(rest)
	if err := fs.Parse(rest); err != nil {
		return err
	}

	switch sub {
	case "keygen":
		_, err := keygen(runner, *outDir, *force, stdin, stderr)
		return err
	case "verify":
		if fs.NArg() < 1 {
			return errors.New("verify needs a license token")
		}
		return verify(fs.Arg(0), *outDir, stdout)
	case "":
		return issue(runner, *outDir, *email, *hosts, *exp, stdin, stdout, stderr)
	default:
		fs.Usage()
		return fmt.Errorf("unknown command %q", sub)
	}
}

// boolFlags are the flags that take no value, so hoistFlags knows not to
// swallow the following argument.
var boolFlags = map[string]bool{"-force": true, "--force": true}

// hoistFlags reorders args so every flag (and its value) precedes the
// positional arguments, which is what flag.Parse requires.
func hoistFlags(args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		// "-flag=value" carries its value inline; a boolean takes none.
		if strings.Contains(a, "=") || boolFlags[a] {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}

// ---------------------------------------------------------------- bootstrap

// ensureToken loads the service token, prompting for one when absent.
//
// A freshly typed token is written to .op.json *before* op is invoked, so a
// vault failure never costs the operator the paste: the token is on disk and
// the run can simply be retried. The file is the single source of truth for
// OP_SERVICE_ACCOUNT_TOKEN in the child environment (see execOpRunner.run).
func ensureToken(runner opRunner, dir string, stdin *os.File, stderr *os.File) (string, error) {
	if err := runner.lookPath(); err != nil {
		return "", err
	}
	path := filepath.Join(dir, opTokenFile)
	token, err := readToken(path)
	if err != nil {
		return "", err
	}
	if token != "" {
		if err := validateServiceToken(token); err != nil {
			return "", fmt.Errorf("the token in %s is unusable: %w — delete the file to be prompted again", path, err)
		}
		if err := checkVaultAccess(runner, token); err != nil {
			return "", vaultAccessError(path, err)
		}
		return token, nil
	}
	// An exported token is the natural source in CI, where nothing can be typed.
	// It is persisted like a typed one so every later run uses the same source.
	if envToken := strings.TrimSpace(os.Getenv("OP_SERVICE_ACCOUNT_TOKEN")); envToken != "" {
		if err := validateServiceToken(envToken); err != nil {
			return "", fmt.Errorf("OP_SERVICE_ACCOUNT_TOKEN is set but unusable: %w", err)
		}
		if err := writeToken(path, envToken); err != nil {
			return "", err
		}
		fmt.Fprintf(stderr, "saved OP_SERVICE_ACCOUNT_TOKEN to %s\n", path)
		if err := checkVaultAccess(runner, envToken); err != nil {
			return "", vaultAccessError(path, err)
		}
		return envToken, nil
	}

	fmt.Fprintf(stderr, "1Password service token for vault %s\n", vaultName)
	fmt.Fprintf(stderr, "(input is hidden — the screen stays blank while you paste; press Enter when done)\n")
	fmt.Fprint(stderr, "token: ")
	token, err = readSecret(stdin)
	fmt.Fprintln(stderr)
	if err != nil {
		return "", err
	}
	token = strings.TrimSpace(token)
	// Echo is off, so confirm something was actually captured; a silent prompt
	// followed by a silent failure gives the operator nothing to act on.
	if token != "" {
		fmt.Fprintf(stderr, "read %d characters\n", len(token))
	}
	// Check the shape before invoking op: for an empty or malformed token op
	// reports only "No accounts configured for use with 1Password CLI", which
	// sends the operator off configuring an account they do not need.
	if err := validateServiceToken(token); err != nil {
		return "", fmt.Errorf("%w — a service token alone can read and write the vault; no `op account add` and no desktop app is needed", err)
	}
	// Save before touching op, so a rejected token still does not have to be
	// pasted a second time.
	if err := writeToken(path, token); err != nil {
		return "", err
	}
	fmt.Fprintf(stderr, "saved service token to %s\n", path)
	if err := checkVaultAccess(runner, token); err != nil {
		return "", vaultAccessError(path, err)
	}
	return token, nil
}

// vaultAccessError explains a failed vault check. op answers a token it cannot
// decode with "No accounts configured for use with 1Password CLI" — advice for
// a human sign-in that does not apply to a service account — so that specific
// message is translated rather than passed through bare.
func vaultAccessError(path string, err error) error {
	if strings.Contains(err.Error(), "No accounts configured") {
		return fmt.Errorf("1Password rejected the service token (saved in %s).\n"+
			"op reports \"No accounts configured\" for any token it cannot decode; it is the token that is wrong, not your setup —\n"+
			"a valid service token needs no `op account add` and no desktop app.\n"+
			"Check it was copied whole (they are ~850 characters, start with ops_, and are shown only once at creation),\n"+
			"then delete %s and re-run. Original: %w", path, path, err)
	}
	return fmt.Errorf("service token cannot reach vault %s (saved in %s; delete it to re-enter): %w", vaultName, path, err)
}

// readSecret reads a line with terminal echo disabled where possible.
//
// With echo off the terminal shows nothing at all while the token is typed or
// pasted, which is indistinguishable from a hung program. Callers therefore say
// so in the prompt, and confirm the length once the line is read.
func readSecret(stdin *os.File) (string, error) {
	fd := int(stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		return string(b), err
	}
	// Non-interactive (tests, pipes): fall back to a plain line read.
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// keygen performs the full bootstrap and returns the master private key.
func keygen(runner opRunner, dir string, force bool, stdin *os.File, stderr *os.File) (ed25519.PrivateKey, error) {
	token, err := ensureToken(runner, dir, stdin, stderr)
	if err != nil {
		return nil, err
	}

	item, err := getItem(runner, token, masterKeyItem)
	if err != nil {
		return nil, err
	}

	var priv ed25519.PrivateKey
	var pub ed25519.PublicKey
	if item != nil {
		privRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(item.field("private_key")))
		if err != nil {
			return nil, fmt.Errorf("decode private_key from %q: %w", masterKeyItem, err)
		}
		if len(privRaw) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("private_key in %q has wrong size %d", masterKeyItem, len(privRaw))
		}
		priv = ed25519.PrivateKey(privRaw)
		pub = priv.Public().(ed25519.PublicKey)
	} else {
		pub, priv, err = ed25519.GenerateKey(nil)
		if err != nil {
			return nil, err
		}
		// API_CREDENTIAL needs no primary password field, so each key sits in a
		// field named for what it is.
		err = createItem(runner, token, opItem{
			Title:    masterKeyItem,
			Category: "API_CREDENTIAL",
			Fields: []opField{
				{Label: "private_key", Type: "CONCEALED", Value: base64.StdEncoding.EncodeToString(priv)},
				{Label: "public_key", Type: "STRING", Value: base64.StdEncoding.EncodeToString(pub)},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("create %q: %w", masterKeyItem, err)
		}
		fmt.Fprintf(stderr, "created %q in vault %s\n", masterKeyItem, vaultName)
	}

	// Write master_key_pub, refusing to silently invalidate issued licenses.
	pubEncoded := base64.StdEncoding.EncodeToString(pub)
	pubPath := filepath.Join(dir, pubKeyFile)
	existing, err := os.ReadFile(pubPath)
	switch {
	case err == nil && strings.TrimSpace(string(existing)) == pubEncoded:
		// Already current.
	case err == nil && !force:
		return nil, fmt.Errorf("%s does not match the vault public key; overwriting it invalidates every license already issued — re-run with -force to replace it", pubPath)
	case err != nil && !os.IsNotExist(err):
		return nil, err
	default:
		if err := os.WriteFile(pubPath, []byte(pubEncoded+"\n"), 0644); err != nil {
			return nil, err
		}
		fmt.Fprintf(stderr, "wrote %s\n", pubPath)
	}
	return priv, nil
}

// ------------------------------------------------------------------- issue

func issue(runner opRunner, dir, email, hostsArg, exp string, stdin *os.File, stdout, stderr *os.File) error {
	priv, err := keygen(runner, dir, false, stdin, stderr)
	if err != nil {
		return err
	}
	token, err := readToken(filepath.Join(dir, opTokenFile))
	if err != nil {
		return err
	}

	reader := bufio.NewReader(stdin)
	if strings.TrimSpace(email) == "" {
		fmt.Fprint(stderr, "Email of the user: ")
		line, _ := reader.ReadString('\n')
		email = line
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return fmt.Errorf("invalid email %q", email)
	}

	if strings.TrimSpace(hostsArg) == "" {
		fmt.Fprint(stderr, "Hosts the license is valid for (comma or space separated): ")
		line, _ := reader.ReadString('\n')
		hostsArg = line
	}
	hosts, err := normalizeHosts(hostsArg)
	if err != nil {
		return err
	}

	if exp = strings.TrimSpace(exp); exp != "" {
		if _, err := time.Parse(licenseDateFormat, exp); err != nil {
			return fmt.Errorf("invalid -exp %q: want YYYY-MM-DD", exp)
		}
	}

	payload := licensePayload{
		V:     1,
		Sub:   email,
		Hosts: hosts,
		Iat:   time.Now().Format(licenseDateFormat),
		Exp:   exp,
	}
	licenseToken, err := signLicense(payload, priv)
	if err != nil {
		return err
	}

	// Print the token first: it must reach the operator even if archiving fails.
	fmt.Fprintln(stdout, licenseToken)
	fmt.Fprintf(stderr, "\nemail:  %s\nhosts:  %s\nexpiry: %s\n", email, strings.Join(hosts, ", "), expiryLabel(exp))

	title, err := nextItemTitle(runner, token, email)
	if err != nil {
		return fmt.Errorf("license issued but NOT archived (cannot list vault %s): %w", vaultName, err)
	}
	// API_CREDENTIAL rather than PASSWORD: it needs no primary password field,
	// so the token is stored once, in a field named for what it actually is.
	err = createItem(runner, token, opItem{
		Title:    title,
		Category: "API_CREDENTIAL",
		Fields: []opField{
			{Label: "license", Type: "CONCEALED", Value: licenseToken},
			{Label: "email", Type: "STRING", Value: email},
			{Label: "hosts", Type: "STRING", Value: strings.Join(hosts, ",")},
		},
	})
	if err != nil {
		return fmt.Errorf("license issued but NOT archived: %w", err)
	}
	fmt.Fprintf(stderr, "archived as %q in vault %s\n", title, vaultName)

	// The share link is a convenience for delivering the license, so a failure
	// here must not fail the run: the token is already printed and archived.
	link, shareErr := shareItem(runner, token, title, email)
	if shareErr != nil {
		fmt.Fprintf(stderr, "warning: could not create a share link (the license is issued and archived): %s\n", shareErr)
		return nil
	}
	fmt.Fprintf(stderr, "\nshare link (valid %s, opens only for %s):\n%s\n", shareExpiry, email, link)
	fmt.Fprintf(stderr, "1Password does not send this — mail it to the customer yourself.\n")
	return nil
}

func expiryLabel(exp string) string {
	if exp == "" {
		return "never"
	}
	return exp
}

// normalizeHosts validates and normalizes the host list. Every entry must carry
// an http/https scheme and a hostname; wildcards are rejected.
func normalizeHosts(raw string) ([]string, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	if len(fields) == 0 {
		return nil, errors.New("no hosts given")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		h, err := normalizeHost(f)
		if err != nil {
			return nil, err
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out, nil
}

// normalizeHost mirrors normalizeAPIHost in license.go.
func normalizeHost(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("empty host")
	}
	if strings.Contains(s, "*") {
		return "", fmt.Errorf("wildcards are not supported: %q", raw)
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("not a valid URL: %q", raw)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("host %q must include an http:// or https:// scheme", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("host %q has no hostname", raw)
	}
	return scheme + "://" + strings.ToLower(u.Host), nil
}

// signLicense produces "lic_<base64url(payload)>.<base64url(sig)>".
func signLicense(p licensePayload, priv ed25519.PrivateKey) (string, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(priv, body)
	return licensePrefix +
		base64.RawURLEncoding.EncodeToString(body) + "." +
		base64.RawURLEncoding.EncodeToString(sig), nil
}

// suffixPattern matches an archived license title's optional " <n>" suffix.
var suffixPattern = regexp.MustCompile(`^ (\d+)$`)

// nextItemTitle picks the first free name in the sequence
// "Trustable License: <email>", "... 2", "... 3". Existing items are never
// overwritten, and gaps left by deletions are reused.
func nextItemTitle(runner opRunner, token, email string) (string, error) {
	titles, err := listItemTitles(runner, token)
	if err != nil {
		return "", err
	}
	base := licenseItemPrefix + email
	taken := map[int]bool{}
	for _, t := range titles {
		rest, ok := cutPrefixFold(strings.TrimSpace(t), base)
		if !ok {
			continue
		}
		if rest == "" {
			taken[1] = true
			continue
		}
		if m := suffixPattern.FindStringSubmatch(rest); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				taken[n] = true
			}
		}
	}
	if !taken[1] {
		return base, nil
	}
	used := make([]int, 0, len(taken))
	for n := range taken {
		used = append(used, n)
	}
	sort.Ints(used)
	for n := 2; ; n++ {
		if !taken[n] {
			return fmt.Sprintf("%s %d", base, n), nil
		}
	}
}

// cutPrefixFold is strings.CutPrefix with case-insensitive matching, so
// "A@B.C" and "a@b.c" share one sequence.
func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return "", false
	}
	return s[len(prefix):], true
}

// ------------------------------------------------------------------ verify

func verify(token, dir string, stdout *os.File) error {
	pubRaw, err := os.ReadFile(filepath.Join(dir, pubKeyFile))
	if err != nil {
		return fmt.Errorf("read %s: %w", pubKeyFile, err)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(pubRaw)))
	if err != nil {
		return fmt.Errorf("decode %s: %w", pubKeyFile, err)
	}
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("%s has wrong size %d", pubKeyFile, len(key))
	}

	rest, ok := strings.CutPrefix(strings.TrimSpace(token), licensePrefix)
	if !ok {
		return fmt.Errorf("license does not start with %s", licensePrefix)
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 2 {
		return errors.New("malformed license: expected 2 parts separated by '.'")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(key), body, sig) {
		return errors.New("signature does not verify against " + pubKeyFile)
	}

	var p licensePayload
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	pretty, _ := json.MarshalIndent(p, "", "  ")
	fmt.Fprintln(stdout, string(pretty))

	if p.Exp != "" {
		t, err := time.Parse(licenseDateFormat, p.Exp)
		if err != nil {
			return fmt.Errorf("unreadable expiry %q", p.Exp)
		}
		if time.Now().After(t.AddDate(0, 0, 1).Add(-time.Nanosecond)) {
			return fmt.Errorf("signature is valid but the license expired on %s", p.Exp)
		}
	}
	fmt.Fprintln(stdout, "valid: signature OK, not expired")
	return nil
}
