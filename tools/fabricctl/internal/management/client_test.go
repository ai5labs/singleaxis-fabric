// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package management

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	public "github.com/singleaxis/singleaxis-fabric/tools/fabricctl/pkg/lifecycle"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestTLSManagementAdapterKeepsBearerValuesInRequestBodiesAndVerifiesReceipt(t *testing.T) {
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("1", 64)
	pairing := public.PairingRequest{BundleDigest: digest, TargetDigest: digest, EffectiveDigest: digest, Target: public.TargetIdentity{Backend: "kubernetes-helm", ClusterUID: "cluster-1", ReleaseName: "fabric"}, Workload: public.WorkloadIdentity{Type: "spiffe", Reference: "spiffe/example/fabric"}}
	var sawDeviceBody, sawGrantBody bool
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.RawQuery != "" || strings.Contains(request.URL.String(), "device-code") || strings.Contains(request.URL.String(), "grant-assertion") {
			t.Errorf("bearer value leaked in URL: %s", request.URL)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/fabric/v1/pairings":
			_ = json.NewEncoder(writer).Encode(pairingResponse{PairingID: "pairing-1", DeviceCode: "device-code", UserCode: "ABCD", VerificationURI: "https://platform.example/pair", ExpiresAt: now.Add(10 * time.Minute), PollIntervalSeconds: 1})
		case "/api/fabric/v1/pairings/pairing-1/poll":
			var body pollRequest
			_ = json.NewDecoder(request.Body).Decode(&body)
			sawDeviceBody = body.DeviceCode == "device-code"
			_ = json.NewEncoder(writer).Encode(pollResponse{Status: "approved", GrantID: "grant-1", Assertion: "grant-assertion", ExpiresAt: now.Add(time.Minute)})
		case "/api/fabric/v1/registrations":
			var body registrationRequest
			_ = json.NewDecoder(request.Body).Decode(&body)
			sawGrantBody = body.Assertion == "grant-assertion"
			receipt := public.ConnectionReceipt{SchemaVersion: public.ConnectionReceiptSchema, ConnectionID: "connection-1", Mode: "singleaxis-saas", EndpointOrigin: "https://platform.example", WorkloadRef: pairing.Workload.Reference, EffectiveDigest: digest, ConnectedAt: now, CredentialStored: false}
			signed, _ := json.Marshal(receipt)
			_ = json.NewEncoder(writer).Encode(public.SignedConnectionReceipt{SchemaVersion: public.SignedConnectionReceiptSchema, KeyID: "management-2026", Receipt: receipt, Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, signed))})
		default:
			http.NotFound(writer, request)
		}
	})
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		response := recorder.Result()
		response.Request = request
		if response.Body == nil {
			response.Body = io.NopCloser(strings.NewReader(""))
		}
		return response, nil
	})}
	client, err := NewClient("https://platform.example", httpClient, map[string]ed25519.PublicKey{"management-2026": publicKey}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := public.ConnectManagement(context.Background(), client, pairing, nil, now)
	if err != nil || receipt.ConnectionID != "connection-1" || !sawDeviceBody || !sawGrantBody {
		t.Fatalf("ConnectManagement() = %#v err=%v device=%v grant=%v", receipt, err, sawDeviceBody, sawGrantBody)
	}
}
