// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package initializer

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/bundle"
	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/recorder"
)

type recorderInitReceipt struct {
	SchemaVersion      string           `json:"schema_version"`
	Status             string           `json:"status"`
	Operation          string           `json:"operation"`
	Network            bool             `json:"network"`
	RuntimeMutation    bool             `json:"runtime_mutation"`
	InstallationStatus string           `json:"installation_status"`
	Recorder           recorderIdentity `json:"recorder"`
	Configuration      recorderFile     `json:"configuration"`
	Generator          bundle.Generator `json:"generator"`
}

type recorderIdentity struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type recorderFile struct {
	File string `json:"file"`
}

func runRecorder(options Options) (*Result, error) {
	if err := validateOptions(options); err != nil {
		return nil, err
	}
	wizard := wizard{reader: bufio.NewScanner(options.Input), output: options.Output}
	wizard.reader.Buffer(make([]byte, 1024), 4096)

	resource, err := wizard.collectRecorder()
	if err != nil {
		return nil, err
	}
	payload, err := recorder.Render(resource)
	if err != nil {
		return nil, fmt.Errorf("validate recorder configuration: %w", err)
	}
	digest, err := recorder.Digest(resource)
	if err != nil {
		return nil, fmt.Errorf("digest recorder configuration: %w", err)
	}
	generator := options.Generator
	if generator.Name == "" {
		generator = bundle.Generator{Name: "fabricctl", Version: "0.0.0-dev", Commit: strings.Repeat("0", 40)}
	}
	receiptPayload, err := json.MarshalIndent(recorderInitReceipt{
		SchemaVersion:      "fabricctl.recorder-init-receipt/v1",
		Status:             "prepared",
		Operation:          "prepare",
		Network:            false,
		RuntimeMutation:    false,
		InstallationStatus: "not-installed",
		Recorder:           recorderIdentity{Name: resource.Metadata.Name, Digest: digest},
		Configuration:      recorderFile{File: recorder.FileName},
		Generator:          generator,
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render recorder initialization receipt: %w", err)
	}
	receiptPayload = append(receiptPayload, '\n')

	fmt.Fprintln(options.Output, "\nReview recorder configuration:")
	fmt.Fprintln(options.Output, strings.TrimSuffix(string(payload), "\n"))
	fmt.Fprintf(options.Output, "\nConfiguration digest: %s\n", digest)
	fmt.Fprintln(options.Output, "This prepares passive CAPTURE -> PROTECT -> DELIVER configuration only.")
	fmt.Fprintln(options.Output, "It does not install Fabric, contact the destination, inspect agent traffic, or change the monitored system.")
	confirmation, err := wizard.readLine(`Type "write" to create the recorder configuration and preparation receipt: `)
	if err != nil {
		return nil, err
	}
	if confirmation != "write" {
		fmt.Fprintln(options.Output, "Initialization cancelled; no files were written.")
		return nil, ErrDeclined
	}

	paths, err := writeRecorderArtifacts(options.OutputDir, payload, receiptPayload)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(options.Output, "Created %s\n", paths.configuration)
	fmt.Fprintf(options.Output, "Created %s\n", paths.receipt)
	fmt.Fprintf(options.Output, "Recorder configuration prepared: %s\n", digest)
	fmt.Fprintln(options.Output, "Installation status: not-installed")
	return &Result{
		Recorder:        &resource,
		RecorderPath:    paths.configuration,
		InitReceiptPath: paths.receipt,
		RecorderDigest:  digest,
	}, nil
}

func validateOptions(options Options) error {
	if options.Input == nil {
		return errors.New("initializer input is required")
	}
	if options.Output == nil {
		return errors.New("initializer output is required")
	}
	if strings.TrimSpace(options.OutputDir) == "" {
		return errors.New("initializer output directory is required")
	}
	if !options.Interactive {
		return ErrInteractiveTerminalRequired
	}
	return nil
}

func (w *wizard) collectRecorder() (recorder.Resource, error) {
	fmt.Fprintln(w.output, "SingleAxis Fabric OSS recorder initializer")
	fmt.Fprintln(w.output, "Prepare passive CAPTURE -> PROTECT -> DELIVER configuration.")
	fmt.Fprintln(w.output, "This offline wizard asks only for identifiers, references, and a configuration digest; never secret values.")
	fmt.Fprintln(w.output)

	name, err := w.required("Recorder name (lowercase DNS-style)", validName)
	if err != nil {
		return recorder.Resource{}, err
	}
	systemID, err := w.recorderReference("Monitored system identity")
	if err != nil {
		return recorder.Resource{}, err
	}
	deploymentID, err := w.recorderReference("Monitored deployment identity")
	if err != nil {
		return recorder.Resource{}, err
	}
	inputMethod, err := w.choice("Input method", []choice{
		{"1", "otlp", "receive existing OpenTelemetry activity"},
		{"2", "http", "receive authenticated Fabric activity events"},
		{"3", "sdk", "instrument customer-controlled code"},
		{"4", "adapter", "use a reviewed framework or vendor adapter"},
	})
	if err != nil {
		return recorder.Resource{}, err
	}
	contentMode, err := w.choice("Exported content mode", []choice{
		{"1", "metadata", "export allowlisted metadata only"},
		{"2", "hash", "export governed hashes; hashes are not anonymization"},
		{"3", "governed-reference", "export references to customer-governed content"},
	})
	if err != nil {
		return recorder.Resource{}, err
	}
	privacyPolicyRef, err := w.recorderReference("Privacy policy reference")
	if err != nil {
		return recorder.Resource{}, err
	}
	configDigest, err := w.required("Reviewed privacy configuration digest (sha256:...)", func(value string) bool {
		return digestPattern.MatchString(value)
	})
	if err != nil {
		return recorder.Resource{}, err
	}
	destinationRef, err := w.recorderReference("Approved destination reference")
	if err != nil {
		return recorder.Resource{}, err
	}
	installationRef, err := w.recorderReference("Installation reference")
	if err != nil {
		return recorder.Resource{}, err
	}
	return recorder.Resource{
		APIVersion: recorder.APIVersion,
		Kind:       recorder.Kind,
		Metadata:   recorder.Metadata{Name: name},
		Spec: recorder.Spec{
			Identity:     recorder.Identity{RecorderID: name, SystemID: systemID, DeploymentID: deploymentID},
			Input:        recorder.Input{Method: inputMethod},
			Content:      recorder.Content{Mode: contentMode},
			Protect:      recorder.Protect{PrivacyPolicyRef: privacyPolicyRef, ConfigDigest: configDigest},
			Destination:  recorder.Reference{Ref: destinationRef},
			Installation: recorder.Reference{Ref: installationRef},
		},
	}, nil
}

func (w *wizard) recorderReference(label string) (string, error) {
	fmt.Fprintln(w.output, "Provide a non-secret identifier or configuration reference only; do not paste a URL, credential, or sensitive value.")
	return w.required(label, func(value string) bool {
		return validReference(value) && !strings.Contains(value, "://")
	})
}

type recorderPaths struct {
	directory, configuration, receipt string
}

func writeRecorderArtifacts(outputDir string, configuration, receipt []byte) (recorderPaths, error) {
	canonicalDirectory, err := canonicalizeDirectory(outputDir)
	if err != nil {
		return recorderPaths{}, err
	}
	paths := recorderPaths{
		directory:     canonicalDirectory,
		configuration: filepath.Join(canonicalDirectory, recorder.FileName),
		receipt:       filepath.Join(canonicalDirectory, recorder.InitReceiptName),
	}
	if err := prepareOutputDirectory(paths.directory); err != nil {
		return recorderPaths{}, err
	}
	for _, path := range []string{paths.configuration, paths.receipt} {
		info, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return recorderPaths{}, fmt.Errorf("inspect initializer target: %w", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return recorderPaths{}, fmt.Errorf("%w: %s", ErrSymlinkTarget, path)
		}
		return recorderPaths{}, fmt.Errorf("%w: %s", ErrTargetExists, path)
	}
	if err := createFinal(paths.configuration, configuration); err != nil {
		return recorderPaths{}, err
	}
	if err := createFinal(paths.receipt, receipt); err != nil {
		if rollbackErr := os.Remove(paths.configuration); rollbackErr != nil && !errors.Is(rollbackErr, os.ErrNotExist) {
			return recorderPaths{}, errors.Join(err, fmt.Errorf("roll back newly created recorder configuration: %w", rollbackErr))
		}
		return recorderPaths{}, err
	}
	return paths, nil
}
