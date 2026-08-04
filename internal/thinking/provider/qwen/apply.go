// Package qwen implements thinking configuration for Qwen (Alibaba Cloud) models.
//
// Qwen models support thinking/reasoning via a feature_config field in the request.
// The thinking level maps to Qwen's internal thinking level scale.
package qwen

import (
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Applier implements thinking.ProviderApplier for Qwen models.
//
// Qwen-specific behavior:
//   - Uses feature_config.thinking.enabled for enable/disable
//   - Uses feature_config.thinking.level for thinking intensity (0-5)
//   - Supports reasoning_effort as an alternative OpenAI-compatible field
type Applier struct{}

var _ thinking.ProviderApplier = (*Applier)(nil)

// NewApplier creates a new Qwen thinking applier.
func NewApplier() *Applier {
	return &Applier{}
}

func init() {
	thinking.RegisterProvider("qwen", NewApplier())
}

// Apply applies thinking configuration to Qwen request body.
//
// Expected output format (enabled):
//
//	{
//	  "feature_config": {
//	    "thinking": {
//	      "enabled": true,
//	      "level": 3
//	    }
//	  }
//	}
//
// Expected output format (disabled):
//
//	{
//	  "feature_config": {
//	    "thinking": {
//	      "enabled": false
//	    }
//	  }
//	}
func (a *Applier) Apply(body []byte, config thinking.ThinkingConfig, modelInfo *registry.ModelInfo) ([]byte, error) {
	if thinking.IsUserDefinedModel(modelInfo) {
		return applyCompatibleQwen(body, config)
	}
	if modelInfo.Thinking == nil {
		return body, nil
	}

	if len(body) == 0 || !gjson.ValidBytes(body) {
		body = []byte(`{}`)
	}

	switch config.Mode {
	case thinking.ModeNone:
		return applyThinkingDisabled(body)
	case thinking.ModeAuto:
		return applyThinkingLevel(body, 3) // Medium level for auto
	case thinking.ModeLevel:
		level := levelToQwenLevel(config.Level)
		return applyThinkingLevel(body, level)
	case thinking.ModeBudget:
		level := budgetToQwenLevel(config.Budget)
		return applyThinkingLevel(body, level)
	default:
		return body, nil
	}
}

// applyCompatibleQwen applies thinking config for user-defined Qwen models.
func applyCompatibleQwen(body []byte, config thinking.ThinkingConfig) ([]byte, error) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		body = []byte(`{}`)
	}

	switch config.Mode {
	case thinking.ModeNone:
		return applyThinkingDisabled(body)
	case thinking.ModeAuto:
		return applyThinkingLevel(body, 3)
	case thinking.ModeLevel:
		level := levelToQwenLevel(config.Level)
		return applyThinkingLevel(body, level)
	case thinking.ModeBudget:
		level := budgetToQwenLevel(config.Budget)
		return applyThinkingLevel(body, level)
	default:
		return body, nil
	}
}

// applyThinkingDisabled disables thinking in the Qwen request body.
func applyThinkingDisabled(body []byte) ([]byte, error) {
	result, err := sjson.SetBytes(body, "feature_config.thinking.enabled", false)
	if err != nil {
		return body, fmt.Errorf("qwen thinking: failed to set thinking disabled: %w", err)
	}
	// Remove level when disabled
	result, err = sjson.DeleteBytes(result, "feature_config.thinking.level")
	if err != nil {
		return result, nil // Non-critical, return what we have
	}
	// Also remove reasoning_effort if present
	result, err = sjson.DeleteBytes(result, "reasoning_effort")
	if err != nil {
		return result, nil
	}
	return result, nil
}

// applyThinkingLevel sets the thinking level in the Qwen request body.
func applyThinkingLevel(body []byte, level int) ([]byte, error) {
	result, err := sjson.SetBytes(body, "feature_config.thinking.enabled", true)
	if err != nil {
		return body, fmt.Errorf("qwen thinking: failed to set thinking enabled: %w", err)
	}
	result, err = sjson.SetBytes(result, "feature_config.thinking.level", level)
	if err != nil {
		return body, fmt.Errorf("qwen thinking: failed to set thinking level: %w", err)
	}
	// Remove reasoning_effort when using feature_config
	result, _ = sjson.DeleteBytes(result, "reasoning_effort")
	return result, nil
}

// levelToQwenLevel maps a thinking level to Qwen's numeric thinking level (0-5).
func levelToQwenLevel(level thinking.ThinkingLevel) int {
	switch level {
	case thinking.LevelNone:
		return 0
	case thinking.LevelMinimal:
		return 1
	case thinking.LevelLow:
		return 2
	case thinking.LevelMedium:
		return 3
	case thinking.LevelHigh:
		return 4
	case thinking.LevelXHigh:
		return 5
	case thinking.LevelMax:
		return 5
	case thinking.LevelAuto:
		return 3 // Default to medium for auto
	default:
		return 3
	}
}

// budgetToQwenLevel converts a token budget to Qwen's thinking level.
func budgetToQwenLevel(budget int) int {
	// Map budget ranges to Qwen levels
	switch {
	case budget <= 0:
		return 0
	case budget <= 1024:
		return 1
	case budget <= 4096:
		return 2
	case budget <= 8192:
		return 3
	case budget <= 16384:
		return 4
	default:
		return 5
	}
}
