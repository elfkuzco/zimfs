<!--
                █████
               ░░███
       ██████  ███████    ██████
      ███░░███░░░███░    ░░░░░███
     ░███ ░███  ░███      ███████
     ░███ ░███  ░███ ███ ███░░███
     ░░██████   ░░█████ ░░████████
      ░░░░░░     ░░░░░   ░░░░░░░░

   Copyright (C) 2026 — 2026, Ota. All Rights Reserved.

   DO NOT ALTER OR REMOVE COPYRIGHT NOTICES OR THIS FILE HEADER.

   Licensed under the Apache License, Version 2.0. See LICENSE for the full license text.
   You may not use this file except in compliance with that License.
   Unless required by applicable law or agreed to in writing, software distributed under the
   License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
   either express or implied. See the License for the specific language governing permissions
   and limitations under the License.

   If you need additional information or have any questions, please email: os@ota.run
-->

# Agent And Governance Checklist

Use this checklist when deciding whether a contract is trustworthy for humans, CI, and agents.

- Does `agent.entrypoint` lead into the canonical verification or readiness path?
- Is `agent.default_task` finite, bounded, and suitable as the normal post-change verification task?
- Are `agent.safe_tasks` honest about network and dependency hydration behavior rather than marking
  long-running or broad-mutation tasks safe?
- Is each claimed-safe task safe across its full selected dependency and aggregate closure, not
  only at the requested task body?
- When a repo needs to prove the runner still refuses a disallowed lane, does it declare
  `agent.refusal_canaries` and run each control through `ota run --agent --expect-refusal` or
  `ota up --agent --workflow <name> --expect-refusal`? A shell imitation does not test Ota's
  enforcement boundary.
- Does `verify_after_changes` point to the real bounded verification surface?
- Do `writable_paths` and `protected_paths` match the effects of safe tasks, generated files, and
  dependency hydration?
- If `AGENTS.md` or `CLAUDE.md` exists, is it the right kind of source for the job:
  Ota-generated guidance should stay self-origin and excluded from detect evidence, prose-only
  docs should not be over-interpreted, and only structured external boundary lists plus narrow
  command surfaces such as labeled command bullets or exact `| Task | Command |` tables inside
  explicit command sections should be treated as admissible detect truth?
- Are task `effects` explicit when the task writes, touches external state, or uses the network?
- Are blocker `checks` present when the repo has known preconditions that should fail early in
  `ota doctor`?
- If selected immutable replay inputs must not drift, do they declare `expected_identity` and keep
  the pin maintainer-reviewed rather than allowing Ota or an agent to rewrite it?
- If replay-input identity policy governs selected tasks or workflows, do task rules own their
  reachable closures, do rules accumulate deterministically with `deny > review > allow`, and do
  `review`, unavailable policy, and hard-pin failures all refuse before execution or mutation?
- Are policy findings and admission-produced receipts derived from one command-scoped replay-input
  observation set rather than rereading mutable files or reconstructing policy after execution?
- If `governance.crossing_authority` is present, does the repository contain only the authority
  identity while keys, bundles, sequence state, and trust paths remain independently managed
  system state? Repository-controlled trust material is self-issued authority.
- Does every heavier non-agent grant bind the exact semantic closure and remain subject to
  freshness, revocation, and rollback checks? A grant must never widen `--agent` safety or be
  accepted for a routine lane.
- Does every real granted crossing carry a terminal runner-owned transaction created before
  selected-lane mutation, while refusal and dry-run remain admission-only and create no
  transaction or crossing record?
- Does refused admission preserve typed authority-source, authority/grant selection, stable reason,
  and `execution_started: false` evidence without minting a crossing record?
- Is the local transaction posture described honestly as
  `runner_local_content_addressed`, without implying independent authentication against same-user
  writes to `.ota/state`?
- Does the first signed-file carrier avoid free-form task inputs and stay described as bounded
  offline authority with a bounded local transaction carrier rather than human identity proof,
  online revocation, or independently authenticated one-use work-unit authority?
- If the contract consumes a promoted generated baseline, does it state the honest authority and
  execution boundary: explicit `replay_baseline` promotion, SCM review as an external trust
  assumption rather than Ota-verified approval, and `read_only` only where an ephemeral container
  can enforce it?
- Do runtime-proof workflows prove surfaced readiness rather than only process startup?
- If a workflow declares `proof.lifecycle`, does it use only manager-owned services with positive
  inactive-state cleanup authority, preserve pre-existing/unknown-state services, and retain
  lifecycle `not_proved[]` boundaries instead of treating command success as application proof?
- Is lifecycle proof intentionally outside CI projection and claim assurance until a separately
  eligible typed or isolated boundary has been pressure-proven?
- Do all contracts, including published examples, declare `metadata.ota.minimum_version`, and do
  workflow pins stay in sync with that lowest honest compatible release?
