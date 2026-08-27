// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"testing"
	"time"

	public "github.com/singleaxis/singleaxis-fabric/tools/fabricctl/pkg/lifecycle"
)

func TestVerifyNeverConflatesReadinessWithDeliveryProof(t *testing.T) {
	options, _, _ := writePlanFixture(t)
	runner := statusRunner{uid: "cluster-uid", manifest: []byte("effective"), workloads: []byte(`{"items":[{"kind":"Deployment","metadata":{"name":"fabric-otel"},"spec":{"replicas":1},"status":{"readyReplicas":1}}]}`)}
	receipt := &public.OperationReceipt{EffectiveDigest: fileDigest(runner.manifest)}
	report, err := Verify(context.Background(), runner, nil, options.BundleDir, receipt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "partial" || len(report.Limitations) != 2 {
		t.Fatalf("verification overclaimed: %#v", report)
	}
	for _, check := range report.Checks {
		if check.ID == "runtime.destination_acknowledgement" && check.Status != "unverified" {
			t.Fatalf("delivery was overclaimed: %#v", report)
		}
	}
}
