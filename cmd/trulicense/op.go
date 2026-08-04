package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	vaultName     = "NuvolarisLicenses"
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
	cmd := exec.Command("op", args...)
	// The token goes through the environment, never on the command line.
	cmd.Env = append(os.Environ(), "OP_SERVICE_ACCOUNT_TOKEN="+token)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
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
