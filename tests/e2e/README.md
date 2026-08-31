# End-to-end tests

This directory contains release-test workloads and controlled test support. It
is not part of the Fabric runtime or any published SDK, image, chart, CLI or
contracts archive.

`healthcare_shadow/` exercises a realistic ambient-clinical documentation
workflow in passive shadow mode. The external model, FHIR records and EHR
staging response are deterministic simulations. The public Fabric Python SDK,
OTLP exporter, released Collector image, privacy processor, persistent queue
and Kubernetes deployment are real.

The test proves observable behavior only: capture, metadata-only protection,
causal correlation and at-least-once recovery to a controlled fsync sink. It
does not claim clinical correctness, runtime enforcement, exactly-once
delivery or durable persistence by an arbitrary customer destination.
