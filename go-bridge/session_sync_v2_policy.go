package gobridge

import "time"

// SessionSyncV2PolicyVersion freezes the K3 admission policy. Changing any value below requires
// a new version and a fresh shadow evidence report; runtime measurements must not silently relax
// these product anchors.
const SessionSyncV2PolicyVersion = "k3-v1"

const (
	projectionHydrateRetryInitial        = time.Second
	projectionHydrateRetryMaximum        = 30 * time.Second
	projectionHydrateRetryJitterFraction = 0.20
	projectionHydrateMaxConcurrent       = 4

	projectionCheckpointHitP95SLO = 2 * time.Second
	projectionColdOpenP50SLO      = 5 * time.Second
	projectionColdOpenP95SLO      = 15 * time.Second
	projectionColdOpenMaximumSLO  = 30 * time.Second
)
