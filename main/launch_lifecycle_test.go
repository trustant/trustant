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
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestManagedRuntimeHealthyForAppRequiresOwnedProcessGroupAndBothPorts(t *testing.T) {
	origWorkbench := WorkbenchDir
	WorkbenchDir = t.TempDir()
	t.Cleanup(func() { WorkbenchDir = origWorkbench })

	left, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen left: %s", err)
	}
	defer left.Close()
	right, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen right: %s", err)
	}
	defer right.Close()

	if err := os.WriteFile(filepath.Join(WorkbenchDir, "current"), []byte("trutest"), 0644); err != nil {
		t.Fatalf("write current: %s", err)
	}
	if err := os.WriteFile(filepath.Join(WorkbenchDir, "pgid"), []byte(strconv.Itoa(syscall.Getpgrp())), 0644); err != nil {
		t.Fatalf("write pgid: %s", err)
	}

	leftPort := left.Addr().(*net.TCPAddr).Port
	rightPort := right.Addr().(*net.TCPAddr).Port
	if !managedRuntimeHealthyForApp("trutest", leftPort, rightPort) {
		t.Fatal("expected matching app, live process group, and both listeners to be reusable")
	}
	if managedRuntimeHealthyForApp("another-app", leftPort, rightPort) {
		t.Fatal("must not reuse a runtime owned by another app")
	}

	if err := right.Close(); err != nil {
		t.Fatalf("close right listener: %s", err)
	}
	if managedRuntimeHealthyForApp("trutest", leftPort, rightPort) {
		t.Fatal("must not reuse a runtime when either shared service is unavailable")
	}
}

func TestRuntimeLifecycleLockSerializesOverlappingOperations(t *testing.T) {
	releaseFirst := lockRuntimeLifecycle("test first")
	firstReleased := false
	defer func() {
		if !firstReleased {
			releaseFirst()
		}
	}()

	entered := make(chan struct{})
	done := make(chan struct{})
	go func() {
		releaseSecond := lockRuntimeLifecycle("test second")
		close(entered)
		releaseSecond()
		close(done)
	}()

	select {
	case <-entered:
		t.Fatal("second lifecycle operation entered while the first still held the lock")
	case <-time.After(100 * time.Millisecond):
	}

	releaseFirst()
	firstReleased = true
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second lifecycle operation did not proceed after the first released the lock")
	}
}
