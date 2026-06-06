package cliproxy

import (
	"context"
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func TestEnsureExecutorsForAuth_CodexDoesNotReplaceInNormalMode(t *testing.T) {
	service := &Service{
		cfg:         &config.Config{},
		coreManager: coreauth.NewManager(nil, nil, nil),
	}
	auth := &coreauth.Auth{
		ID:       "codex-auth-1",
		Provider: "codex",
		Status:   coreauth.StatusActive,
	}

	service.ensureExecutorsForAuth(auth)
	firstExecutor, okFirst := service.coreManager.Executor("codex")
	if !okFirst || firstExecutor == nil {
		t.Fatal("expected codex executor after first bind")
	}

	service.ensureExecutorsForAuth(auth)
	secondExecutor, okSecond := service.coreManager.Executor("codex")
	if !okSecond || secondExecutor == nil {
		t.Fatal("expected codex executor after second bind")
	}

	if firstExecutor != secondExecutor {
		t.Fatal("expected codex executor to stay unchanged in normal mode")
	}
}

func TestEnsureExecutorsForAuthWithMode_CodexForceReplace(t *testing.T) {
	service := &Service{
		cfg:         &config.Config{},
		coreManager: coreauth.NewManager(nil, nil, nil),
	}
	auth := &coreauth.Auth{
		ID:       "codex-auth-2",
		Provider: "codex",
		Status:   coreauth.StatusActive,
	}

	service.ensureExecutorsForAuth(auth)
	firstExecutor, okFirst := service.coreManager.Executor("codex")
	if !okFirst || firstExecutor == nil {
		t.Fatal("expected codex executor after first bind")
	}

	service.ensureExecutorsForAuthWithMode(auth, true)
	secondExecutor, okSecond := service.coreManager.Executor("codex")
	if !okSecond || secondExecutor == nil {
		t.Fatal("expected codex executor after forced rebind")
	}

	if firstExecutor == secondExecutor {
		t.Fatal("expected codex executor replacement in force mode")
	}
}

func TestRebindExecutors_MultipleCodexAuths(t *testing.T) {
	service := &Service{
		cfg:         &config.Config{},
		coreManager: coreauth.NewManager(nil, nil, nil),
	}
	auth1 := &coreauth.Auth{
		ID:       "codex-auth-1",
		Provider: "codex",
		Status:   coreauth.StatusActive,
	}
	auth2 := &coreauth.Auth{
		ID:       "codex-auth-2",
		Provider: "codex",
		Status:   coreauth.StatusActive,
	}

	_, _ = service.coreManager.Register(context.Background(), auth1)
	_, _ = service.coreManager.Register(context.Background(), auth2)

	// Clean up registration on start and end
	modelRegistry := GlobalModelRegistry()
	modelRegistry.UnregisterClient(auth1.ID)
	modelRegistry.UnregisterClient(auth2.ID)
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(auth1.ID)
		modelRegistry.UnregisterClient(auth2.ID)
	})

	service.rebindExecutors()

	// Both active auths should have their models registered (at least GetAvailableModelsByProvider or ClientSupportsModel should reflect this)
	// We can check if GetModelsForClient has registered models for each of them (the registry will register default codex models for each active Codex auth).
	// To query, let's cast/access the underlying registry if it's internalregistry, or just use ClientSupportsModel with a default Codex model.
	// Default pro models include things like "gpt-5.4-mini". Let's check:
	if !modelRegistry.ClientSupportsModel(auth1.ID, "gpt-5.4-mini") {
		t.Error("expected auth1 to support gpt-5.4-mini after rebind")
	}
	if !modelRegistry.ClientSupportsModel(auth2.ID, "gpt-5.4-mini") {
		t.Error("expected auth2 to support gpt-5.4-mini after rebind")
	}
}
