# Controller fixture blocker

Status: **EVIDENCE-ONLY / real fixture missing**

All non-mutating preparation for the frozen target passed. The repository now contains an exact static call-site contract, a live-fixture/redaction contract, a fail-closed validator and a non-mutating preflight that confirms:

- the installed Desktop, embedded Codex and ASAR hashes still match;
- the private device-key addon hash and four exported operations match;
- the embedded Codex ChatGPT login status is available without exposing a token;
- both official step-up callback ports are currently available;
- no key, controller identity, network mutation or pairing change has occurred.

The current exec-plan item cannot be proven until the owner authorizes a bounded real-account experiment. That experiment will require in-memory use of the official embedded Codex auth result, an external-browser PKCE step-up, creation of one independently named OS-protected key, official controller enrollment/WSS traffic, and manual observation of the already-connected ChatGPT iOS controller. None of those observations may be replaced by a fabricated fixture or old log.

The strict validator currently fails at the intended first boundary:

```text
Error: real redacted live fixture is missing
```

Phase 0 Gate remains not passed. No product backend or iOS product wiring is authorized by this preparation.
