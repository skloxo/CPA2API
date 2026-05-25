package registry

import "testing"

func TestQwenStaticModelsLookup(t *testing.T) {
	models := GetQwenModels()
	if len(models) == 0 {
		t.Fatal("expected GetQwenModels to return at least one model definition")
	}
	firstModel := models[0]
	lookup := LookupStaticModelInfo(firstModel.ID)
	if lookup == nil {
		t.Fatalf("expected LookupStaticModelInfo to find %s", firstModel.ID)
	}
	if lookup.ID != firstModel.ID {
		t.Fatalf("model ID mismatch: got %q, want %q", lookup.ID, firstModel.ID)
	}

	channelModels := GetStaticModelDefinitionsByChannel("qwen")
	if len(channelModels) == 0 {
		t.Fatal("expected GetStaticModelDefinitionsByChannel('qwen') to return models")
	}
}
