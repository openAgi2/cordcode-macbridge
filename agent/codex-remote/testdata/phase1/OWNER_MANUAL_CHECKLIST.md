# Phase 1 owner manual checks (batched)

Automated evidence already covers envelope Transport, JSON-RPC, thread/list,
thread/resume, turn/start, text delta, turn/completed, and interrupt against a
fake host. Frozen `agent/codex-web` and `agent/codex` are unchanged.

Do these on the feature branch after a Release install, in one sitting. Do not
paste pairing codes into chat.

1. ChatGPT Desktop is the frozen target (`26.825.32147` / Codex `0.150.0-alpha.12.2`).
2. Enable `codex-remote` only via an explicit runtime `-drivers` override if asked;
   default Mac drivers must still omit it until this list is checked off.
3. Complete step-up + Computer-tab pairing on the localhost form.
4. Confirm diagnostics show controller protocol 3 and the selected Desktop
   environment, not a fabricated cursor.
5. `thread/list` shows Desktop threads; resume the currently open thread.
6. Send a short Desktop message; iOS is **not** required. Mac-side logs/events
   must show `turn/started`, `item/agentMessage/delta`, `turn/completed`.
7. Optional: interrupt an in-flight turn and confirm `turn/completed` interrupted.
8. Disconnect/reconnect: product must fail closed without inventing
   `x-codex-subscribe-cursor`.
9. Revoke only the CordCode controller; ChatGPT iOS pairing must remain.

Known gaps (must stay fail-closed / unadvertised until proven):

- controller reconnect cursor
- official iOS controller coexistence / HTTP 409
- unique probe-marked thread identity
