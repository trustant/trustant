// Copyright 2025-2026 Nuvolaris Inc
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestOpsDevelLogIsPrivateAndOutsideWorkbench(t *testing.T) {
	root := t.TempDir()
	trustableRuntimeLogRootOverride = filepath.Join(root, "runtime")
	t.Cleanup(func() { trustableRuntimeLogRootOverride = "" })

	path, err := opsDevelLogPath("example")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	writer, err := openRotatingRuntimeLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("watcher ready\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("watcher log mode = %o, want 600", info.Mode().Perm())
	}
	directoryInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if directoryInfo.Mode().Perm() != 0700 {
		t.Fatalf("watcher log directory mode = %o, want 700", directoryInfo.Mode().Perm())
	}
}

func TestOpsDevelLogRotatesAtBoundedSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime", "example", "ops-ide-devel.log")
	writer, err := openRotatingRuntimeLog(path)
	if err != nil {
		t.Fatal(err)
	}
	writer.maxBytes = 64
	if _, err := writer.Write(bytes.Repeat([]byte("a"), 48)); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(bytes.Repeat([]byte("b"), 48)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if len(current) > 64 || len(previous) > 64 {
		t.Fatalf("rotated logs exceed bound: current=%d previous=%d", len(current), len(previous))
	}
}
