// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package fabricredactprocessor

import (
	"errors"
	"fmt"
	"time"
)

const (
	// ByteHandlingRedactUTF8 treats byte-valued pdata as content. Valid UTF-8 is
	// sent to the redaction sidecar and written back as bytes; non-UTF-8 fails
	// closed because the strings-only sidecar cannot inspect it safely.
	ByteHandlingRedactUTF8 = "redact_utf8"

	// ByteHandlingReject fails closed whenever a non-empty byte value is seen.
	ByteHandlingReject = "reject"

	// ByteHandlingPassthrough preserves the processor's legacy behavior. It is
	// intentionally opt-in because raw byte values can contain PII or secrets.
	ByteHandlingPassthrough = "passthrough"
)

// Config controls the Fabric redaction processor.
type Config struct {
	// UnixSocket is the absolute path of the Presidio sidecar's UDS.
	// Required.
	UnixSocket string `mapstructure:"unix_socket"`

	// Timeout bounds each /v1/redact call. Defaults to 500ms.
	Timeout time.Duration `mapstructure:"timeout"`

	// EventClassAttribute names the log-record attribute used as the
	// `<class>` prefix in the path sent to the sidecar.
	EventClassAttribute string `mapstructure:"event_class_attribute"`

	// SkipAttributes lists attribute keys that are never sent to the
	// sidecar (e.g. identifiers known to be safe). This is an explicit
	// privacy exemption: values under these top-level keys pass through raw.
	SkipAttributes []string `mapstructure:"skip_attributes"`

	// ByteHandling controls byte-valued log bodies and attributes. The secure
	// default is "redact_utf8". "passthrough" restores the historical behavior
	// and should only be used when an upstream control proves bytes are safe.
	ByteHandling string `mapstructure:"byte_handling"`
}

func (c *Config) Validate() error {
	if c.UnixSocket == "" {
		return errors.New("fabricredact: unix_socket is required")
	}
	if c.Timeout <= 0 {
		return errors.New("fabricredact: timeout must be > 0")
	}
	if c.EventClassAttribute == "" {
		return errors.New("fabricredact: event_class_attribute is required")
	}
	switch c.effectiveByteHandling() {
	case ByteHandlingRedactUTF8, ByteHandlingReject, ByteHandlingPassthrough:
	default:
		return fmt.Errorf("fabricredact: byte_handling must be one of %q, %q, or %q",
			ByteHandlingRedactUTF8, ByteHandlingReject, ByteHandlingPassthrough)
	}
	return nil
}

func (c *Config) effectiveByteHandling() string {
	if c.ByteHandling == "" {
		return ByteHandlingRedactUTF8
	}
	return c.ByteHandling
}

func createDefaultConfig() *Config {
	return &Config{
		Timeout:             500 * time.Millisecond,
		EventClassAttribute: "event_class",
		ByteHandling:        ByteHandlingRedactUTF8,
	}
}
