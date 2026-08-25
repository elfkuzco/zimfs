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

# Execution Governance Capability Map

Use this map when authoring or reviewing an Ota contract that needs more than basic task execution.
Open the focused public reference before copying the example.

| Job | Public reference | Contract or evidence surface | Example |
| --- | --- | --- | --- |
| Restrict agent execution to an effective safe closure | [Safe Agent Execution and Refusal](https://ota.run/docs/reference/safe-agent-execution-and-refusal) | `agent.safe_tasks`, `safe_for_agent`, `agent.refusal_canaries`, `ota run|up --agent` | [`reference/safe-agent-execution`](https://github.com/ota-run/examples/tree/1.6.26-implementation/reference/safe-agent-execution) |
| Project contract-owned verification into CI | [Contract-to-CI Governance](https://ota.run/docs/reference/contract-to-ci-governance) | `ota ci projection`, `ota ci github render|check|sync`, stable merge-check identity | [`ci/github-actions`](https://github.com/ota-run/examples/tree/main/ci/github-actions) |
| Apply compatible runtime controls through a provider | [Sandbox Policy and Runtime Enforcement](https://ota.run/docs/reference/sandbox-policy-and-runtime-enforcement) | selected closure policy, target platform, `--sandbox-target oci_local`, applied-control and cleanup evidence | [`reference/enforced-oci-sandbox`](https://github.com/ota-run/examples/tree/1.6.26-implementation/reference/enforced-oci-sandbox) |
| Publish bounded proof rather than one broad green | [Proof Evidence and Honest Boundaries](https://ota.run/docs/reference/proof-evidence-and-honest-boundaries) | `proof_verdict`, `not_proved[]`, dependency evidence, prerequisite freshness, lifecycle proof | [`reference/runtime-proof-evidence`](https://github.com/ota-run/examples/tree/main/reference/runtime-proof-evidence) |
| Govern immutable replay inputs and generated baselines | [Replay Inputs and Trusted Baselines](https://ota.run/docs/reference/replay-inputs-and-trusted-baselines) | `replay_inputs[]`, `expected_identity`, replay policy, artifact record/promotion/consumption | [`reference/replay-baseline-regeneration`](https://github.com/ota-run/examples/tree/main/reference/replay-baseline-regeneration) |
| Check whether evidence supports a contract claim | [Contract-Claim Assurance](https://ota.run/docs/reference/contract-claim-assurance) | `claim_assurance[]` with `supported`, `contradicted`, or `unknown` | Use selected proof and archived receipt evidence; never author the outcome directly |
| Audit a heavier allowed boundary crossing | [Audited Execution Boundary Crossings](https://ota.run/docs/reference/audited-execution-boundary-crossings) | released crossing classification/records; active V11.7 signed grant preview | [`reference/audited-crossing-authority`](https://github.com/ota-run/examples/tree/1.6.26-implementation/reference/audited-crossing-authority) |
| Explain semantic contract drift against failures | [Semantic Snapshots and Correlation](https://ota.run/docs/reference/semantic-snapshots-and-correlation) | archived contract snapshot, `ota diff`, receipt baseline correlation | [`reference/receipt-workflow-history`](https://github.com/ota-run/examples/tree/main/reference/receipt-workflow-history) |

## Authoring rules

- Do not add a field because the feature name sounds useful. Model only a selected repo behavior
  that Ota can validate, execute, or bound honestly.
- Keep declaration, admission, execution, proof, and authority separate.
- A safe task label must be true for its complete closure.
- A provider capability profile is not enforcement until the provider applies it and Ota records
  bounded applied-control evidence.
- A green proof must carry its `not_proved[]` boundary.
- A baseline record is not trusted until one exact attestation is explicitly promoted.
- Claim assurance is derived evidence posture; never hand-author `supported`.
- A crossing record is never reusable approval authority.
- The bounded V11.7 `--grant` authority slice is complete on the `1.6.26-implementation` branch but
  remains unreleased. Do not teach repository-owned trust keys or signed bundles as a shortcut;
  provider attestation remains optional stronger hardening.
- Raw shell outside an adopted Ota, CI, or sandbox chokepoint remains outside Ota's control.
