# Loom walkthrough script (5 minutes)

Storyboard for the recorded demo. Read this once, hit record, follow the
beats. Total target: 5:00–5:30. Re-record if it goes long.

> Voice: confident, technical-peer, not salesy. McKinsey-meets-Anthropic.

---

## Scene 1 · Cold open (0:00 – 0:20)

**On screen:** a terminal, half-screen; the SingleAxis website is open in the other half.

> "I'm Bryan from SingleAxis. In the next five minutes you're going to see
> a real AI agent get instrumented, redact a customer's PII before it hits
> the model, and have a tool call denied by policy — all on my laptop,
> all open source."

`pip install singleaxis-fabric` runs in 5 seconds. *Don't talk through pip output.*

---

## Scene 2 · Wrap an agent (0:20 – 1:20)

**On screen:** `examples/kind-quickstart/agent.py` open in the editor.

> "Here's a refund agent. Four lines of Fabric: I tell it the tenant and
> the agent, open a decision per turn, and wrap the LLM call to capture
> tokens, cost, and latency."

Highlight:

```python
fabric = Fabric(FabricConfig(tenant_id="acme-demo", agent_id="refund-bot"))
with fabric.decision(session_id=...) as d:
    with d.llm_call(system="anthropic", model="claude-haiku-4-5") as call:
        ...
        call.set_usage(input_tokens=..., output_tokens=...)
```

> "That's it. Now every decision is an OpenTelemetry span flowing into
> whatever observability backend you already have — Datadog, Langfuse,
> Phoenix, Honeycomb. We don't replace your stack."

---

## Scene 3 · PII redaction in the loop (1:20 – 2:40)

**On screen:** terminal split — agent on top, collector logs on bottom.

> "Watch the inputs. The user message has an email address. Let me run
> the agent."

`./up.sh --mock` — wait for the decision to complete.

> "In the collector logs you can see: the span recorded
> `guardrail.entities=[EMAIL_ADDRESS]` and the model only saw a
> placeholder. The raw email never landed on a span attribute. It went
> out-of-band to a content store you control, with an HMAC fingerprint
> for telemetry attribution. Sub-millisecond regex pre-filter, real
> Presidio analyzer behind it."

Point at one specific line in the log.

> "This is critical for SOC 2 and GDPR: your telemetry pipeline is no
> longer a PII leak surface."

---

## Scene 4 · Policy deny on a real tool call (2:40 – 3:50)

**On screen:** agent.py open at the `evaluate_policy` block.

> "Refund agents make real money decisions. The user is asking for
> $4,200. My policy says auto-approve anything under $2,000."

Highlight:

```python
verdict = d.evaluate_policy(opa, policy_id="finance.refund.cap",
                             input={"amount": 4200})
```

`./up.sh --mock` — the agent runs, the tool call is suppressed.

> "The agent tried to call `send_refund`. The OPA policy denied it —
> see the span event: `decision=deny`, `reason='amount $4200 exceeds
> $2,000 cap'`, bundle digest, latency. The side effect was suppressed —
> no real money moved, no email sent. And we have a record of every
> piece of that for the audit."

---

## Scene 5 · What the audit gets (3:50 – 4:40)

**On screen:** `docs/auditor-checklist.md` open, scroll through the 11 categories.

> "When the auditor shows up, here's what we can answer for them today,
> from the OSS substrate alone: who the agent was, what the user asked,
> what PII we removed, which model we used, what it cost, what tools it
> tried, who authorized them, which policy denied them, what side effects
> were suppressed, and a full per-decision audit log."

Pause.

> "57 questions on the checklist. 38 of them are answered out of the
> box by OSS. The other 19 — the integrity layer, signed evidence bundles,
> credentialed human review — are what you'd pay for. We're explicit
> about the boundary."

---

## Scene 6 · The Commercial onramp (4:40 – 5:10)

**On screen:** brief glimpse of the agent console (if available) or a static slide.

> "The Commercial plane — Decision Graph, continuous judges, the Expert
> Review queue with credentialed SingleAxis Evaluators on tap, and the
> signed Evidence Bundle — that's how you turn this OSS substrate into
> something a regulator will accept. You install OSS first. You upgrade
> when you need to prove it."

---

## Close (5:10 – 5:30)

**On screen:** the GitHub repo + the singleaxis.ai/install URL.

> "Everything you just saw: `pip install singleaxis-fabric`. Helm chart
> on GHCR. Apache 2.0. We'd love to put it on your cluster — link in the
> description. Thanks for the five minutes."

---

## Production notes

- **Record at 1440p**, scale terminal font ≥ 18pt so it's readable on
  phone replays.
- **Two takes** — one for the technical-buyer (this script), one cut
  down to 2 minutes for execs (Scenes 1, 3, 4, 6 only).
- **Captions are mandatory** — most playback is muted.
- **Re-record any scene that drifts off-script** rather than editing
  around it. The pacing has to feel deliberate.

## What to record after the signed Evidence Bundle ships

A 90-second "Scene 7" addendum:

> "The same decision, sealed. Hash-chained, Ed25519 signed by acme-ops,
> WORM-stored. Here's the verifier CLI confirming the chain is unbroken.
> Hand this to your auditor."

That's the upsell sequence. Don't record it before the bundle is built.
