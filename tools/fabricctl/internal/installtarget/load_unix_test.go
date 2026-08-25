// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

//go:build linux || darwin

package installtarget

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestFileLoaderRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("FIFO unavailable: %v", err)
	}
	_, err := LoadFile(path)
	assertDocumentErrorID(t, err, "installtarget.file.not_regular")
	if removeErr := os.Remove(path); removeErr != nil {
		t.Fatal(removeErr)
	}
}
