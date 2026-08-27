// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

// Package management implements the optional provider-neutral HTTPS adapter
// for the public lifecycle pairing workflow.
package management

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	public "github.com/singleaxis/singleaxis-fabric/tools/fabricctl/pkg/lifecycle"
)

const maxResponseBytes = 1 << 20

type Client struct {
	origin      *url.URL
	httpClient  *http.Client
	trustedKeys map[string]ed25519.PublicKey
	now         func() time.Time
}

func NewClient(origin string, httpClient *http.Client, trustedKeys map[string]ed25519.PublicKey, now func() time.Time) (*Client, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("management origin must be an HTTPS origin without credentials, path, query, or fragment")
	}
	if httpClient == nil || len(trustedKeys) == 0 {
		return nil, fmt.Errorf("management connector requires an HTTP client and receipt trust")
	}
	if now == nil {
		now = time.Now
	}
	return &Client{origin: parsed, httpClient: httpClient, trustedKeys: trustedKeys, now: now}, nil
}

type pairingResponse struct {
	PairingID           string    `json:"pairing_id"`
	DeviceCode          string    `json:"device_code"`
	UserCode            string    `json:"user_code"`
	VerificationURI     string    `json:"verification_uri"`
	ExpiresAt           time.Time `json:"expires_at"`
	PollIntervalSeconds int       `json:"poll_interval_seconds"`
}

func (c *Client) StartPairing(ctx context.Context, request public.PairingRequest) (public.PairingSession, error) {
	var response pairingResponse
	if err := c.postJSON(ctx, "/api/fabric/v1/pairings", request, &response); err != nil {
		return public.PairingSession{}, err
	}
	return public.PairingSession{PairingID: response.PairingID, DeviceCode: response.DeviceCode, UserCode: response.UserCode, VerificationURI: response.VerificationURI, ExpiresAt: response.ExpiresAt, PollInterval: time.Duration(response.PollIntervalSeconds) * time.Second}, nil
}

type pollRequest struct {
	DeviceCode string `json:"device_code"`
}

type pollResponse struct {
	Status    string    `json:"status"`
	GrantID   string    `json:"grant_id,omitempty"`
	Assertion string    `json:"assertion,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

func (c *Client) AwaitApproval(ctx context.Context, session public.PairingSession) (public.ConnectionGrant, error) {
	interval := session.PollInterval
	for {
		if !c.now().Before(session.ExpiresAt) {
			return public.ConnectionGrant{}, fmt.Errorf("pairing session expired")
		}
		var response pollResponse
		path := "/api/fabric/v1/pairings/" + url.PathEscape(session.PairingID) + "/poll"
		if err := c.postJSON(ctx, path, pollRequest{DeviceCode: session.DeviceCode}, &response); err != nil {
			return public.ConnectionGrant{}, err
		}
		switch response.Status {
		case "approved":
			return public.ConnectionGrant{GrantID: response.GrantID, Assertion: response.Assertion, ExpiresAt: response.ExpiresAt}, nil
		case "denied", "expired":
			return public.ConnectionGrant{}, fmt.Errorf("pairing was denied or expired")
		case "pending":
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return public.ConnectionGrant{}, ctx.Err()
			case <-timer.C:
			}
		default:
			return public.ConnectionGrant{}, fmt.Errorf("pairing provider returned an unsupported status")
		}
	}
}

type registrationRequest struct {
	GrantID   string                `json:"grant_id"`
	Assertion string                `json:"assertion"`
	Pairing   public.PairingRequest `json:"pairing"`
}

func (c *Client) RegisterWorkload(ctx context.Context, grant public.ConnectionGrant, request public.PairingRequest) (public.ConnectionReceipt, error) {
	body, err := c.postRaw(ctx, "/api/fabric/v1/registrations", registrationRequest{GrantID: grant.GrantID, Assertion: grant.Assertion, Pairing: request})
	if err != nil {
		return public.ConnectionReceipt{}, err
	}
	return public.VerifySignedConnectionReceipt(body, c.trustedKeys, request, c.now())
}

func (c *Client) postJSON(ctx context.Context, path string, requestBody, responseBody any) error {
	payload, err := c.postRaw(ctx, path, requestBody)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(responseBody); err != nil {
		return fmt.Errorf("management provider returned a malformed response")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("management provider returned trailing response content")
	}
	return nil
}

func (c *Client) postRaw(ctx context.Context, path string, requestBody any) ([]byte, error) {
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("management request cannot be encoded")
	}
	endpoint := *c.origin
	endpoint.Path = path
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("management request cannot be created")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("management provider cannot be reached over authenticated TLS")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		return nil, fmt.Errorf("management provider rejected the request")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return nil, fmt.Errorf("management provider response is unreadable or too large")
	}
	return body, nil
}
