// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/deployment"
)

const PairingRequestSchema = "fabricctl.pairing-request/v1"

const SignedConnectionReceiptSchema = "fabricctl.signed-connection-receipt/v1"

type WorkloadIdentity struct {
	Type      string `json:"type"`
	Reference string `json:"reference"`
}

// PairingRequest contains stable public identity only. It has no field capable
// of carrying a workload token, private key, prompt, or telemetry content.
type PairingRequest struct {
	SchemaVersion   string           `json:"schema_version"`
	BundleDigest    string           `json:"bundle_digest"`
	TargetDigest    string           `json:"target_digest"`
	EffectiveDigest string           `json:"effective_digest"`
	Target          TargetIdentity   `json:"target"`
	Workload        WorkloadIdentity `json:"workload"`
}

// PairingSession is held in memory only. DeviceCode is deliberately omitted
// from JSON so debug serialization cannot persist the bearer credential.
type PairingSession struct {
	PairingID       string        `json:"pairing_id"`
	DeviceCode      string        `json:"-"`
	UserCode        string        `json:"user_code"`
	VerificationURI string        `json:"verification_uri"`
	ExpiresAt       time.Time     `json:"expires_at"`
	PollInterval    time.Duration `json:"-"`
}

type PairingPrompt struct {
	UserCode        string
	VerificationURI string
	ExpiresAt       time.Time
}

// ConnectionGrant is short-lived in-memory authority. Assertion is never
// serialized by the shared workflow or placed in a connection receipt.
type ConnectionGrant struct {
	GrantID   string    `json:"grant_id"`
	Assertion string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ManagementConnector is the OSS adapter seam implemented by SingleAxis SaaS,
// customer-hosted SingleAxis, or another approved management provider.
type ManagementConnector interface {
	StartPairing(ctx context.Context, request PairingRequest) (PairingSession, error)
	AwaitApproval(ctx context.Context, session PairingSession) (ConnectionGrant, error)
	RegisterWorkload(ctx context.Context, grant ConnectionGrant, request PairingRequest) (ConnectionReceipt, error)
}

type SignedConnectionReceipt struct {
	SchemaVersion string            `json:"schema_version"`
	KeyID         string            `json:"key_id"`
	Receipt       ConnectionReceipt `json:"receipt"`
	Signature     string            `json:"signature"`
}

// VerifySignedConnectionReceipt verifies provider evidence before an HTTP
// connector returns it to the shared pairing workflow.
func VerifySignedConnectionReceipt(payload []byte, trustedKeys map[string]ed25519.PublicKey, expected PairingRequest, now time.Time) (ConnectionReceipt, error) {
	value, err := deployment.LoadBytes(payload, "json")
	if err != nil {
		return ConnectionReceipt{}, fmt.Errorf("signed connection receipt cannot be safely decoded")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return ConnectionReceipt{}, fmt.Errorf("signed connection receipt cannot be normalized")
	}
	var envelope SignedConnectionReceipt
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return ConnectionReceipt{}, fmt.Errorf("signed connection receipt does not satisfy the strict contract")
	}
	if _, err := decoder.Token(); err != io.EOF || envelope.SchemaVersion != SignedConnectionReceiptSchema || !validApprovalReference(envelope.KeyID) {
		return ConnectionReceipt{}, fmt.Errorf("signed connection receipt is malformed")
	}
	publicKey, ok := trustedKeys[envelope.KeyID]
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return ConnectionReceipt{}, fmt.Errorf("connection receipt signing key is not trusted")
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return ConnectionReceipt{}, fmt.Errorf("connection receipt signature is malformed")
	}
	signedPayload, err := json.Marshal(envelope.Receipt)
	if err != nil || !ed25519.Verify(publicKey, signedPayload, signature) {
		return ConnectionReceipt{}, fmt.Errorf("connection receipt signature is invalid")
	}
	if err := validateConnectionReceipt(envelope.Receipt, expected, now); err != nil {
		return ConnectionReceipt{}, err
	}
	return envelope.Receipt, nil
}

// ConnectManagement runs the provider-neutral pairing lifecycle. Management
// availability never enters the agent request path; this is an operator flow.
func ConnectManagement(ctx context.Context, connector ManagementConnector, request PairingRequest, present func(PairingPrompt) error, now time.Time) (ConnectionReceipt, error) {
	request.SchemaVersion = PairingRequestSchema
	if err := validatePairingRequest(request); err != nil {
		return ConnectionReceipt{}, err
	}
	session, err := connector.StartPairing(ctx, request)
	if err != nil {
		return ConnectionReceipt{}, fmt.Errorf("management provider did not start pairing")
	}
	if err := validatePairingSession(session, now); err != nil {
		return ConnectionReceipt{}, err
	}
	if present != nil {
		if err := present(PairingPrompt{UserCode: session.UserCode, VerificationURI: session.VerificationURI, ExpiresAt: session.ExpiresAt}); err != nil {
			return ConnectionReceipt{}, fmt.Errorf("pairing prompt could not be presented")
		}
	}
	grant, err := connector.AwaitApproval(ctx, session)
	if err != nil {
		return ConnectionReceipt{}, fmt.Errorf("management pairing was not approved")
	}
	if grant.GrantID == "" || grant.Assertion == "" || !grant.ExpiresAt.After(now) {
		return ConnectionReceipt{}, fmt.Errorf("management provider returned an invalid registration grant")
	}
	receipt, err := connector.RegisterWorkload(ctx, grant, request)
	if err != nil {
		return ConnectionReceipt{}, fmt.Errorf("workload identity registration failed")
	}
	if err := validateConnectionReceipt(receipt, request, now); err != nil {
		return ConnectionReceipt{}, err
	}
	return receipt, nil
}

func validatePairingRequest(value PairingRequest) error {
	if value.SchemaVersion != PairingRequestSchema || !approvalDigestPattern.MatchString(value.BundleDigest) ||
		!approvalDigestPattern.MatchString(value.TargetDigest) || !approvalDigestPattern.MatchString(value.EffectiveDigest) ||
		value.Target.Backend == "" || value.Target.ReleaseName == "" || value.Target.ClusterUID == "" ||
		!validWorkloadIdentity(value.Workload) {
		return fmt.Errorf("pairing request is incomplete or malformed")
	}
	return nil
}

func validWorkloadIdentity(value WorkloadIdentity) bool {
	if !validApprovalReference(value.Reference) {
		return false
	}
	switch value.Type {
	case "spiffe", "oidc", "kubernetes-service-account":
		return true
	default:
		return false
	}
}

func validatePairingSession(value PairingSession, now time.Time) error {
	verificationURI, err := url.Parse(value.VerificationURI)
	if err != nil || verificationURI.Scheme != "https" || verificationURI.Host == "" || verificationURI.User != nil ||
		value.PairingID == "" || value.DeviceCode == "" || strings.TrimSpace(value.UserCode) == "" ||
		!value.ExpiresAt.After(now) || value.ExpiresAt.Sub(now) > 20*time.Minute || value.PollInterval < time.Second || value.PollInterval > 30*time.Second {
		return fmt.Errorf("management provider returned an invalid pairing session")
	}
	return nil
}

func validateConnectionReceipt(value ConnectionReceipt, request PairingRequest, now time.Time) error {
	if value.SchemaVersion != ConnectionReceiptSchema || value.ConnectionID == "" ||
		(value.Mode != "singleaxis-saas" && value.Mode != "singleaxis-private") ||
		value.EffectiveDigest != request.EffectiveDigest || value.WorkloadRef != request.Workload.Reference ||
		value.ConnectedAt.After(now.Add(5*time.Minute)) || value.ConnectedAt.Before(now.Add(-5*time.Minute)) || value.CredentialStored {
		return fmt.Errorf("connection receipt is inconsistent with the registered workload")
	}
	endpoint, err := url.Parse(value.EndpointOrigin)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return fmt.Errorf("connection receipt contains an invalid provider origin")
	}
	return nil
}
