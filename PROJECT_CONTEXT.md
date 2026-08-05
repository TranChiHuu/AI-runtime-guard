# PROJECT_CONTEXT.md

# AI Runtime Guard

> Local-First AI Runtime Safety Platform

---

# Product Mission

AI Runtime Guard is a **Local-First Runtime Safety Platform** that continuously observes AI coding agents, understands their behavior, predicts potential risks, and intervenes before irreversible actions happen.

This product is **NOT** a traditional policy engine.

It is **NOT** an observability platform.

It is **NOT** a logging system.

It is a runtime safety layer that sits between AI agents and the developer's machine.

---

# Vision

We believe AI Coding Agents will become the primary way software is built.

As AI agents gain access to:

- Filesystem
- Shell
- Secrets
- Git
- MCP Servers
- External Networks

Developers lose visibility over what AI is actually doing.

Current security tools mainly:

- log events
- audit events
- trace events
- block individual tool calls

None of them truly understand an **AI Session**.

Our vision is to become the runtime safety system for AI agents.

Think:

Cloudflare

↓

protects web traffic

CrowdStrike

↓

protects operating systems

AI Runtime Guard

↓

protects AI agent runtime.

---

# Product Philosophy

## Local First

Security decisions must happen locally.

Protection must never depend on cloud availability.

Even when:

- Internet is offline
- Dashboard is unavailable
- Cloud sync fails

The runtime must continue protecting developers.

Cloud is optional.

Protection is mandatory.

---

## Runtime Before Analytics

Protection always has higher priority than visualization.

Order of importance:

1. Protection
2. Visibility
3. Insights
4. Governance

Never delay runtime enforcement to build dashboards.

---

## Session Instead of Events

A single event is rarely dangerous.

The entire session tells the real story.

Example:

Read README

↓

Read .env

↓

Read ~/.ssh

↓

Call curl

↓

Upload data

Each action alone may be harmless.

Together they indicate a high-risk AI session.

Everything should be modeled around **Session State** rather than isolated events.

---

# Product Core

The heart of the product is **Runtime Brain**.

Everything else exists to support it.

Runtime Brain contains:

- Session Engine
- Risk Engine
- Policy Engine
- Decision Engine

Adapters are NOT the product.

Dashboards are NOT the product.

CLI is NOT the product.

Runtime Brain is the product.

---

# Runtime Brain

Runtime Brain continuously performs five steps.

Observe

↓

Understand

↓

Predict

↓

Intervene

↓

Learn

---

## Observe

Collect runtime signals.

Signals include:

- Prompt
- Tool Calls
- Filesystem
- Shell
- Git
- MCP
- Network
- Context
- Tool Results

Every signal contributes to session understanding.

---

## Understand

Maintain a real-time session model.

Example:

Session

Secret Access

YES

Filesystem

YES

Outbound Network

YES

Git Push

NO

Untrusted Context

YES

Runtime Brain should understand the current state instead of reacting to individual events.

---

## Predict

Risk is dynamic.

Risk should never be represented as:

IF tool == curl THEN deny.

Instead:

Risk Score

87%

Reason

Secret Access

+

Outbound Request

+

Unknown Destination

The engine predicts unsafe behavior before irreversible actions occur.

---

## Intervene

Runtime Brain can respond in multiple ways.

Notify

↓

Ask

↓

Pause

↓

Block

Blocking is the last option.

Pause is often a better developer experience.

---

## Learn

Every user decision improves future behavior.

Examples:

Always Allow

Always Deny

Approve Once

Dismiss Warning

Runtime Brain should become smarter over time.

---

# Runtime Safety Model

Instead of thinking in policies only:

Observe

↓

Risk

↓

Decision

↓

Action

Risk is continuously recalculated during the entire AI session.

---

# Safety State Machine

Every session exists in one state.

SAFE

↓

WATCHING

↓

WARNING

↓

CRITICAL

↓

INTERVENTION

The user should always understand the current safety state.

---

# Product Architecture

Agent

↓

Adapter (Sensor)

↓

Normalization

↓

Runtime Brain

↓

Decision

↓

Action

↓

Audit

Every AI platform should have its own lightweight adapter.

Examples:

- Claude Code
- Codex CLI
- Gemini CLI
- Cursor
- MCP

Adapters should only normalize events.

Business logic belongs inside Runtime Brain.

---

# Primary Interfaces

The product is Local First.

Primary interface:

CLI

Examples:

guard up

guard status

guard doctor

guard report

Secondary interface:

Local Dashboard

localhost

Enterprise interface:

Cloud Dashboard

The CLI is the entry point.

Dashboard is only for visualization.

---

# Product Principles

## Explain Everything

Every warning must answer:

What happened?

Why?

What evidence?

What risk?

What should the user do?

No black-box decisions.

---

## Developer Native

Developers already live inside:

- Terminal
- Claude Code
- Codex CLI
- Gemini CLI
- Cursor

The product should integrate naturally into their workflow.

Never force unnecessary UI.

---

## Safety Without Friction

Developers should feel protected, not interrupted.

Intervention should happen only when confidence is high.

Avoid unnecessary confirmations.

---

## Local Privacy

Runtime data belongs to the developer.

Cloud synchronization should always be optional.

Never require cloud connectivity for protection.

---

# Long-Term Product Strategy

The first product is Runtime Guard.

The long-term platform may include:

- Runtime Guard
- Runtime Replay
- AI Security Report
- MCP Security Scanner
- Prompt Risk Scanner
- Secret Exposure Monitor
- Team Governance
- Policy Marketplace

All products share one Runtime Brain.

---

# Competitive Advantage

Our moat is NOT:

- Hooks
- Plugins
- Dashboards
- Policy Rules

Anyone can build those.

Our moat is:

- Runtime Brain
- Session Intelligence
- Risk Modeling
- Safety Heuristics
- Explainable Runtime Decisions

These capabilities improve as more runtime sessions are analyzed.

---

# Success Metrics

Do NOT optimize for:

- Number of Rules
- Number of Dashboards
- Number of Features

Optimize for:

- Protected AI Actions
- Dangerous Sessions Prevented
- Secret Exfiltration Prevented
- Active Protected Developers
- Runtime Decision Accuracy

---

# Engineering Principles

When building features, always ask:

Does this improve Runtime Brain?

Does this improve developer trust?

Does this reduce response time?

Does this improve explainability?

If not,

do not build it.

---

# Product Motto

Observe continuously.

Understand context.

Predict early.

Intervene wisely.

Protect developers.

Always local.