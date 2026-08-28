# Phase 0 fixture and evidence policy

Only redacted, versioned artifacts are allowed here.

Permitted:

- target App/Codex/upstream metadata and hashes;
- endpoint names, header names and schema-only fixtures;
- pseudonymous environment/client/stream/thread/turn identifiers that cannot be correlated back to the account;
- timestamps normalized to relative offsets when wall-clock time is unnecessary;
- redacted request/response/envelope fixtures with all secrets and user content removed;
- command summaries, validator logs and Gate adjudication reports.

Forbidden:

- access/refresh/controller/session/step-up tokens;
- pairing or MFA codes;
- device private keys or raw signatures that enable replay;
- raw account/workspace/email identity;
- unredacted user prompts, assistant output, tool payloads or file contents;
- ChatGPT/Codex credential files, Keychain exports, raw network captures or crash dumps containing secrets.

Every live fixture set must include a metadata record naming the target versions, capture purpose, redaction procedure, source classification and secret-scan result. Unknown or partially redacted data stays outside the repository and cannot be cited as committed evidence.
