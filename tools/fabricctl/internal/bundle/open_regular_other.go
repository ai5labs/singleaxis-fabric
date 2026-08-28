// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

//go:build !linux && !darwin

package bundle

import (
	"errors"
	"os"
)

var errArtifactNotRegular = errors.New("bundle artifact is not a regular file")

// openRegularArtifact provides a portable before/after identity check. Unix
// platforms additionally use nonblocking, no-follow open flags.
func openRegularArtifact(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, errArtifactNotRegular
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
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
