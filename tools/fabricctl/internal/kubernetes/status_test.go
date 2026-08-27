// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	public "github.com/singleaxis/singleaxis-fabric/tools/fabricctl/pkg/lifecycle"
)

type statusRunner struct {
	uid       string
	manifest  []byte
	workloads []byte
}

func (r statusRunner) Run(_ context.Context, name string, args []string, _ []byte) ([]byte, error) {
	joined := strings.Join(args, " ")
	switch {
	case name == "kubectl" && strings.Contains(joined, "namespace kube-system"):
		return []byte(r.uid), nil
	case name == "helm" && strings.HasPrefix(joined, "get manifest"):
		return r.manifest, nil
	case name == "kubectl" && strings.Contains(joined, "deployments,statefulsets,daemonsets"):
		return r.workloads, nil
	default:
		return nil, fmt.Errorf("unexpected command")
	}
}

func TestStatusReportsReceiptBoundDriftAndReadiness(t *testing.T) {
	options, _, _ := writePlanFixture(t)
	runner := statusRunner{uid: "cluster-uid", manifest: []byte("effective"), workloads: []byte(`{"items":[{"kind":"Deployment","metadata":{"name":"fabric-otel"},"spec":{"replicas":2},"status":{"readyReplicas":2}},{"kind":"StatefulSet","metadata":{"name":"fabric-relay"},"spec":{"replicas":1},"status":{"readyReplicas":0,"currentRevision":"relay-1"}}]}`)}
	digest := fileDigest(runner.manifest)
	receipt := &public.OperationReceipt{EffectiveDigest: digest}
	snapshot, err := Status(context.Background(), runner, options.BundleDir, receipt, time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Drift != "none-detected" || snapshot.Delivery != "unverified" || len(snapshot.Components) != 2 || !snapshot.Components[0].Ready || snapshot.Components[1].Ready {
		t.Fatalf("unexpected status: %#v", snapshot)
	}
	receipt.EffectiveDigest = "sha256:" + strings.Repeat("0", 64)
	snapshot, err = Status(context.Background(), runner, options.BundleDir, receipt, time.Now())
	if err != nil || snapshot.Drift != "detected" {
		t.Fatalf("changed status: %#v %v", snapshot, err)
	}
}
