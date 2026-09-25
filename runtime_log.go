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
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const opsDevelLogMaxBytes int64 = 1024 * 1024

var trustantRuntimeLogRootOverride string

// opsDevelLogPath keeps watcher diagnostics outside the model-editable
// workbench. WHY: Pi needs authoritative deployment evidence, while application
// code must not be able to forge or delete the host-owned log it consumes.
func opsDevelLogPath(app string) (string, error) {
	if !namePattern.MatchString(app) {
		return "", fmt.Errorf("invalid application name for watcher log: %q", app)
	}
	root := trustantRuntimeLogRootOverride
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to resolve home for watcher log: %w", err)
		}
		root = filepath.Join(home, ".config", "trustant", "runtime")
	}
	return filepath.Join(root, app, "ops-ide-devel.log"), nil
}

type rotatingRuntimeLog struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	size     int64
	maxBytes int64
}

// openRotatingRuntimeLog creates a private two-generation log. WHY: ops ide
// devel is intentionally long-lived, so a plain append-only file would turn
// observability into an unbounded disk-growth failure.
func openRotatingRuntimeLog(path string) (*rotatingRuntimeLog, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("failed to create watcher log directory: %w", err)
	}
	// WHY: MkdirAll preserves an existing directory's permissions. Tighten an
	// older or manually-created path before writing diagnostics that may contain
	// backend details.
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, fmt.Errorf("failed to protect watcher log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open watcher log: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to protect watcher log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to inspect watcher log: %w", err)
	}
	writer := &rotatingRuntimeLog{
		path:     path,
		file:     file,
		size:     info.Size(),
		maxBytes: opsDevelLogMaxBytes,
	}
	if writer.size >= writer.maxBytes {
		if err := writer.rotateLocked(); err != nil {
			file.Close()
			return nil, err
		}
	}
	return writer, nil
}

func (writer *rotatingRuntimeLog) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()

	originalLength := len(data)
	if int64(len(data)) > writer.maxBytes {
		data = data[len(data)-int(writer.maxBytes):]
	}
	if writer.size+int64(len(data)) > writer.maxBytes {
		if err := writer.rotateLocked(); err != nil {
			return 0, err
		}
	}
	written, err := writer.file.Write(data)
	writer.size += int64(written)
	if err != nil {
		return written, err
	}
	// io.Writer callers expect the input length even when an oversized single
	// write was deliberately reduced to its most recent bounded tail.
	return originalLength, nil
}

func (writer *rotatingRuntimeLog) rotateLocked() error {
	if writer.file != nil {
		if err := writer.file.Close(); err != nil {
			return fmt.Errorf("failed to close watcher log before rotation: %w", err)
		}
	}
	previous := writer.path + ".1"
	if err := os.Remove(previous); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove previous watcher log: %w", err)
	}
	if err := os.Rename(writer.path, previous); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to rotate watcher log: %w", err)
	}
	file, err := os.OpenFile(writer.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create rotated watcher log: %w", err)
	}
	writer.file = file
	writer.size = 0
	return nil
}

func (writer *rotatingRuntimeLog) Close() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.file == nil {
		return nil
	}
	err := writer.file.Close()
	writer.file = nil
	return err
}
