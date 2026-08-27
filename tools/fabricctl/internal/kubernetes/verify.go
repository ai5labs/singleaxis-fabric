// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"time"

	public "github.com/singleaxis/singleaxis-fabric/tools/fabricctl/pkg/lifecycle"
)

type IngressProber interface {
	Probe(ctx context.Context, target public.TargetIdentity) (string, error)
}

// Verify establishes only the coverage available from Kubernetes and a prior
// operation receipt. The ingress probe proves Collector acceptance only; the
// result deliberately remains partial until a destination acknowledgement
// adapter proves correlated persistence beyond that boundary.
func Verify(ctx context.Context, runner Runner, prober IngressProber, bundleDir string, expected *public.OperationReceipt, observedAt time.Time) (public.RuntimeVerification, error) {
	snapshot, err := Status(ctx, runner, bundleDir, expected, observedAt)
	if err != nil {
		return public.RuntimeVerification{}, err
	}
	componentsReady := len(snapshot.Components) > 0
	for _, component := range snapshot.Components {
		componentsReady = componentsReady && component.Ready
	}
	driftVerified := snapshot.Drift == "none-detected"
	syntheticStatus := "unverified"
	syntheticID := ""
	if prober != nil {
		syntheticID, err = prober.Probe(ctx, snapshot.Target)
		if err != nil {
			syntheticStatus = "fail"
		} else {
			syntheticStatus = "pass"
		}
	}
	checks := []public.Check{
		{ID: "runtime.cluster_identity", Status: "pass"},
		{ID: "runtime.components_ready", Status: passFail(componentsReady)},
		{ID: "runtime.manifest_receipt_binding", Status: passUnverified(driftVerified, expected != nil)},
		{ID: "runtime.synthetic_ingress", Status: syntheticStatus},
		{ID: "runtime.destination_acknowledgement", Status: "unverified"},
	}
	status := "partial"
	if !componentsReady || (expected != nil && !driftVerified) || syntheticStatus == "fail" {
		status = "failed"
	}
	return public.RuntimeVerification{
		SchemaVersion: public.RuntimeVerifySchema, ObservedAt: observedAt.UTC(), Status: status, SyntheticID: syntheticID, Checks: checks,
		Limitations: []string{
			"the selected destination did not provide a correlation acknowledgement",
			"component readiness does not prove privacy, policy, Relay delivery, or evidence persistence",
		},
	}, nil
}

func passFail(value bool) string {
	if value {
		return "pass"
	}
	return "fail"
}

func passUnverified(value, attempted bool) string {
	if !attempted {
		return "unverified"
	}
	return passFail(value)
}
