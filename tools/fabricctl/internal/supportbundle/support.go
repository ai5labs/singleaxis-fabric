// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

// Package supportbundle creates a local, allowlisted diagnostic artifact. It
// never uploads, reads environment variables, or copies desired-state files.
package supportbundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/bundle"
	public "github.com/singleaxis/singleaxis-fabric/tools/fabricctl/pkg/lifecycle"
)

const (
	ManifestSchema = "fabricctl.support-manifest/v1"
	ManifestName   = "support-manifest.json"
)

type Generator struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

type File struct {
	Path   string `json:"path"`
	Class  string `json:"class"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	SchemaVersion string    `json:"schema_version"`
	CreatedAt     time.Time `json:"created_at"`
	Generator     Generator `json:"generator"`
	Files         []File    `json:"files"`
	Excluded      []string  `json:"excluded"`
	Uploaded      bool      `json:"uploaded"`
	BundleDigest  string    `json:"bundle_digest"`
}

type Options struct {
	BundleDir string
	Receipt   *public.OperationReceipt
	OutputDir string
	Generator Generator
	Now       time.Time
}

type artifact struct {
	path    string
	class   string
	payload []byte
}

// Write creates a new directory containing only safe derived reports. It
// refuses to replace any existing directory or file.
func Write(options Options) (Manifest, error) {
	report := bundle.VerifyDirectory(options.BundleDir)
	if report.Status != "pass" {
		return Manifest{}, fmt.Errorf("support input bundle is not verified")
	}
	if options.Now.IsZero() {
		options.Now = time.Now()
	}
	reportPayload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return Manifest{}, fmt.Errorf("bundle verification report cannot be encoded")
	}
	artifacts := []artifact{{path: "bundle-verification-report.json", class: "derived-bundle-integrity", payload: append(reportPayload, '\n')}}
	environmentPayload, err := json.MarshalIndent(map[string]string{
		"schema_version": "fabricctl.support-environment/v1", "fabricctl_version": options.Generator.Version,
		"fabricctl_commit": options.Generator.Commit, "os": runtime.GOOS, "architecture": runtime.GOARCH,
	}, "", "  ")
	if err != nil {
		return Manifest{}, fmt.Errorf("support environment report cannot be encoded")
	}
	artifacts = append(artifacts, artifact{path: "environment.json", class: "allowlisted-runtime-identity", payload: append(environmentPayload, '\n')})
	if options.Receipt != nil {
		receiptPayload, err := json.MarshalIndent(options.Receipt, "", "  ")
		if err != nil {
			return Manifest{}, fmt.Errorf("operation receipt cannot be encoded")
		}
		artifacts = append(artifacts, artifact{path: "operation-receipt.json", class: "verified-operation-evidence", payload: append(receiptPayload, '\n')})
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].path < artifacts[j].path })
	manifest := Manifest{
		SchemaVersion: ManifestSchema, CreatedAt: options.Now.UTC(), Generator: options.Generator,
		Files: make([]File, 0, len(artifacts)), Uploaded: false,
		Excluded: []string{"raw prompts and responses", "tool payloads", "environment variables", "credentials and tokens", "Kubernetes Secrets", "desired-state source files", "cluster logs"},
	}
	hasher := sha256.New()
	for _, item := range artifacts {
		digest := sha256.Sum256(item.payload)
		hexDigest := hex.EncodeToString(digest[:])
		manifest.Files = append(manifest.Files, File{Path: item.path, Class: item.class, SHA256: hexDigest})
		hasher.Write([]byte(item.path))
		hasher.Write([]byte{0})
		hasher.Write([]byte(hexDigest))
		hasher.Write([]byte{'\n'})
	}
	manifest.BundleDigest = "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	manifestPayload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, fmt.Errorf("support manifest cannot be encoded")
	}
	artifacts = append(artifacts, artifact{path: ManifestName, class: "support-manifest", payload: append(manifestPayload, '\n')})
	if err := writeDirectory(options.OutputDir, artifacts); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func writeDirectory(path string, artifacts []artifact) error {
	if filepath.Clean(path) == "." || filepath.Clean(path) == string(filepath.Separator) {
		return fmt.Errorf("support output must be a new dedicated directory")
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		return fmt.Errorf("support output directory already exists or cannot be created")
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(path)
		}
	}()
	for _, item := range artifacts {
		file, err := os.OpenFile(filepath.Join(path, item.path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("support artifact cannot be created")
		}
		if _, err := file.Write(item.payload); err != nil {
			_ = file.Close()
			return fmt.Errorf("support artifact cannot be written")
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return fmt.Errorf("support artifact cannot be synced")
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("support artifact cannot be closed")
		}
	}
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("support directory cannot be opened")
	}
	err = directory.Sync()
	_ = directory.Close()
	if err != nil {
		return fmt.Errorf("support directory cannot be synced")
	}
	complete = true
	return nil
}
