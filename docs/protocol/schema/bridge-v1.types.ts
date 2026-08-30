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
  retryAfterMillis?: number;
  attempts?: number;
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
  kind: "claude_code" | "opencode" | "codex" | "opencode-web" | string;
  displayName?: string;
  /**
   * Backend-scoped capabilities. `session_sync_v2` here (not only hello_ack) selects ownership;
   * `structured_user_input_v1` advertises the multi-question/multi-select path (resolve_user_input
   * RPC + user_input part). Both are advertised per-descriptor only when that backend's adapter +
   * responder + Kernel reducer are ready (design §13.1).
   */
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
    /**
     * Control-plane connection policy (Relay-first + opt-in LAN). Optional; absent or
     * preferLocalNetwork=false means Relay is the base. Does NOT enter the timeline/projection.
     * See bridge-v1.md「Connection policy (control-plane)」.
     */
    connectionPolicy?: {
      preferLocalNetwork: boolean; // default false
    };
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
  | "session_turn_items"   // turn_detail_lazy_v1 (unified-bridge-protocol.md §11.7; v1 codex-remote only)
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
  | "read_file_v2"
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
  | "create_git_worktree"
  // §6.1 checkpoint 只读 diff: per-turn / full-thread read-only workspace diff backed by
  // hidden git refs. Capability string: "supports_checkpoint" (derived from the driver
  // implementing core.CheckpointProvider). Scoped to session.read (scope table §6.3).
  // Canonical doc: docs/protocol/bridge-v1.md「RPC: get_turn_diff / get_full_thread_diff」.
  | "get_turn_diff"
  | "get_full_thread_diff";

export interface BridgeRequest<TParams = Record<string, unknown>> {
  type: "request";
  requestId: string;
  backendId: string;
  method: BridgeRPCMethod;
  params?: TParams;
}

// Model item in `list_models` results. `variants` (canonical additive revision,
// E1b sample-verified) carries exactly the live `/provider…models[modelID].variants`
// keys for that model — empty/absent means the model has no variant selector.
export interface BridgeModelItem {
  id: string;
  name: string;
  provider: string;
  providerId: string;
  reasoning: boolean;
  supportedReasoningEfforts?: string[];
  defaultReasoningEffort?: string;
  isDefault?: boolean;
  variants?: string[];
}

// `send_message` model parameter. `variant` (canonical additive revision, E1b
// sample-verified) is a model-specific OpenCode variant key — NOT reasoningEffort.
// An unlisted key fails the RPC with zero POSTs. `agent` (already canonical,
// `send_message.params.agent`) selects the official agent for the same prompt.
export interface BridgeSendMessageModel {
  id: string;
  providerId: string;
  variant?: string;
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

// Official permission payload extras (opencode-web v1.18 permission.asked,
// live-pinned 1.18.18 /global/event): permissionKind = official category key
// (rendered via the official i18n catalog settings.permissions.tool.{kind}.description),
// patterns = official pattern rows. Additive; absent on other backends — clients
// keep the legacy two-button card verbatim. Requests carrying them offer the
// official reject/always/once triple (wire deny/always/allow).
// Canonical doc: bridge-v1.md「Official permission payload」(2026-08-19).
export interface BridgePermissionRequestExtras {
  permissionKind?: string;
  patterns?: string[];
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
  | "permission_resolved"
  | "context_compressing"
  | "context_compressed"
  | "context_usage_updated"
  | "question_asked"
  | "question_resolved"
  | "sessions_changed"
  // §6.1 checkpoint 只读 diff: control-plane push after MacBridge writes a turn's
  // checkpoint git ref. Carries per-file {path,+/-} (capped, NO full patch) so clients
  // can surface the summary without polling. Control-plane only: never mutates the
  // message projection (SSV2 guardrail 8 enumerated exception — not a second writer).
  // Canonical doc: bridge-v1.md「Event: turn_diff_ready」.
  | "turn_diff_ready"
  // Session Projection Stream (session_sync_v2 capability). Mac reduces EventPublisher
  // output into one authoritative SessionProjection; clients apply patches/snapshots only
  // and never dual-source merge. See bridge-v1.md「Session Projection Stream」.
  // Phase 1 = Codex rollout path only; driver/local-send/web are Phase 3+.
  | "projection_patch"
  | "projection_snapshot"
  // Transient provider-retry notice (opencode-web 1.18 session.status{type:"retry"}):
  // {attempt, message, next?}. Turn stays alive — control-plane only, never settles
  // turn state, not mailbox-durable. Canonical doc: bridge-v1.md (2026-08-19).
  | "session_retry_status"
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
  /**
   * Official dsh-web agent preset id (`standard` / `code` / `minimal` / `cordis`).
   * Present only when the backend reports one (DeepSeek Harness session.list/get).
   */
  agentPreset?: string;
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
  | { type: "text"; text: string; presentation?: "progress" | "final" }
  | { type: "reasoning"; text: string }
  | {
      type: "tool";
      itemId?: string; // rollout call_id (authoritative tool identity)
      toolName?: string;
      toolInput?: unknown;
      toolResult?: unknown;
      toolStatus?: string;
      matches?: ToolMatches;
      /**
       * Optional display title for the tool step. Path-bearing for file tools
       * (e.g. Claude Edit/Write `file_path`, Codex patch target). Additive;
       * absent on older producers — clients fall back to toolInput / toolName.
       */
      title?: string;
      /**
       * Optional structured file mutations for this tool step (Codex Patch /
       * apply_patch). Shape matches UnifiedFileChange (path/kind/diff/movePath).
       * Additive; absent when the producer only has free-form toolResult text.
       */
      fileChanges?: Array<{
        path: string;
        kind?: string;
        movePath?: string;
        diff?: string;
      }>;
      /**
       * Optional: this pending tool must be approved before the turn continues
       * (dsh-web approval/requested → permission_request). Additive; absent/false
       * on older producers. Clients map to the existing permission card.
       */
      requiresPermissionConfirmation?: boolean;
      /**
       * Official permission payload (opencode-web v1.18 permission.asked, live-pinned):
       * category key + pattern rows carried on the projected permission part so SSV2
       * clients render the official card (需要权限 + category line + patterns +
       * reject/always/once). Mirrors the wire permission_request extras; additive.
       */
      permissionKind?: string;
      permissionPatterns?: string[];
      /** Exact client actions supported by this pending request. */
      permissionActions?: Array<"approve" | "approveAlways" | "reject" | "rejectAlways">;
    }
  | { type: "file"; path?: string; kind?: string; diff?: string; movePath?: string }
  | {
      // B4 child-stream (sync-only): a Claude Agent/Task tool nested subagent group. Built
      // entirely by the MacBridge projection kernel as the single source of truth; clients map
      // this read-only (no client-side tree building). Join keys mirror the real sidechain
      // .meta.json schema (sample-verified): depth-1 anchors to the mainstream turn via
      // spawnToolUseId ↔ mainstream Agent tool_use id; depth≥2 nests via parentAgentId ↔
      // parent agentId. subagentBlocks is recursive — the subagent's own content (text/reasoning/
      // tool) plus any nested depth+1 subagent parts.
      type: "subagent";
      agentId: string;
      parentAgentId?: string;
      spawnToolUseId?: string;
      spawnDepth?: number;
      subagentType?: string; // async | sync (from .meta.json agentType)
      subagentStatus?: string; // running | completed | failed (sample only verified completed)
      subagentBlocks?: BridgeProjectionPart[];
      subagentError?: string;
      subagentDiagnostic?: string; // orphan_parent | cycle | max_depth
    }
  | {
      // Structured user input v2 (design §6/§10). One part per interactionId, upserted in place
      // by the MacBridge Projection Kernel (the single writer) — never a second "answered" card.
      // Clients map this read-only into a dedicated block; status is owned by the kernel, answer
      // text is never stored in the projection (esp. for isSecret). See bridge-v1.md
      // 「Part vocabulary: user_input」 and 「Structured user input v2」.
      type: "user_input";
      interactionId: string; // stable derived id ("ui_"+sha256…); the upsert key
      status: "pending" | "answered" | "rejected" | "auto_resolved" | "unavailable" | "failed";
      questions?: BridgeUserInputQuestion[]; // present on requested; absent on resolved
      canRespond: boolean; // false for failed/unavailable (no clickable UI)
      canReject: boolean; // Claude true (real deny control_response), Codex false
      expiresAt?: number; // epoch-ms display hint; clients MUST NOT run a local timer to flip status
      resolvedAt?: number; // epoch-ms when the interaction reached a terminal status
      resolutionSource?: "ios" | "mac" | "other_client" | "backend";
      diagnosticCode?: string; // e.g. invalid_backend_request for malformed/failed
    };

/** Canonical structured-input question (design §6.1). Ids derived: questionId = interactionId+"_q_"+i. */
export interface BridgeUserInputQuestion {
  id: string;
  header?: string;
  prompt: string;
  answerMode: "single" | "multiple" | "text";
  options: BridgeUserInputOption[]; // non-empty (empty is malformed → failed)
  allowsCustomAnswer: boolean; // Claude AskUserQuestion: true (real Other/custom-result path)
  isSecret: boolean; // Claude v1: always false
  required: boolean; // Claude v1: always true
}

/** optionId = questionId+"_o_"+j. */
export interface BridgeUserInputOption {
  id: string;
  label: string;
  description?: string;
}

export interface ResolveUserInputParams {
  sessionId: string;
  interactionId: string;
  clientActionId: string; // canonical UUID v4, reused for idempotent retry of the same action
  action: "answer" | "reject";
  answers?: Array<{
    questionId: string;
    values: Array<
      | { kind: "option"; optionId: string }
      | { kind: "text"; text: string }
    >;
  }>;
}

export interface ResolveUserInputResult {
  interactionId: string;
  outcome: "accepted" | "already_resolved" | "in_progress";
  currentStatus: Extract<BridgeProjectionPart, { type: "user_input" }>["status"];
  headRev: number;
}

export interface BridgeMessageProjection {
  /** Authoritative source id: rollout response_item.id (user) / call_id (tool) / lifecycle turn_id (assistant text). */
  id: string;
  /** Optional echo of a client optimistic id (Phase 3 local-send correlation; absent in Phase 1–2). */
  clientId?: string;
  role: "user" | "assistant" | "system";
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
  /** Transcript lifecycle milestone such as a Claude compact boundary. */
  system?: BridgeMessageProjection;
  /**
   * turn_detail_lazy_v1 (bridge-v1.md "Capability: turn_detail_lazy_v1", frozen 2026-08-30).
   * Absent decodes as "notRequested" (backward compatible). Present iff the backend
   * supports per-turn lazy detail; carries through snapshot/window/upsertTurns.
   */
  detailLoadState?: "notRequested" | "loading" | "loaded" | "failed";
  /** Present iff detailLoadState === "failed"; non-empty. Frozen closed set in bridge-v1.md. */
  detailReasonCode?: string;
  /** Per-turn monotonic generation; bumps on every post-completion mutation. Absent => 0. */
  generation?: number;
}

/** One turnStateOps entry of a BridgeProjectionPatch (turn_detail_lazy_v1). */
export interface BridgeTurnStateOp {
  turnId: string;
  /** "notRequested" is a decode-only default and is never emitted on the wire. */
  detailLoadState: "loading" | "loaded" | "failed";
  /** REQUIRED non-empty iff failed; MUST be absent otherwise. */
  reasonCode?: string;
  /** Turn generation at commit time (stale-write fence component). */
  generation: number;
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
  | { turnId: string; messageId: string; op: "upsert_user_input"; part: Extract<BridgeProjectionPart, { type: "user_input" }> }
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
  /** turn_detail_lazy_v1: turn-level detail-load state ops; applied AFTER upsertTurns, BEFORE partOps. */
  turnStateOps?: BridgeTurnStateOp[];
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

export type BridgeProjectionResume =
  | { kind: "at_head" | "journal"; fromRev: number; toRev: number }
  | {
      kind: "full";
      reason: "cold" | "journal_gap" | "epoch_change" | "limit";
      requestedRev?: number;
    };

/** get_session_projection result: full projection (no sinceRev / server chose snapshot) OR delta patches. */
export type BridgeGetSessionProjectionResult =
  | { projection: BridgeSessionProjection; resume?: BridgeProjectionResume }
  | { patches: BridgeProjectionPatch[]; headRev: number; resume?: BridgeProjectionResume };

// ---------------------------------------------------------------------------
// Projection Window (server-owned windowing) — FROZEN SPEC, not advertised.
// Canonical rules: docs/protocol/bridge-v1.md §Projection Window (R1–R10).
// ---------------------------------------------------------------------------

export type BridgeProjectionWindowDirection =
  | "window_0"
  | "older"
  | "newer"
  | "latest"
  | "locate";

export interface BridgeGetSessionProjectionWindowParams {
  sessionId: string;
  directory?: string;
  /** R1: backend identity is part of the request scope; cursors never cross backends. */
  backendId: string;
  direction: BridgeProjectionWindowDirection;
  /** Opaque, bridge-owned. Required for older/newer (R1/R2). */
  cursor?: string;
  /** Max TURNS requested; hard cap maxWindowTurns (R5). */
  limit?: number;
  /** locate only (R8). */
  anchorTurnId?: string;
}

export interface BridgeProjectionWindow {
  /** Opaque; embeds scope (backendId, bridgeEpoch, sessionId) + generation. */
  windowId: string;
  /** Monotonic per (backendId, sessionId) within one bridgeEpoch; resets with epoch (R6). */
  generation: number;
  coverage: "full" | "window";
  /** Window's first (oldest) turn id; null ONLY for an empty projection (see bridge-v1.md window anchoring paragraph). */
  headTurnId: string | null;
  /** Window's last (newest) turn id; null ONLY for an empty projection; hasNewer=false => this id is the committed live tail. */
  tailTurnId: string | null;
  hasOlder: boolean;
  hasNewer: boolean;
  /** Present iff hasOlder (R5/R7). */
  nextOlderCursor?: string;
  /** Present iff hasNewer (R5/R7). */
  nextNewerCursor?: string;
}

/** get_session_projection_window result: turn-aligned window content + admission cut (R3/R4). */
export interface BridgeGetSessionProjectionWindowResult {
  window: BridgeProjectionWindow;
  /** Window content: turn-aligned, ordered, deduplicated by turnId (R3). */
  turns: BridgeTurnProjection[];
  /** Kernel admission cut; subsequent projection_patch frames chain from it (R4). */
  syncRev: number;
  resume?: { kind: "at_head" };
}

// ── Web Push (web_push_v1) — additive; see bridge-v1.md "Web Push" section ──────

export type BridgeWebPushClientCapability = "web_push_v1";

/** hello_ack additive field; present only when the client declared web_push_v1 and the store is healthy. */
export interface BridgeHelloAckWebPush {
  schemaVersion: 1;
  /** base64url-unpadded 65-byte uncompressed P-256 public key. Absent when status === "misconfigured". */
  vapidPublicKey?: string;
  /** Additive diagnostic; "misconfigured" ⇒ register/send disabled, unregister stays reachable. */
  status?: "ok" | "misconfigured";
}

export interface BridgeRegisterPushSubscriptionParams {
  schemaVersion: 1;
  platform: string;
  /** Must equal hello_ack.webPush.vapidPublicKey byte-for-byte. */
  applicationServerKey: string;
  subscription: {
    endpoint: string;
    expirationTime: number | null;
    keys: { p256dh: string; auth: string };
  };
}

export interface BridgeRegisterPushSubscriptionResult {
  subscriptionId: string;
  registeredAtMillis: number;
}

export interface BridgeUnregisterPushSubscriptionParams {
  schemaVersion: 1;
  subscriptionId: string;
}

export interface BridgeUnregisterPushSubscriptionResult {
  removed: boolean;
}

/** MacBridge-side fixed plaintext schema (RFC 8291-encrypted before transport). */
export interface BridgeWebPushPayloadV1 {
  schemaVersion: 1;
  notification: {
    title: string;
    body: string;
    tag: string;
  };
  target: {
    bridgeId: string;
    backendId: string;
    sessionId: string;
    eventId: string;
    anchor: { kind: "turn" | "interaction"; id: string } | null;
  };
}

/** Service Worker → window message on notificationclick (client-side only, never on the wire). */
export interface BridgePushNavigateMessage {
  type: "CORDCODE_PUSH_NAVIGATE_V1";
  target: BridgeWebPushPayloadV1["target"];
}
