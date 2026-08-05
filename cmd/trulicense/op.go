package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

const (
	vaultName     = "TrustableLicenses"
	masterKeyItem = "MasterKey Trustable"
	opTokenFile   = ".op.json"
)

// opRunner runs the `op` CLI. It is an interface so tests can substitute a fake
// vault and assert on the exact argv and stdin used.
type opRunner interface {
	// run invokes `op` with args, feeding stdin, and returns stdout.
	run(token string, stdin string, args ...string) (string, error)
	// lookPath reports whether the op binary is available.
	lookPath() error
}

type execOpRunner struct{}

func (execOpRunner) lookPath() error {
	if _, err := exec.LookPath("op"); err != nil {
		return fmt.Errorf("the 1Password CLI (op) is not on PATH — install it from https://developer.1password.com/docs/cli/get-started/")
	}
	return nil
}

func (execOpRunner) run(token string, stdin string, args ...string) (string, error) {
	// Every caller obtains this token from ensureToken, which sources it from
	// .op.json (writing it there first when it came from a prompt or the
	// environment). An empty one would make op fall back to interactive
	// sign-in, so refuse rather than let it reach the CLI.
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("internal: op invoked without a service token (op %s)", strings.Join(args, " "))
	}
	cmd := exec.Command("op", args...)
	// The token goes through the environment, never on the command line.
	// OP_ACCOUNT is cleared so a stray desktop-app account cannot shadow the
	// service token and send op down the interactive sign-in path.
	cmd.Env = append(filterEnv(os.Environ(), "OP_ACCOUNT", "OP_SERVICE_ACCOUNT_TOKEN"),
		"OP_SERVICE_ACCOUNT_TOKEN="+token)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	} else {
		// Never hand op the terminal: without a usable token it would otherwise
		// prompt for a sign-in address and hang on the operator's TTY.
		cmd.Stdin = strings.NewReader("")
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Surface op's own diagnostics verbatim: vault permission problems are
		// the most likely failure and must not be swallowed.
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("op %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// filterEnv returns env without any of the named variables.
func filterEnv(env []string, drop ...string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(drop, name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// serviceTokenPrefix is the prefix of every 1Password service account token.
// Checking it locally turns "op has no accounts configured" — which op prints
// for an empty or malformed token, never naming the token as the cause — into a
// message that says what is actually wrong.
const serviceTokenPrefix = "ops_"

func validateServiceToken(token string) error {
	switch {
	case token == "":
		return errors.New("the service token is empty (a terminal paste with echo off can silently produce nothing)")
	case !strings.HasPrefix(token, serviceTokenPrefix):
		return fmt.Errorf("does not look like a 1Password service account token: expected it to start with %q", serviceTokenPrefix)
	}
	return nil
}

// opField is one field of a 1Password item.
type opField struct {
	ID      string `json:"id,omitempty"`
	Label   string `json:"label"`
	Type    string `json:"type"` // STRING or CONCEALED
	Value   string `json:"value"`
	Purpose string `json:"purpose,omitempty"`
}

// opItem is the subset of the 1Password item JSON this tool reads and writes.
type opItem struct {
	ID       string    `json:"id,omitempty"`
	Title    string    `json:"title"`
	Category string    `json:"category"`
	Vault    *opVault  `json:"vault,omitempty"`
	Fields   []opField `json:"fields,omitempty"`
}

type opVault struct {
	Name string `json:"name,omitempty"`
}

func (i *opItem) field(label string) string {
	for _, f := range i.Fields {
		if strings.EqualFold(f.Label, label) || strings.EqualFold(f.ID, label) {
			return f.Value
		}
	}
	// Items created before the switch to API_CREDENTIAL are PASSWORD-category
	// and keep their secret in the primary password field. Keep reading those
	// so an existing vault does not need migrating.
	if strings.EqualFold(label, "private_key") || strings.EqualFold(label, "license") {
		for _, f := range i.Fields {
			if f.Purpose == "PASSWORD" {
				return f.Value
			}
		}
	}
	return ""
}

// tokenFile is the on-disk shape of .op.json. It holds only the service token
// and never any key material.
type tokenFile struct {
	ServiceToken string `json:"service_token"`
}

// readToken loads the service token from .op.json, returning "" when absent.
func readToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var tf tokenFile
	if err := json.Unmarshal(data, &tf); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	return strings.TrimSpace(tf.ServiceToken), nil
}

// writeToken persists the service token with owner-only permissions.
func writeToken(path, token string) error {
	data, err := json.MarshalIndent(tokenFile{ServiceToken: token}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

// checkVaultAccess verifies a token can actually reach the licenses vault.
func checkVaultAccess(r opRunner, token string) error {
	_, err := r.run(token, "", "vault", "get", vaultName, "--format", "json")
	return err
}

// getItem fetches one item by title. Returns nil when it does not exist.
func getItem(r opRunner, token, title string) (*opItem, error) {
	out, err := r.run(token, "", "item", "get", title, "--vault", vaultName, "--format", "json")
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	var item opItem
	if err := json.Unmarshal([]byte(out), &item); err != nil {
		return nil, fmt.Errorf("parse item %q: %w", title, err)
	}
	return &item, nil
}

func isNotFound(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "isn't an item") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "no item matches")
}

// listItemTitles returns the titles of every item in the licenses vault.
func listItemTitles(r opRunner, token string) ([]string, error) {
	out, err := r.run(token, "", "item", "list", "--vault", vaultName, "--format", "json")
	if err != nil {
		return nil, err
	}
	var items []opItem
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		return nil, fmt.Errorf("parse item list: %w", err)
	}
	titles := make([]string, 0, len(items))
	for _, it := range items {
		titles = append(titles, it.Title)
	}
	return titles, nil
}

// shareExpiry is how long an issued license's share link stays valid.
const shareExpiry = "7d"

// shareItem returns a 1Password share link for an item, restricted to the
// customer's email so only they can open it. 1Password does NOT send the mail:
// the link is printed for the operator to deliver.
func shareItem(r opRunner, token, title, email string) (string, error) {
	out, err := r.run(token, "", "item", "share", title,
		"--vault", vaultName, "--emails", email, "--expires-in", shareExpiry)
	if err != nil {
		return "", err
	}
	link := strings.TrimSpace(out)
	if link == "" {
		return "", errors.New("op returned no share link")
	}
	return link, nil
}

// createItem creates an item from a JSON template piped on stdin, so secret
// values never appear in argv.
func createItem(r opRunner, token string, item opItem) error {
	item.Vault = &opVault{Name: vaultName}
	payload, err := json.Marshal(item)
	if err != nil {
		return err
	}
	_, err = r.run(token, string(payload), "item", "create", "--format", "json", "-")
	return err
}
