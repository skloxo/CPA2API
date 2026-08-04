package cliproxy

import (
	"context"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func (s *Service) registerModelsForAuth(a *coreauth.Auth) {
	if a == nil || a.ID == "" {
		return
	}
	if a.Disabled {
		GlobalModelRegistry().UnregisterClient(a.ID)
		return
	}
	authKind := strings.ToLower(strings.TrimSpace(a.Attributes["auth_kind"]))
	if authKind == "" {
		if kind, _ := a.AccountInfo(); strings.EqualFold(kind, "api_key") {
			authKind = "apikey"
		}
	}
	if a.Attributes != nil {
		if v := strings.TrimSpace(a.Attributes["gemini_virtual_primary"]); strings.EqualFold(v, "true") {
			GlobalModelRegistry().UnregisterClient(a.ID)
			return
		}
	}
	// Unregister legacy client ID (if present) to avoid double counting
	if a.Runtime != nil {
		if idGetter, ok := a.Runtime.(interface{ GetClientID() string }); ok {
			if rid := idGetter.GetClientID(); rid != "" && rid != a.ID {
				GlobalModelRegistry().UnregisterClient(rid)
			}
		}
	}
	provider := strings.ToLower(strings.TrimSpace(a.Provider))
	compatProviderKey, compatDisplayName, compatDetected := openAICompatInfoFromAuth(a)
	if compatDetected {
		provider = "openai-compatibility"
	}
	excluded := s.oauthExcludedModels(provider, authKind, a)
	// Dynamically merge per-account exclusions (resolving the parent's Metadata if it's a virtual child) with live global exclusions.
	var metadata map[string]any
	if a.Attributes != nil {
		if parentID := strings.TrimSpace(a.Attributes["gemini_virtual_parent"]); parentID != "" && s.coreManager != nil {
			if parentAuth, ok := s.coreManager.GetByID(parentID); ok && parentAuth != nil {
				metadata = parentAuth.Metadata
			}
		}
	}
	if metadata == nil {
		metadata = a.Metadata
	}
	if metadata != nil {
		perAccount := extractPerAccountExcludedModels(metadata)
		seen := make(map[string]struct{})
		var merged []string
		for _, entry := range excluded {
			trimmed := strings.ToLower(strings.TrimSpace(entry))
			if trimmed != "" {
				if _, exists := seen[trimmed]; !exists {
					seen[trimmed] = struct{}{}
					merged = append(merged, entry)
				}
			}
		}
		for _, entry := range perAccount {
			trimmed := strings.ToLower(strings.TrimSpace(entry))
			if trimmed != "" {
				if _, exists := seen[trimmed]; !exists {
					seen[trimmed] = struct{}{}
					merged = append(merged, entry)
				}
			}
		}
		excluded = merged
	} else if a.Attributes != nil {
		// Fallback to pre-merged list if present (e.g. for compatibility virtual child auths or unit tests where Metadata is nil)
		if val, ok := a.Attributes["excluded_models"]; ok && strings.TrimSpace(val) != "" {
			excluded = strings.Split(val, ",")
		}
	}
	var models []*ModelInfo
	switch provider {
	case "gemini":
		models = registry.GetGeminiModels()
		if entry := s.resolveConfigGeminiKey(a); entry != nil {
			if len(entry.Models) > 0 {
				models = buildGeminiConfigModels(entry)
			}
			if authKind == "apikey" {
				excluded = mergeExclusions(excluded, entry.ExcludedModels)
			}
		}
		models = applyExcludedModels(models, excluded)
	case "vertex":
		// Vertex AI Gemini supports the same model identifiers as Gemini.
		models = registry.GetGeminiVertexModels()
		if entry := s.resolveConfigVertexCompatKey(a); entry != nil {
			if len(entry.Models) > 0 {
				models = buildVertexCompatConfigModels(entry)
			}
			if authKind == "apikey" {
				excluded = mergeExclusions(excluded, entry.ExcludedModels)
			}
		}
		models = applyExcludedModels(models, excluded)
	case "gemini-cli":
		models = registry.GetGeminiCLIModels()
		models = applyExcludedModels(models, excluded)
	case "aistudio":
		models = registry.GetAIStudioModels()
		models = applyExcludedModels(models, excluded)
	case "antigravity":
		models = registry.GetAntigravityModels()
		models = applyExcludedModels(models, excluded)
	case "claude":
		models = registry.GetClaudeModels()
		if entry := s.resolveConfigClaudeKey(a); entry != nil {
			if len(entry.Models) > 0 {
				models = buildClaudeConfigModels(entry)
			}
			if authKind == "apikey" {
				excluded = mergeExclusions(excluded, entry.ExcludedModels)
			}
		}
		models = applyExcludedModels(models, excluded)
	case "codex":
		codexPlanType := ""
		if a.Attributes != nil {
			codexPlanType = strings.TrimSpace(a.Attributes["plan_type"])
		}
		switch strings.ToLower(codexPlanType) {
		case "pro":
			models = registry.GetCodexProModels()
		case "plus":
			models = registry.GetCodexPlusModels()
		case "team", "business", "go":
			models = registry.GetCodexTeamModels()
		case "free":
			models = registry.GetCodexFreeModels()
		default:
			models = registry.GetCodexProModels()
		}
		if entry := s.resolveConfigCodexKey(a); entry != nil {
			if len(entry.Models) > 0 {
				models = buildCodexConfigModels(entry)
			}
			if authKind == "apikey" {
				excluded = mergeExclusions(excluded, entry.ExcludedModels)
			}
		}
		models = applyExcludedModels(models, excluded)
	case "kimi":
		models = registry.GetKimiModels()
		models = applyExcludedModels(models, excluded)
	case "qwen":
		models = registry.GetDiscoveredModels("qwen")
		staticModels := registry.GetQwenModels()
		models = unionQwenModels(models, staticModels)
		models = applyExcludedModels(models, excluded)
	case "xai":
		models = registry.GetXAIModels()
		models = applyExcludedModels(models, excluded)
	default:
		// Handle OpenAI-compatibility providers by name using config
		if s.cfg != nil {
			providerKey := provider
			compatName := strings.TrimSpace(a.Provider)
			isCompatAuth := false
			if compatDetected {
				if compatProviderKey != "" {
					providerKey = compatProviderKey
				}
				if compatDisplayName != "" {
					compatName = compatDisplayName
				}
				isCompatAuth = true
			}
			if strings.EqualFold(providerKey, "openai-compatibility") {
				isCompatAuth = true
				if a.Attributes != nil {
					if v := strings.TrimSpace(a.Attributes["compat_name"]); v != "" {
						compatName = v
					}
					if v := strings.TrimSpace(a.Attributes["provider_key"]); v != "" {
						providerKey = strings.ToLower(v)
						isCompatAuth = true
					}
				}
				if providerKey == "openai-compatibility" && compatName != "" {
					providerKey = strings.ToLower(compatName)
				}
			} else if a.Attributes != nil {
				if v := strings.TrimSpace(a.Attributes["compat_name"]); v != "" {
					compatName = v
					isCompatAuth = true
				}
				if v := strings.TrimSpace(a.Attributes["provider_key"]); v != "" {
					providerKey = strings.ToLower(v)
					isCompatAuth = true
				}
			}
			for i := range s.cfg.OpenAICompatibility {
				compat := &s.cfg.OpenAICompatibility[i]
				if compat.Disabled {
					continue
				}
				if strings.EqualFold(compat.Name, compatName) {
					isCompatAuth = true
					ms := buildOpenAICompatibilityConfigModels(compat)
					// Register and return
					if len(ms) > 0 {
						resolvedProviderKey := strings.ToLower(compat.Name)
						compatExcluded := s.oauthExcludedModels(providerKey, authKind, a)
						mergedExcluded := mergeExclusions(excluded, compatExcluded)
						ms = applyExcludedModels(ms, mergedExcluded)
						s.registerResolvedModelsForAuth(a, resolvedProviderKey, applyModelPrefixes(ms, a.Prefix, s.cfg.ForceModelPrefix))
					} else {
						// Ensure stale registrations are cleared when model list becomes empty.
						GlobalModelRegistry().UnregisterClient(a.ID)
					}
					return
				}
			}
			if isCompatAuth {
				// No matching provider found or models removed entirely; drop any prior registration.
				GlobalModelRegistry().UnregisterClient(a.ID)
				return
			}
		}
	}
	models = applyOAuthModelAlias(s.cfg, provider, authKind, models)
	if len(models) > 0 {
		key := provider
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(a.Provider))
		}
		s.registerResolvedModelsForAuth(a, key, applyModelPrefixes(models, a.Prefix, s.cfg != nil && s.cfg.ForceModelPrefix))
		return
	}

	GlobalModelRegistry().UnregisterClient(a.ID)
}

// refreshModelRegistrationForAuth re-applies the latest model registration for
// one auth and reconciles any concurrent auth changes that race with the
// refresh. Callers are expected to pre-filter provider membership.
//
// Re-registration is deliberate: registry cooldown/suspension state is treated
// as part of the previous registration snapshot and is cleared when the auth is
// rebound to the refreshed model catalog.
func (s *Service) refreshModelRegistrationForAuth(current *coreauth.Auth) bool {
	if s == nil || s.coreManager == nil || current == nil || current.ID == "" {
		return false
	}

	if !current.Disabled {
		s.ensureExecutorsForAuth(current)
	}
	s.registerModelsForAuth(current)
	s.coreManager.ReconcileRegistryModelStates(context.Background(), current.ID)

	latest, ok := s.latestAuthForModelRegistration(current.ID)
	if !ok || latest.Disabled {
		GlobalModelRegistry().UnregisterClient(current.ID)
		s.coreManager.RefreshSchedulerEntry(current.ID)
		return false
	}

	// Re-apply the latest auth snapshot so concurrent auth updates cannot leave
	// stale model registrations behind. This may duplicate registration work when
	// no auth fields changed, but keeps the refresh path simple and correct.
	s.ensureExecutorsForAuth(latest)
	s.registerModelsForAuth(latest)
	s.coreManager.ReconcileRegistryModelStates(context.Background(), latest.ID)
	s.coreManager.RefreshSchedulerEntry(current.ID)
	return true
}

// latestAuthForModelRegistration returns the latest auth snapshot regardless of
// provider membership. Callers use this after a registration attempt to restore
// whichever state currently owns the client ID in the global registry.
func (s *Service) latestAuthForModelRegistration(authID string) (*coreauth.Auth, bool) {
	if s == nil || s.coreManager == nil || authID == "" {
		return nil, false
	}
	auth, ok := s.coreManager.GetByID(authID)
	if !ok || auth == nil || auth.ID == "" {
		return nil, false
	}
	return auth, true
}

func (s *Service) resolveConfigClaudeKey(auth *coreauth.Auth) *config.ClaudeKey {
	if auth == nil || s.cfg == nil {
		return nil
	}
	var attrKey, attrBase string
	if auth.Attributes != nil {
		attrKey = strings.TrimSpace(auth.Attributes["api_key"])
		attrBase = strings.TrimSpace(auth.Attributes["base_url"])
	}
	for i := range s.cfg.ClaudeKey {
		entry := &s.cfg.ClaudeKey[i]
		cfgKey := strings.TrimSpace(entry.APIKey)
		cfgBase := strings.TrimSpace(entry.BaseURL)
		if attrKey != "" && attrBase != "" {
			if strings.EqualFold(cfgKey, attrKey) && strings.EqualFold(cfgBase, attrBase) {
				return entry
			}
			continue
		}
		if attrKey != "" && strings.EqualFold(cfgKey, attrKey) {
			if cfgBase == "" || strings.EqualFold(cfgBase, attrBase) {
				return entry
			}
		}
		if attrKey == "" && attrBase != "" && strings.EqualFold(cfgBase, attrBase) {
			return entry
		}
	}
	if attrKey != "" {
		for i := range s.cfg.ClaudeKey {
			entry := &s.cfg.ClaudeKey[i]
			if strings.EqualFold(strings.TrimSpace(entry.APIKey), attrKey) {
				return entry
			}
		}
	}
	return nil
}

func (s *Service) resolveConfigGeminiKey(auth *coreauth.Auth) *config.GeminiKey {
	if auth == nil || s.cfg == nil {
		return nil
	}
	var attrKey, attrBase string
	if auth.Attributes != nil {
		attrKey = strings.TrimSpace(auth.Attributes["api_key"])
		attrBase = strings.TrimSpace(auth.Attributes["base_url"])
	}
	for i := range s.cfg.GeminiKey {
		entry := &s.cfg.GeminiKey[i]
		cfgKey := strings.TrimSpace(entry.APIKey)
		cfgBase := strings.TrimSpace(entry.BaseURL)
		if attrKey != "" && strings.EqualFold(cfgKey, attrKey) {
			if cfgBase == "" || strings.EqualFold(cfgBase, attrBase) {
				return entry
			}
			continue
		}
		if attrKey == "" && attrBase != "" && strings.EqualFold(cfgBase, attrBase) {
			return entry
		}
	}
	return nil
}

func (s *Service) resolveConfigVertexCompatKey(auth *coreauth.Auth) *config.VertexCompatKey {
	if auth == nil || s.cfg == nil {
		return nil
	}
	var attrKey, attrBase string
	if auth.Attributes != nil {
		attrKey = strings.TrimSpace(auth.Attributes["api_key"])
		attrBase = strings.TrimSpace(auth.Attributes["base_url"])
	}
	for i := range s.cfg.VertexCompatAPIKey {
		entry := &s.cfg.VertexCompatAPIKey[i]
		cfgKey := strings.TrimSpace(entry.APIKey)
		cfgBase := strings.TrimSpace(entry.BaseURL)
		if attrKey != "" && strings.EqualFold(cfgKey, attrKey) {
			if cfgBase == "" || strings.EqualFold(cfgBase, attrBase) {
				return entry
			}
			continue
		}
		if attrKey == "" && attrBase != "" && strings.EqualFold(cfgBase, attrBase) {
			return entry
		}
	}
	if attrKey != "" {
		for i := range s.cfg.VertexCompatAPIKey {
			entry := &s.cfg.VertexCompatAPIKey[i]
			if strings.EqualFold(strings.TrimSpace(entry.APIKey), attrKey) {
				return entry
			}
		}
	}
	return nil
}

func (s *Service) resolveConfigCodexKey(auth *coreauth.Auth) *config.CodexKey {
	if auth == nil || s.cfg == nil {
		return nil
	}
	var attrKey, attrBase string
	if auth.Attributes != nil {
		attrKey = strings.TrimSpace(auth.Attributes["api_key"])
		attrBase = strings.TrimSpace(auth.Attributes["base_url"])
	}
	for i := range s.cfg.CodexKey {
		entry := &s.cfg.CodexKey[i]
		cfgKey := strings.TrimSpace(entry.APIKey)
		cfgBase := strings.TrimSpace(entry.BaseURL)
		if attrKey != "" && strings.EqualFold(cfgKey, attrKey) {
			if cfgBase == "" || strings.EqualFold(cfgBase, attrBase) {
				return entry
			}
			continue
		}
		if attrKey == "" && attrBase != "" && strings.EqualFold(cfgBase, attrBase) {
			return entry
		}
	}
	return nil
}

func (s *Service) oauthExcludedModels(provider, authKind string, a *coreauth.Auth) []string {
	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()
	if cfg == nil {
		return nil
	}
	providerKey := strings.ToLower(strings.TrimSpace(provider))
	if a != nil {
		compatProviderKey, compatDisplayName, compatDetected := openAICompatInfoFromAuth(a)
		if compatDetected {
			if compatProviderKey != "" {
				providerKey = compatProviderKey
			} else if compatDisplayName != "" {
				providerKey = compatDisplayName
			}
		}
		if a.Attributes != nil {
			if v := strings.TrimSpace(a.Attributes["provider_key"]); v != "" {
				providerKey = strings.ToLower(v)
			}
		}
		if strings.EqualFold(providerKey, "openai-compatibility") {
			compatName := strings.TrimSpace(a.Provider)
			if a.Attributes != nil {
				if v := strings.TrimSpace(a.Attributes["compat_name"]); v != "" {
					compatName = v
				}
			}
			if compatName != "" {
				providerKey = strings.ToLower(compatName)
			}
		}
	}
	providerKey = strings.ToLower(strings.TrimSpace(providerKey))
	globalExclusions := cfg.OAuthExcludedModels[providerKey]

	var credentialExclusions []string
	if authKind == "apikey" && a != nil {
		switch providerKey {
		case "gemini":
			if entry := s.resolveConfigGeminiKey(a); entry != nil {
				credentialExclusions = entry.ExcludedModels
			}
		case "vertex", "vertex-api-key":
			if entry := s.resolveConfigVertexCompatKey(a); entry != nil {
				credentialExclusions = entry.ExcludedModels
			}
		case "claude":
			if entry := s.resolveConfigClaudeKey(a); entry != nil {
				credentialExclusions = entry.ExcludedModels
			}
		case "codex":
			if entry := s.resolveConfigCodexKey(a); entry != nil {
				credentialExclusions = entry.ExcludedModels
			}
		default:
			// check OpenAICompatibility
			compatName := strings.TrimSpace(a.Provider)
			if a.Attributes != nil {
				if v := strings.TrimSpace(a.Attributes["compat_name"]); v != "" {
					compatName = v
				}
			}
			for i := range cfg.OpenAICompatibility {
				compat := &cfg.OpenAICompatibility[i]
				if strings.EqualFold(compat.Name, compatName) {
					credentialExclusions = compat.ExcludedModels
					break
				}
			}
		}
	}

	return mergeExclusions(globalExclusions, credentialExclusions)
}

func applyExcludedModels(models []*ModelInfo, excluded []string) []*ModelInfo {
	if len(models) == 0 || len(excluded) == 0 {
		return models
	}

	patterns := make([]string, 0, len(excluded))
	for _, item := range excluded {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			patterns = append(patterns, strings.ToLower(trimmed))
		}
	}
	if len(patterns) == 0 {
		return models
	}

	filtered := make([]*ModelInfo, 0, len(models))
	for _, model := range models {
		if model == nil {
			continue
		}
		modelID := strings.ToLower(strings.TrimSpace(model.ID))
		blocked := false
		for _, pattern := range patterns {
			if matchWildcard(pattern, modelID) {
				blocked = true
				break
			}
		}
		if !blocked {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

func mergeExclusions(a, b []string) []string {
	seen := make(map[string]struct{})
	var merged []string
	for _, entry := range a {
		trimmed := strings.ToLower(strings.TrimSpace(entry))
		if trimmed != "" {
			if _, exists := seen[trimmed]; !exists {
				seen[trimmed] = struct{}{}
				merged = append(merged, entry)
			}
		}
	}
	for _, entry := range b {
		trimmed := strings.ToLower(strings.TrimSpace(entry))
		if trimmed != "" {
			if _, exists := seen[trimmed]; !exists {
				seen[trimmed] = struct{}{}
				merged = append(merged, entry)
			}
		}
	}
	return merged
}

func applyModelPrefixes(models []*ModelInfo, prefix string, forceModelPrefix bool) []*ModelInfo {
	trimmedPrefix := strings.TrimSpace(prefix)
	if trimmedPrefix == "" || len(models) == 0 {
		return models
	}

	out := make([]*ModelInfo, 0, len(models)*2)
	seen := make(map[string]struct{}, len(models)*2)

	addModel := func(model *ModelInfo) {
		if model == nil {
			return
		}
		id := strings.TrimSpace(model.ID)
		if id == "" {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		out = append(out, model)
	}

	for _, model := range models {
		if model == nil {
			continue
		}
		baseID := strings.TrimSpace(model.ID)
		if baseID == "" {
			continue
		}
		if !forceModelPrefix || trimmedPrefix == baseID {
			addModel(model)
		}
		clone := *model
		clone.ID = trimmedPrefix + "/" + baseID
		addModel(&clone)
	}
	return out
}

// matchWildcard performs case-insensitive wildcard matching where '*' matches any substring.
func matchWildcard(pattern, value string) bool {
	if pattern == "" {
		return false
	}

	// Fast path for exact match (no wildcard present).
	if !strings.Contains(pattern, "*") {
		return pattern == value
	}

	parts := strings.Split(pattern, "*")
	// Handle prefix.
	if prefix := parts[0]; prefix != "" {
		if !strings.HasPrefix(value, prefix) {
			return false
		}
		value = value[len(prefix):]
	}

	// Handle suffix.
	if suffix := parts[len(parts)-1]; suffix != "" {
		if !strings.HasSuffix(value, suffix) {
			return false
		}
		value = value[:len(value)-len(suffix)]
	}

	// Handle middle segments in order.
	for i := 1; i < len(parts)-1; i++ {
		segment := parts[i]
		if segment == "" {
			continue
		}
		idx := strings.Index(value, segment)
		if idx < 0 {
			return false
		}
		value = value[idx+len(segment):]
	}

	return true
}

type modelEntry interface {
	GetName() string
	GetAlias() string
}

func buildOpenAICompatibilityConfigModels(compat *config.OpenAICompatibility) []*ModelInfo {
	if compat == nil || len(compat.Models) == 0 {
		return nil
	}
	now := time.Now().Unix()
	models := make([]*ModelInfo, 0, len(compat.Models))
	for i := range compat.Models {
		model := compat.Models[i]
		modelID := strings.TrimSpace(model.Alias)
		if modelID == "" {
			modelID = strings.TrimSpace(model.Name)
		}
		if modelID == "" {
			continue
		}
		modelType := "openai-compatibility"
		if model.Image {
			modelType = registry.OpenAIImageModelType
		}
		thinking := model.Thinking
		if thinking == nil && !model.Image {
			thinking = &registry.ThinkingSupport{Levels: []string{"low", "medium", "high"}}
		}
		models = append(models, &ModelInfo{
			ID:          modelID,
			Object:      "model",
			Created:     now,
			OwnedBy:     compat.Name,
			Type:        modelType,
			DisplayName: modelID,
			UserDefined: false,
			Thinking:    thinking,
		})
	}
	return models
}

func buildConfigModels[T modelEntry](models []T, ownedBy, modelType string) []*ModelInfo {
	if len(models) == 0 {
		return nil
	}
	now := time.Now().Unix()
	out := make([]*ModelInfo, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for i := range models {
		model := models[i]
		name := strings.TrimSpace(model.GetName())
		alias := strings.TrimSpace(model.GetAlias())
		if alias == "" {
			alias = name
		}
		if alias == "" {
			continue
		}
		key := strings.ToLower(alias)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		display := name
		if display == "" {
			display = alias
		}
		info := &ModelInfo{
			ID:          alias,
			Object:      "model",
			Created:     now,
			OwnedBy:     ownedBy,
			Type:        modelType,
			DisplayName: display,
			UserDefined: true,
		}
		if name != "" {
			if upstream := registry.LookupStaticModelInfo(name); upstream != nil && upstream.Thinking != nil {
				info.Thinking = upstream.Thinking
			}
		}
		out = append(out, info)
	}
	return out
}

func buildVertexCompatConfigModels(entry *config.VertexCompatKey) []*ModelInfo {
	if entry == nil {
		return nil
	}
	return buildConfigModels(entry.Models, "google", "vertex")
}

func buildGeminiConfigModels(entry *config.GeminiKey) []*ModelInfo {
	if entry == nil {
		return nil
	}
	return buildConfigModels(entry.Models, "google", "gemini")
}

func buildClaudeConfigModels(entry *config.ClaudeKey) []*ModelInfo {
	if entry == nil {
		return nil
	}
	return buildConfigModels(entry.Models, "anthropic", "claude")
}

func buildCodexConfigModels(entry *config.CodexKey) []*ModelInfo {
	if entry == nil {
		return nil
	}
	return registry.WithCodexBuiltins(buildConfigModels(entry.Models, "openai", "openai"))
}

func rewriteModelInfoName(name, oldID, newID string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return name
	}
	oldID = strings.TrimSpace(oldID)
	newID = strings.TrimSpace(newID)
	if oldID == "" || newID == "" {
		return name
	}
	if strings.EqualFold(oldID, newID) {
		return name
	}
	if strings.EqualFold(trimmed, oldID) {
		return newID
	}
	if strings.HasSuffix(trimmed, "/"+oldID) {
		prefix := strings.TrimSuffix(trimmed, oldID)
		return prefix + newID
	}
	if trimmed == "models/"+oldID {
		return "models/" + newID
	}
	return name
}

func applyOAuthModelAlias(cfg *config.Config, provider, authKind string, models []*ModelInfo) []*ModelInfo {
	if cfg == nil || len(models) == 0 {
		return models
	}
	channel := coreauth.OAuthModelAliasChannel(provider, authKind)
	if channel == "" || len(cfg.OAuthModelAlias) == 0 {
		return models
	}
	aliases := cfg.OAuthModelAlias[channel]
	if len(aliases) == 0 {
		return models
	}

	type aliasEntry struct {
		alias string
		fork  bool
	}

	forward := make(map[string][]aliasEntry, len(aliases))
	for i := range aliases {
		name := strings.TrimSpace(aliases[i].Name)
		alias := strings.TrimSpace(aliases[i].Alias)
		if name == "" || alias == "" {
			continue
		}
		if strings.EqualFold(name, alias) {
			continue
		}
		key := strings.ToLower(name)
		forward[key] = append(forward[key], aliasEntry{alias: alias, fork: aliases[i].Fork})
	}
	if len(forward) == 0 {
		return models
	}

	out := make([]*ModelInfo, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		if model == nil {
			continue
		}
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		entries := forward[key]
		if len(entries) == 0 {
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, model)
			continue
		}

		keepOriginal := false
		for _, entry := range entries {
			if entry.fork {
				keepOriginal = true
				break
			}
		}
		if keepOriginal {
			if _, exists := seen[key]; !exists {
				seen[key] = struct{}{}
				out = append(out, model)
			}
		}

		addedAlias := false
		for _, entry := range entries {
			mappedID := strings.TrimSpace(entry.alias)
			if mappedID == "" {
				continue
			}
			if strings.EqualFold(mappedID, id) {
				continue
			}
			aliasKey := strings.ToLower(mappedID)
			if _, exists := seen[aliasKey]; exists {
				continue
			}
			seen[aliasKey] = struct{}{}
			clone := *model
			clone.ID = mappedID
			if clone.Name != "" {
				clone.Name = rewriteModelInfoName(clone.Name, id, mappedID)
			}
			out = append(out, &clone)
			addedAlias = true
		}

		if !keepOriginal && !addedAlias {
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, model)
		}
	}
	return out
}

func extractPerAccountExcludedModels(metadata map[string]any) []string {
	if metadata == nil {
		return nil
	}
	raw, ok := metadata["excluded_models"]
	if !ok {
		raw, ok = metadata["excluded-models"]
	}
	if !ok || raw == nil {
		return nil
	}
	var stringSlice []string
	switch v := raw.(type) {
	case []string:
		stringSlice = v
	case []interface{}:
		stringSlice = make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				stringSlice = append(stringSlice, s)
			}
		}
	default:
		return nil
	}
	result := make([]string, 0, len(stringSlice))
	for _, s := range stringSlice {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// IsModelExcluded checks if a model is globally excluded.
func (s *Service) IsModelExcluded(modelID, provider string) bool {
	if s == nil {
		return false
	}
	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()

	return isModelGloballyExcluded(cfg, modelID, provider)
}

func getModelProvider(modelID string) string {
	baseID := strings.ToLower(modelID)
	if idx := strings.Index(baseID, "/"); idx >= 0 {
		baseID = baseID[idx+1:]
	}
	if strings.HasPrefix(baseID, "gemini-") {
		return "gemini"
	}
	if strings.HasPrefix(baseID, "grok-") {
		return "xai"
	}
	if strings.HasPrefix(baseID, "qwen") {
		return "qwen"
	}
	if strings.Contains(baseID, "moonshot") || strings.HasPrefix(baseID, "kimi") {
		return "kimi"
	}
	return "codex"
}

func isModelGloballyExcluded(cfg *config.Config, modelID string, provider string) bool {
	if cfg == nil {
		return false
	}

	modelID = strings.ToLower(strings.TrimSpace(modelID))
	if modelID == "" {
		return true
	}

	baseID := modelID
	if idx := strings.Index(modelID, "/"); idx >= 0 {
		baseID = modelID[idx+1:]
	}

	// Determine channels/providers to check
	channels := []string{getModelProvider(modelID)}
	if provider != "" {
		channels = append(channels, strings.ToLower(strings.TrimSpace(provider)))
	}
	if idx := strings.Index(modelID, "/"); idx >= 0 {
		prefixProvider := strings.ToLower(strings.TrimSpace(modelID[:idx]))
		if prefixProvider != "" {
			channels = append(channels, prefixProvider)
		}
	}

	// Remove duplicates from channels
	seenChannels := make(map[string]bool)
	var uniqueChannels []string
	for _, c := range channels {
		if c != "" && !seenChannels[c] {
			seenChannels[c] = true
			uniqueChannels = append(uniqueChannels, c)
		}
	}

	for _, channel := range uniqueChannels {
		// 2. Check oauth-excluded-models
		if len(cfg.OAuthExcludedModels) > 0 {
			if excluded, ok := cfg.OAuthExcludedModels[channel]; ok {
				for _, pattern := range excluded {
					trimmedPattern := strings.ToLower(strings.TrimSpace(pattern))
					if trimmedPattern != "" && (matchWildcard(trimmedPattern, modelID) || matchWildcard(trimmedPattern, baseID)) {
						return true
					}
				}
			}
		}
	}

	return false
}

func (s *Service) hasActiveQwenCredentials() bool {
	if s.coreManager == nil {
		return false
	}
	auths := s.coreManager.List()
	for _, a := range auths {
		if a != nil && strings.EqualFold(strings.TrimSpace(a.Provider), "qwen") && !a.Disabled {
			return true
		}
	}
	return false
}

func (s *Service) checkQwenCredentialsState() {
	if !s.hasActiveQwenCredentials() {
		disc := executor.GetQwenModelDiscovery(s.cfg)
		disc.Stop()
		disc.ClearCredentials()
		registry.GetGlobalRegistry().UnregisterClient("qwen-dynamic")
		return
	}

	var token, cookie string
	auths := s.coreManager.List()
	for _, a := range auths {
		if a != nil && strings.EqualFold(strings.TrimSpace(a.Provider), "qwen") && !a.Disabled {
			if a.Metadata != nil {
				if v, ok := a.Metadata["access_token"].(string); ok && strings.TrimSpace(v) != "" {
					token = v
				}
				if v, ok := a.Metadata["cookie"].(string); ok && strings.TrimSpace(v) != "" {
					cookie = v
				}
			}
			if a.Attributes != nil {
				if token == "" {
					if v := a.Attributes["access_token"]; v != "" {
						token = v
					}
				}
				if token == "" {
					if v := a.Attributes["api_key"]; v != "" {
						token = v
					}
				}
				if cookie == "" {
					if v := a.Attributes["cookie"]; v != "" {
						cookie = v
					}
				}
			}
			if token != "" {
				break
			}
		}
	}
	if token != "" {
		disc := executor.GetQwenModelDiscovery(s.cfg)
		disc.SetCredentials(token, cookie, "")
	}
}

func unionQwenModels(a, b []*registry.ModelInfo) []*registry.ModelInfo {
	seen := make(map[string]bool)
	var result []*registry.ModelInfo
	for _, m := range a {
		if m != nil && m.ID != "" {
			key := strings.ToLower(strings.TrimSpace(m.ID))
			if !seen[key] {
				seen[key] = true
				result = append(result, m)
			}
		}
	}
	for _, m := range b {
		if m != nil && m.ID != "" {
			key := strings.ToLower(strings.TrimSpace(m.ID))
			if !seen[key] {
				seen[key] = true
				result = append(result, m)
			}
		}
	}
	return result
}
