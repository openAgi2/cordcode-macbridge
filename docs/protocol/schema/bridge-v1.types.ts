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

export interface BridgeHello {
  type: "hello";
  client: {
    app: string;
    version: string;
    deviceId: string;
  };
  protocol: BridgeProtocol;
}

export interface BridgeRegister {
  type: "register";
  client: {
    id: string;
    name: string;
    version: string;
  };
  protocol: Pick<BridgeProtocol, "name" | "version">;
  lastBridgeEpoch?: string;
  lastEventId?: string;
  lastSeenBySession?: Record<string, { eventId: string; seq: number }>;
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
  recovery?: {
    type?: string;
    affectedSessions?: Array<{ backendId?: string; sessionId?: string }>;
  };
  error?: BridgeWireError;
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
  | "get_workspace_diff";

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
  | "question_resolved";

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

/** Minimal session info returned by list_sessions/get_session. */
export interface BridgeSessionInfo {
  id: string;
  title: string;
  directory?: string;
  modelId?: string;
  providerId?: string;
  effectiveModelId?: string;
  effectiveProviderId?: string;
  reasoningEffort?: string;
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
}

export interface BridgeGetSessionMessagesResult {
  messages: unknown[];
  oldestCursor?: string; // send as beforeCursor for the next (older) page
  newestCursor?: string; // informational, for client merge/dedup
  hasMore: boolean;
  contextUsage?: unknown;
}

/** Backend capability string for get_session_messages history paging, not list_sessions paging. */
export type BridgeSessionPaginationCapability = "session_pagination";
