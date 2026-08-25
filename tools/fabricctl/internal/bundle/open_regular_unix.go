// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

//go:build linux || darwin

package bundle

import (
	"errors"
	"os"
	"syscall"
)

var errArtifactNotRegular = errors.New("bundle artifact is not a regular file")

// openRegularArtifact rejects non-regular inputs before reading and opens the
// checked path without following a final symlink. O_NONBLOCK prevents a path
// swapped to a FIFO or device from blocking during open.
func openRegularArtifact(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, errArtifactNotRegular
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, errArtifactNotRegular
		}
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, errors.New("cannot create bundle artifact descriptor")
	}
	after, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, errArtifactNotRegular
	}
	return file, nil
}
