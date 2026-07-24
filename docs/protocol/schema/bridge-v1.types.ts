export type BridgeProtocolName = "cordcode-bridge";

export interface BridgeProtocol {
  name: BridgeProtocolName;
  version: 1;
  schemaRevision?: string;
  supportedSchemaRevisions?: string[];
}

export interface BridgeWireError {
  code?: string;
  message?: string;
  retryable?: boolean;
  recoverBy?: string;
  backendId?: string;
}

export interface BridgeClientInfo {
  id?: string;
  app?: string;
  name?: string;
  version: string;
  deviceId?: string;
}

export type BridgeClientCapability =
  | "recovery_v1"
  | "relay_gzip_v1"
  | "relay_chunks_v1"
  | "session_sync_v2";

export interface RelayChunkMetadata {
  groupId: string;
  index: number;
  count: number;
}

export interface BridgeSessionCut {
  eventId: string;
  seq: number;
}

/** backendId -> sessionId -> acknowledged cut. */
export type BridgeSessionCutMap = Record<string, Record<string, BridgeSessionCut>>;

export type BridgeRecoveryPlan =
  | {
      recoveryId: string;
      mode: "replay";
      replayThroughBySession: BridgeSessionCutMap;
    }
  | {
      recoveryId: string;
      mode: "snapshot_required" | "full_resync";
      affectedSessions: Array<{ backendId: string; sessionId: string }>;
      cutBySession: BridgeSessionCutMap;
    };

export interface BridgeHello {
  type: "hello";
  client: {
    app: string;
    version: string;
    deviceId: string;
  };
  protocol: BridgeProtocol;
  capabilities?: BridgeClientCapability[];
  lastBridgeEpoch?: string;
  /** Compatibility hint only; recovery decisions use lastSeenBySession. */
  lastEventId?: string;
  lastSeenBySession?: BridgeSessionCutMap;
}

export interface BridgeRegister {
  type: "register";
  client: {
    id: string;
    name: string;
    version: string;
  };
  protocol: Pick<BridgeProtocol, "name" | "version">;
}

export interface BridgeSecurityProfile {
  level: string;
  scheme?: string;
  hostCategory?: string;
  isTailscaleCGNAT?: boolean;
  isPublicWS?: boolean;
}

export interface BridgeBackendInfo {
  id: string;
  kind: "claude_code" | "opencode" | "codex" | string;
  displayName?: string;
  capabilities?: string[];
  descriptor?: Record<string, string>;
  permissionMode?: { mode?: string };
  /**
   * Backend availability status + reason, emitted by go-bridge AgentProviderDescriptor
   * (agent_descriptor.go:31). status is "available"/"unavailable"/...; reason explains why a
   * backend is not available (e.g. not installed / not running). Consumers surface unavailable
   * backends as disabled-with-reason rather than hiding them.
   */
  status?: string;
  reason?: string;
  /** Live-event transport mode the backend advertises (AgentProviderDescriptor.LiveEvents). */
  liveEvents?: string;
  /** Whether the client must poll to observe externally-initiated turns (AgentProviderDescriptor). */
  requiresPollingForExternalTurns?: boolean;
}

export interface BridgeHelloAck {
  type: "hello_ack";
  ok: boolean;
  bridge?: {
    bridgeId: string;
    displayName: string;
    runtimeVersion: string;
    currentURLs: {
      local: string;
      // Secondary LAN direct candidates (ws://<lan-ip>:<port>/bridge); local is the primary.
      // Does not carry Tailscale candidates (those need a separate TLS pin).
      locals?: string[];
      remote?: string | null;
      remotes?: string[];
    };
    protocol: BridgeProtocol;
    security?: BridgeSecurityProfile;
  };
  capabilities?: Record<string, boolean>;
  bridgeEpoch?: string;
  recovery?: BridgeRecoveryPlan;
  backends?: BridgeBackendInfo[];
  bridgeStatus?: string;
  runningSessions?: Array<{
    backendId: string;
    workspaceId?: string;
    sessionId: string;
    status: "running" | string;
  }>;
  error?: BridgeWireError;
}

export interface BridgeRegisterAck {
  type: "register_ack";
  ok: boolean;
  protocol?: BridgeProtocol;
  serverCapabilities?: string[];
  bridgeEpoch?: string;
  backends?: BridgeBackendInfo[];
  error?: BridgeWireError;
}

export interface BridgeRecoveryBarrier {
  type: "recovery_barrier";
  recoveryId: string;
  replayThroughBySession: BridgeSessionCutMap;
}

export interface BridgeRecoveryApplied {
  type: "recovery_applied";
  recoveryId: string;
  appliedThroughBySession: BridgeSessionCutMap;
}

export interface BridgeRecoveryComplete {
  type: "recovery_complete";
  recoveryId: string;
}

export interface BridgeRecoverySnapshotMetadata {
  recoveryId: string;
  hwm: BridgeSessionCut;
}

export type BridgeRPCMethod =
  | "hello"
  | "list_providers"
  | "set_provider"
  | "list_models"
  | "list_agents"
  | "list_permission_modes"
  | "set_permission_mode"
  | "create_session"
  | "send_message"
  | "abort_generation"
  | "get_session"
  | "get_session_messages"
  | "get_session_projection"
  | "delete_session"
  | "resume_session"
  | "switch_model"
  | "resolve_permission"
  | "list_sessions"
  | "list_projects"
  | "fetch_todos"
  | "get_usage"
  | "run_diagnostics"
  | "list_memory_files"
  | "read_memory_file"
  | "fetch_content_chunk"
  | "read_file"
  | "rename_session"
  | "share_session"
  | "archive_session"
  | "set_session_pinned"
  | "list_pinned_sessions"
  | "compress_context"
  | "check_pending_notifications"
  | "question_reply"
  | "question_reject"
  | "get_delivery_prekey_status"
  | "upload_delivery_prekeys"
  | "get_delivery_chain_head"
  // Backfilled (M6): this method is registered in go-bridge handlers.go:699 but was missing from
  // the canonical enum. Now present in all three consumers (iOS/MacBridge/remote-web).
  // Capability string: "workspace_diff".
  | "get_workspace_diff"
  // R19: Backfilled — these 5 git/directory methods are already registered in the go-bridge
  // runtime dispatcher (handlers.go:743-751) and listed in the canonical Markdown
  // (bridge-v1.md:104-108), but were missing from this typed schema (a canonical internal
  // inconsistency). They are file-scoped per capability_policy.go (no capability *string*;
  // they have no "foo_bar" capability gate — see R20). Now mirrored across iOS/MacBridge/
  // remote-web so the typed union matches the runtime + Markdown source of truth.
  // Ordering matches bridge-v1.md.
  | "list_directory"
  | "get_git_context"
  | "checkout_git_branch"
  | "create_git_branch"
  | "create_git_worktree";

export interface BridgeRequest<TParams = Record<string, unknown>> {
  type: "request";
  requestId: string;
  backendId: string;
  method: BridgeRPCMethod;
  params?: TParams;
}

export interface BridgeResult<TData = unknown> {
  type?: "result";
  requestId?: string;
  backendId?: string;
  ok?: boolean;
  data?: TData;
  error?: BridgeWireError;
}

export type ToolMatches =
  | { kind: "count"; count: number }
  | { kind: "paths"; paths: string[] }
  | { kind: "detailed"; items: Array<{ path: string; line?: number; preview?: string }> };

export interface BridgeToolEventData {
  itemId?: string;
  toolName?: string;
  toolInput?: unknown;
  toolInputRaw?: Record<string, unknown>;
  toolResult?: unknown;
  toolStatus?: string;
  toolExitCode?: number;
  matches?: ToolMatches;
  streamId?: string;
  parentStreamId?: string;
}

export type BridgeEventName =
  | "text_delta"
  | "message_updated"
  | "reasoning_delta"
  | "tool_started"
  | "tool_finished"
  | "todos_updated"
  | "turn_started"
  | "turn_completed"
  | "error"
  | "permission_request"
  | "context_compressing"
  | "context_compressed"
  | "context_usage_updated"
  | "question_asked"
  | "question_resolved"
  // Session Projection Stream (session_sync_v2 capability). Mac reduces EventPublisher
  // output into one authoritative SessionProjection; clients apply patches/snapshots only
  // and never dual-source merge. See bridge-v1.md「Session Projection Stream」.
  // Phase 1 = Codex rollout path only; driver/local-send/web are Phase 3+.
  | "projection_patch"
  | "projection_snapshot"
  | "sync_invalidate";

export interface BridgeEvent<TData = unknown> {
  type: "event";
  eventId?: string;
  seq?: number;
  bridgeEpoch?: string;
  backendId?: string;
  sessionId?: string;
  event?: BridgeEventName;
  data?: TData;
  replayable?: boolean;
  timestamp?: number;
}

export interface BridgePing {
  type: "ping";
  ts: number;
}

export interface BridgePong {
  type?: "pong";
  ts?: number;
}

// ── Session list + message-history pagination ───────────────────────────────
// list_sessions limit/cursor fields are additive and do not require the
// message-history capability. The "session_pagination" capability gates only
// get_session_messages paginate/beforeCursor history paging.

/** list_sessions request params (limit/cursor are additive; cursor is opaque and scope-bound). */
export interface BridgeListSessionsParams {
  directory?: string;
  rootsOnly?: boolean;
  limit?: number;
  cursor?: string;
}

/**
 * Session info returned by list_sessions/get_session.
 *
 * This is the verified union of the two wire producers:
 *   - sessionsToWire (go-bridge/handlers.go) for Claude/Codex, and
 *   - mapSession (go-bridge/opencode-proxy.go) for OpenCode.
 * Fields marked backend-specific are emitted only by the noted backend; all others
 * are shared. Optional fields are omitted on the wire when unset.
 */
export interface BridgeSessionInfo {
  id: string;
  title: string;
  /** Shared (sessionsToWire emits always; OpenCode emits when upstream provides). */
  messageCount?: number;
  /** Claude/Codex only (RFC3339 string from sessionsToWire). OpenCode uses createdAtMillis/updatedAtMillis. */
  modifiedAt?: string;
  /** Shared epoch-ms. */
  updatedAtMillis: number;
  /** Shared epoch-ms. NOTE: sessionsToWire currently sets createdAtMillis = ModifiedAt (not a real creation time). */
  createdAtMillis: number;
  /** Shared epoch-ms; present only when the session is archived. */
  archivedAtMillis?: number;
  /**
   * Shared epoch-ms; present only when the session is pinned (置顶).
   * Backed by the session_pin capability + set_session_pinned RPC. Represents when the
   * user pinned the session, NOT the session's updatedAt. Pin/unpin MUST NOT alter
   * updatedAtMillis. See bridge-v1.md「Session Pinning」.
   */
  pinnedAtMillis?: number;
  /** Shared. OpenCode emits always; Claude/Codex emit when non-empty. */
  directory?: string;
  /** Claude/Codex only. */
  modelId?: string;
  /** Shared. */
  effectiveModelId?: string;
  /** Claude/Codex only. */
  providerId?: string;
  /** Shared. */
  effectiveProviderId?: string;
  /** Claude/Codex only. */
  reasoningEffort?: string;
  /** OpenCode only (literal "opencode"). */
  backendId?: string;
  /** OpenCode only. */
  projectId?: string;
  /** OpenCode only. */
  parentId?: string;
  /** OpenCode only ("resumable" | "archived"). */
  availability?: string;
  /** OpenCode only (literal false). */
  isReadOnlyHistory?: boolean;
  /** OpenCode only (literal "idle", later overwritten by runtime-state enrichment). */
  runtimeState?: string;
}

export interface BridgeListSessionsResult {
  sessions: BridgeSessionInfo[];
  nextCursor?: string; // present only when hasMore is true
  hasMore: boolean;
}

/** get_session_messages request params (paginate/beforeCursor are additive). */
export interface BridgeGetSessionMessagesParams {
  sessionId: string;
  directory?: string;
  limit?: number;
  paginate?: boolean;
  beforeCursor?: string;
  /** P3 etag: client's last-known messages revision. When it matches the server's
   *  current revision, the server returns {unchanged:true} with no messages body,
   *  cutting the recurring ~685KB transfer to a few bytes (major idle/cellular heat
   *  win). Additive: old servers ignore it (always return full). */
  ifNoneMatchRevision?: string;
}

export interface BridgeGetSessionMessagesResult {
  messages: unknown[];
  oldestCursor?: string; // send as beforeCursor for the next (older) page
  newestCursor?: string; // informational, for client merge/dedup
  hasMore: boolean;
  contextUsage?: unknown;
  /** P3 etag: revision of the messages payload (sha256[:16] of marshaled messages).
   *  Present on full responses; send back as ifNoneMatchRevision next time. */
  revision?: string;
  /** P3 etag: true when ifNoneMatchRevision matched → messages omitted. Client MUST
   *  keep its cached messages and skip merge/signature work. */
  unchanged?: boolean;
}

/** Backend capability string for get_session_messages history paging, not list_sessions paging. */
export type BridgeSessionPaginationCapability = "session_pagination";

// ── Session Projection Stream (SPS) · session_sync_v2 ────────────────────────
// Single-source multi-device sync (design docs/2026-07-24-single-source-multidevice-sync-design.md).
// MacBridge reduces its EventPublisher output into ONE authoritative SessionProjection per
// (backendId, sessionId); clients only apply patches/snapshots and MUST NOT dual-source merge
// content. syncRev ≡ EventPublisher.perSessionSeq for that session (monotonic under the publisher
// lock). Phase 1 consumes the Codex file-relay/rollout path only (itemId/turnId already carried,
// bypasses DeltaBatcher); driver/local-send/web are Phase 3+.

/** One part of a message projection. `type` mirrors the existing Mapping Notes part vocabulary. */
export type BridgeProjectionPart =
  | { type: "text"; text: string }
  | { type: "reasoning"; text: string }
  | {
      type: "tool";
      itemId?: string; // rollout call_id (authoritative tool identity)
      toolName?: string;
      toolInput?: unknown;
      toolResult?: unknown;
      toolStatus?: string;
      matches?: ToolMatches;
    }
  | { type: "file"; path?: string; kind?: string; diff?: string; movePath?: string };

export interface BridgeMessageProjection {
  /** Authoritative source id: rollout response_item.id (user) / call_id (tool) / lifecycle turn_id (assistant text). */
  id: string;
  /** Optional echo of a client optimistic id (Phase 3 local-send correlation; absent in Phase 1–2). */
  clientId?: string;
  role: "user" | "assistant";
  parts: BridgeProjectionPart[];
}

export type BridgeTurnStatus = "pending" | "running" | "completed" | "aborted" | "error";

export interface BridgeTurnProjection {
  /** Codex rollout: stable lifecycle turn_id (event_msg.turn_id), carried by turn_started/turn_completed. */
  turnId: string;
  status: BridgeTurnStatus;
  startedAt?: number; // epoch-ms
  /** Integrates the existing turnCompletedAt evidence into a turn-level authoritative state. */
  completedAt?: number; // epoch-ms
  user?: BridgeMessageProjection;
  assistant?: BridgeMessageProjection;
}

export type BridgeExecutionPhase = "idle" | "running" | "requires_action";

export interface BridgeExecutionView {
  phase: BridgeExecutionPhase;
  activeTurnId?: string;
}

export interface BridgeSessionProjection {
  sessionId: string;
  /** Monotonic; ≡ EventPublisher.perSessionSeq for (backendId, sessionId). Same value push and pull read. */
  syncRev: number;
  bridgeEpoch?: string;
  updatedAt?: number; // epoch-ms
  execution: BridgeExecutionView;
  turns: BridgeTurnProjection[];
}

/** Incremental part operation (main streaming path). Applies to a specific (turnId, messageId). */
export type BridgePartOp =
  | { turnId: string; messageId: string; op: "append_text"; text: string }
  | { turnId: string; messageId: string; op: "set_thinking"; text: string }
  | { turnId: string; messageId: string; op: "upsert_tool"; part: Extract<BridgeProjectionPart, { type: "tool" }> }
  | { turnId: string; messageId: string; op: "replace_parts"; parts: BridgeProjectionPart[] };

/** Push frame `projection_patch`: baseRev→syncRev incremental delta (coalesced 50–100ms server-side). */
export interface BridgeProjectionPatch {
  baseRev: number;
  syncRev: number;
  execution?: BridgeExecutionView;
  /** Whole-turn upsert: new turn appearance, status change, or authoritative correction. */
  upsertTurns?: BridgeTurnProjection[];
  /** Main path: incremental part ops. Clients append/replace the named part; never merge with another source. */
  partOps?: BridgePartOp[];
  /** Phase 3: authoritative ids that invalidate local optimistic ids (absent in Phase 1–2). */
  replacesClientIds?: string[];
}

/** Push frame `projection_snapshot`: full projection at syncRev (epoch mismatch / recovery). */
export interface BridgeProjectionSnapshot {
  syncRev: number;
  projection: BridgeSessionProjection;
}

/** Push frame `sync_invalidate`: force the client to call get_session_projection. */
export interface BridgeSyncInvalidate {
  reason: "epoch_mismatch" | "gap" | "reducer_reset";
  bridgeEpoch?: string;
}

/** get_session_projection request params (additive; sits beside get_session_messages). */
export interface BridgeGetSessionProjectionParams {
  sessionId: string;
  directory?: string;
  /** When set, the server MAY return a delta {patches, headRev} instead of the full projection. */
  sinceRev?: number;
  limitTurns?: number;
}

/** get_session_projection result: full projection (no sinceRev / server chose snapshot) OR delta patches. */
export type BridgeGetSessionProjectionResult =
  | { projection: BridgeSessionProjection }
  | { patches: BridgeProjectionPatch[]; headRev: number };
