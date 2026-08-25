// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

//go:build linux || darwin

package deployment

import (
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
)

func TestLoadFileRejectsFIFOAndDeviceWithoutBlocking(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "deployment.pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	assertDocumentError(t, fifo, "deployment.file.not_regular")
	assertDocumentError(t, "/dev/null", "deployment.file.not_regular")
}

func TestLoadFileNeverFollowsRacingFinalSymlink(t *testing.T) {
	directory := t.TempDir()
	trusted := filepath.Join(directory, "trusted.json")
	malicious := filepath.Join(directory, "malicious.json")
	input := filepath.Join(directory, "deployment.json")
	if err := os.WriteFile(trusted, []byte(`{"source":"trusted"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(malicious, []byte(`{"source":"malicious"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(trusted, input); err != nil {
		t.Fatal(err)
	}

	var stop atomic.Bool
	writerDone := make(chan error, 1)
	go func() {
		for index := 0; !stop.Load(); index++ {
			candidate := filepath.Join(directory, "candidate")
			_ = os.Remove(candidate)
			var err error
			if index%2 == 0 {
				err = os.Symlink(malicious, candidate)
			} else {
				err = os.Link(trusted, candidate)
			}
			if err != nil {
				writerDone <- err
				return
			}
			if err := os.Rename(candidate, input); err != nil {
				writerDone <- err
				return
			}
			runtime.Gosched()
		}
		writerDone <- nil
	}()

	for index := 0; index < 1000; index++ {
		value, err := LoadFile(input)
		if err != nil {
			continue // rejecting a raced path is safe and expected
		}
		object, ok := value.(map[string]any)
		if !ok || object["source"] != "trusted" {
			stop.Store(true)
			<-writerDone
			t.Fatalf("loaded content through a racing symbolic link: %#v", value)
		}
	}
	stop.Store(true)
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
}
