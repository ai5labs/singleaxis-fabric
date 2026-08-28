// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	public "github.com/singleaxis/singleaxis-fabric/tools/fabricctl/pkg/lifecycle"
)

var forwardingPattern = regexp.MustCompile(`^Forwarding from 127\.0\.0\.1:([0-9]{1,5}) -> 4318$`)

// PortForwardProbe sends metadata-only OTLP/HTTP JSON through a loopback
// kubectl port-forward. HTTP success proves Collector ingress acceptance, not
// downstream persistence or destination acknowledgement.
type PortForwardProbe struct {
	HTTPClient *http.Client
}

func (p PortForwardProbe) Probe(ctx context.Context, target public.TargetIdentity) (string, error) {
	if p.HTTPClient == nil {
		p.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	serviceOutput, err := exec.CommandContext(ctx, "kubectl",
		"--context", target.Context, "--namespace", target.Namespace, "get", "service",
		"--selector", "app.kubernetes.io/instance="+target.ReleaseName+",app.kubernetes.io/name=otel-collector",
		"--output", "jsonpath={.items[0].metadata.name}").Output()
	if err != nil {
		return "", fmt.Errorf("Collector ingress service could not be discovered")
	}
	service := strings.TrimSpace(string(serviceOutput))
	if service == "" || len(service) > 63 || strings.ContainsAny(service, " /\\\t\r\n") {
		return "", fmt.Errorf("Collector ingress service identity is invalid")
	}
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(probeCtx, "kubectl",
		"--context", target.Context, "--namespace", target.Namespace, "port-forward", "service/"+service, ":4318")
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("Collector ingress tunnel could not be prepared")
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return "", fmt.Errorf("Collector ingress tunnel could not be started")
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	ports := make(chan int, 1)
	go func() {
		scanner := bufio.NewScanner(io.LimitReader(stdout, 4096))
		for scanner.Scan() {
			match := forwardingPattern.FindStringSubmatch(strings.TrimSpace(scanner.Text()))
			if len(match) == 2 {
				port, parseErr := strconv.Atoi(match[1])
				if parseErr == nil && port > 0 && port <= 65535 {
					ports <- port
					return
				}
			}
		}
	}()
	var port int
	select {
	case port = <-ports:
	case <-exited:
		return "", fmt.Errorf("Collector ingress tunnel exited before readiness")
	case <-time.After(15 * time.Second):
		return "", fmt.Errorf("Collector ingress tunnel did not become ready")
	case <-ctx.Done():
		return "", ctx.Err()
	}
	syntheticID, payload, err := syntheticTracePayload()
	if err != nil {
		return "", fmt.Errorf("synthetic decision identity could not be generated")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/v1/traces", port), bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("synthetic ingress request could not be created")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.HTTPClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("synthetic decision was not accepted at Collector ingress")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("synthetic decision was rejected at Collector ingress")
	}
	return syntheticID, nil
}

func syntheticTracePayload() (string, []byte, error) {
	traceID := make([]byte, 16)
	spanID := make([]byte, 8)
	synthetic := make([]byte, 16)
	if _, err := rand.Read(traceID); err != nil {
		return "", nil, err
	}
	if _, err := rand.Read(spanID); err != nil {
		return "", nil, err
	}
	if _, err := rand.Read(synthetic); err != nil {
		return "", nil, err
	}
	syntheticID := "synthetic/" + hex.EncodeToString(synthetic)
	now := time.Now().UnixNano()
	span := map[string]any{
		"traceId": hex.EncodeToString(traceID), "spanId": hex.EncodeToString(spanID), "name": "fabric.synthetic.decision",
		"kind": 1, "startTimeUnixNano": strconv.FormatInt(now, 10), "endTimeUnixNano": strconv.FormatInt(now+1_000_000, 10),
		"attributes": []any{map[string]any{"key": "fabric.synthetic_id", "value": map[string]any{"stringValue": syntheticID}}},
	}
	scopeSpan := map[string]any{"scope": map[string]any{"name": "fabricctl"}, "spans": []any{span}}
	resourceSpan := map[string]any{
		"resource": map[string]any{"attributes": []any{
			map[string]any{"key": "service.name", "value": map[string]any{"stringValue": "fabricctl-verifier"}},
			map[string]any{"key": "fabric.synthetic", "value": map[string]any{"boolValue": true}},
		}},
		"scopeSpans": []any{scopeSpan},
	}
	payload := map[string]any{"resourceSpans": []any{resourceSpan}}
	encoded, err := json.Marshal(payload)
	return syntheticID, encoded, err
}
