// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package fabricpolicyprocessor

import (
	"context"
	"fmt"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processorhelper"
)

const typeStr = "fabricpolicy"

func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		func() component.Config { return createDefaultConfig() },
		processor.WithLogs(createLogsProcessor, component.StabilityLevelAlpha),
		processor.WithTraces(createTracesProcessor, component.StabilityLevelAlpha),
	)
}

func createLogsProcessor(
	ctx context.Context,
	set processor.Settings,
	cfg component.Config,
	next consumer.Logs,
) (processor.Logs, error) {
	pcfg, ok := cfg.(*Config)
	if !ok {
		return nil, fmt.Errorf("fabricpolicy: expected *Config, got %T", cfg)
	}
	if err := pcfg.Validate(); err != nil {
		return nil, err
	}
	p, err := newPolicy(ctx, pcfg, set.Logger)
	if err != nil {
		return nil, err
	}
	return processorhelper.NewLogs(
		ctx,
		set,
		pcfg,
		next,
		p.processLogs,
		processorhelper.WithCapabilities(consumer.Capabilities{MutatesData: true}),
	)
}

// createTracesProcessor applies the same Rego policy to the traces
// pipeline so spans are gated under identical egress rules.
func createTracesProcessor(
	ctx context.Context,
	set processor.Settings,
	cfg component.Config,
	next consumer.Traces,
) (processor.Traces, error) {
	pcfg, ok := cfg.(*Config)
	if !ok {
		return nil, fmt.Errorf("fabricpolicy: expected *Config, got %T", cfg)
	}
	if err := pcfg.Validate(); err != nil {
		return nil, err
	}
	p, err := newPolicy(ctx, pcfg, set.Logger)
	if err != nil {
		return nil, err
	}
	return processorhelper.NewTraces(
		ctx,
		set,
		pcfg,
		next,
		p.processTraces,
		processorhelper.WithCapabilities(consumer.Capabilities{MutatesData: true}),
	)
}
