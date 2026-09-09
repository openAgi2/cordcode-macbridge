package grokbuild

// ACP authenticate method selection. Mirrors the official grok pager
// (xai-grok-pager/src/acp/mod.rs select_eager_auth_method, checkout
// 75810042; installed grok 1.0.24):
//
//  1. Agent _meta.defaultAuthMethodId when that id is in authMethods
//  2. cached_token if advertised
//  3. authMethods[0]
//
// Do NOT authenticate with authMethods[0] unconditionally. Unpinned grok
// advertises xai.api_key first whenever any model has its own credentials
// (auth_method.rs should_advertise_xai_api_key / build_unpinned), even if
// the user is actually on OIDC. The pager still authenticates cached_token
// via defaultAuthMethodId. Picking xai.api_key with no global XAI_API_KEY
// stamps the sampler as API-key auth, sends an empty bearer
// (auth_kind=none), and session/prompt returns JSON-RPC -32603.

const (
	acpAuthMethodCachedToken = "cached_token"
	acpAuthMethodXAIAPIKey   = "xai.api_key"
)

func (r initializeResult) eagerAuthMethodID() string {
	defaultID := ""
	if r.Meta != nil {
		defaultID = r.Meta.DefaultAuthMethodID
	}
	return selectEagerAuthMethod(r.AuthMethods, defaultID)
}

func selectEagerAuthMethod(methods []authMethod, defaultID string) string {
	if defaultID != "" {
		for _, m := range methods {
			if m.ID == defaultID {
				return defaultID
			}
		}
	}
	for _, m := range methods {
		if m.ID == acpAuthMethodCachedToken {
			return m.ID
		}
	}
	if len(methods) > 0 {
		return methods[0].ID
	}
	return ""
}
