# Phase 2 Remote app-server fixtures

These fixtures are **schema-derived replay fixtures**, not claims of a new
live Desktop capture. The latest redacted live capture (`phase0/live/
attempt-008...`) proves the Remote envelope delivered `thread/resume` and the
turn/item notification methods, but it intentionally removed payload values.
The payload fields below are therefore frozen from the official app-server
protocol source and exercised only as local decoder/reducer tests until an
owner-authorized capture preserves the same fields.

The two files are kept together to make the boundary explicit:

* `thread-read-app-server.json` is the ordinary `thread/read` response after
  the Remote envelope has been removed.
* `thread-read-remote-envelope.json` wraps the exact same JSON-RPC response in
  the controller's `server_message` shape with fixture-local routing ids.

No fixture contains credentials, account identifiers, or real user content.
The decoder's positive behavior is still gated by the official source and
target-version evidence; unknown item kinds remain skipped and unadvertised.
