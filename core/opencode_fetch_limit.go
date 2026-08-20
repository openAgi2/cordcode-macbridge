package core

// OpenCodeSessionFetchLimit is the single frozen upstream fetch budget shared
// by every OpenCode-family list path (go-bridge ocProxy pagination and the
// opencode-web adapter's upstream GET /session). The OpenCode server is
// array-only on /session (no cursor in the stable generation), so the only way
// to know the real total is one bounded large page; 100 matches the
// server-side default upper bound. Per-client pages are sliced in memory.
//
// This constant lives in core so the adapter package and the bridge share ONE
// number (C2: directory + roots=true + limit=100 on every upstream request;
// inventing a second fetch number is forbidden).
const OpenCodeSessionFetchLimit = 100
