// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type fakeManagementConnector struct {
	now     time.Time
	request PairingRequest
}

func TestVerifySignedConnectionReceiptBindsProviderEvidence(t *testing.T) {
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	digest := "sha256:" + strings.Repeat("1", 64)
	request := PairingRequest{SchemaVersion: PairingRequestSchema, BundleDigest: digest, TargetDigest: digest, EffectiveDigest: digest, Target: TargetIdentity{Backend: "kubernetes-helm", ClusterUID: "cluster-1", ReleaseName: "fabric"}, Workload: WorkloadIdentity{Type: "spiffe", Reference: "spiffe/example/fabric"}}
	receipt := ConnectionReceipt{SchemaVersion: ConnectionReceiptSchema, ConnectionID: "connection/1", Mode: "singleaxis-saas", EndpointOrigin: "https://platform.example", WorkloadRef: request.Workload.Reference, EffectiveDigest: digest, ConnectedAt: now, CredentialStored: false}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signedPayload, _ := json.Marshal(receipt)
	envelope := SignedConnectionReceipt{SchemaVersion: SignedConnectionReceiptSchema, KeyID: "management-2026", Receipt: receipt, Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, signedPayload))}
	payload, _ := json.Marshal(envelope)
	verified, err := VerifySignedConnectionReceipt(payload, map[string]ed25519.PublicKey{"management-2026": publicKey}, request, now)
	if err != nil || verified.ConnectionID != receipt.ConnectionID {
		t.Fatalf("VerifySignedConnectionReceipt() = %#v, %v", verified, err)
	}
	envelope.Receipt.EffectiveDigest = "sha256:" + strings.Repeat("2", 64)
	tampered, _ := json.Marshal(envelope)
	if _, err := VerifySignedConnectionReceipt(tampered, map[string]ed25519.PublicKey{"management-2026": publicKey}, request, now); err == nil {
		t.Fatal("tampered connection receipt passed")
	}
}

func (f *fakeManagementConnector) StartPairing(_ context.Context, request PairingRequest) (PairingSession, error) {
	f.request = request
	return PairingSession{PairingID: "pairing/1", DeviceCode: "ephemeral-device-code", UserCode: "ABCD-EFGH", VerificationURI: "https://platform.example/pair", ExpiresAt: f.now.Add(10 * time.Minute), PollInterval: time.Second}, nil
}

func (f *fakeManagementConnector) AwaitApproval(_ context.Context, session PairingSession) (ConnectionGrant, error) {
	if session.DeviceCode != "ephemeral-device-code" {
		return ConnectionGrant{}, context.Canceled
	}
	return ConnectionGrant{GrantID: "grant/1", Assertion: "ephemeral-registration-assertion", ExpiresAt: f.now.Add(time.Minute)}, nil
}

func (f *fakeManagementConnector) RegisterWorkload(_ context.Context, grant ConnectionGrant, request PairingRequest) (ConnectionReceipt, error) {
	return ConnectionReceipt{SchemaVersion: ConnectionReceiptSchema, ConnectionID: "connection/1", Mode: "singleaxis-private", EndpointOrigin: "https://platform.example", TenantRef: "tenant/acme", WorkloadRef: request.Workload.Reference, EffectiveDigest: request.EffectiveDigest, ConnectedAt: f.now, CredentialStored: false}, nil
}

func TestConnectManagementKeepsBearerMaterialOutOfPromptAndReceipt(t *testing.T) {
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	digest := "sha256:" + strings.Repeat("1", 64)
	connector := &fakeManagementConnector{now: now}
	request := PairingRequest{BundleDigest: digest, TargetDigest: digest, EffectiveDigest: digest, Target: TargetIdentity{Backend: "kubernetes-helm", ClusterUID: "cluster-1", ReleaseName: "fabric"}, Workload: WorkloadIdentity{Type: "spiffe", Reference: "spiffe/example/fabric"}}
	var prompt PairingPrompt
	receipt, err := ConnectManagement(context.Background(), connector, request, func(value PairingPrompt) error { prompt = value; return nil }, now)
	if err != nil || prompt.UserCode != "ABCD-EFGH" || receipt.ConnectionID != "connection/1" || receipt.CredentialStored {
		t.Fatalf("ConnectManagement() = %#v prompt=%#v err=%v", receipt, prompt, err)
	}
	payload, err := json.Marshal(struct {
		Request PairingRequest    `json:"request"`
		Receipt ConnectionReceipt `json:"receipt"`
	}{Request: connector.request, Receipt: receipt})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "ephemeral-device-code") || strings.Contains(string(payload), "ephemeral-registration-assertion") {
		t.Fatalf("bearer material serialized: %s", payload)
	}
}
