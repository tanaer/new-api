package ratio_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func TestFormatMatchingModelName_ClaudeThinkingAndEffort(t *testing.T) {
	cases := map[string]string{
		"claude-sonnet-4-6-thinking": "claude-sonnet-4-6",
		"claude-sonnet-4-6-high":     "claude-sonnet-4-6",
		"claude-opus-4-6-low":        "claude-opus-4-6",
	}

	for input, expected := range cases {
		if got := FormatMatchingModelName(input); got != expected {
			t.Fatalf("FormatMatchingModelName(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestGetModelRatio_ClaudeSonnet46ThinkingUsesBaseRatio(t *testing.T) {
	InitRatioSettings()

	ratio, ok, matched := GetModelRatio("claude-sonnet-4-6-thinking")
	if !ok {
		t.Fatalf("expected ratio to exist")
	}
	if ratio != 1.5 {
		t.Fatalf("GetModelRatio returned %v, want 1.5", ratio)
	}
	if matched != "claude-sonnet-4-6" {
		t.Fatalf("matched model = %q, want %q", matched, "claude-sonnet-4-6")
	}
}

func TestGetModelRatio_FallsBackToDefaultAfterCustomOverride(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)

	if err := UpdateModelRatioByJSONString(`{"gpt-4o":1.25}`); err != nil {
		t.Fatalf("UpdateModelRatioByJSONString error: %v", err)
	}

	ratio, ok, matched := GetModelRatio("claude-sonnet-4-6-thinking")
	if !ok {
		t.Fatalf("expected ratio to fallback to default")
	}
	if ratio != 1.5 {
		t.Fatalf("GetModelRatio returned %v, want 1.5", ratio)
	}
	if matched != "claude-sonnet-4-6" {
		t.Fatalf("matched model = %q, want %q", matched, "claude-sonnet-4-6")
	}
}

func TestGetModelPrice_FallsBackToDefaultAfterCustomOverride(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)

	if err := UpdateModelPriceByJSONString(`{}`); err != nil {
		t.Fatalf("UpdateModelPriceByJSONString error: %v", err)
	}

	price, ok := GetModelPrice("sora-2", false)
	if !ok {
		t.Fatalf("expected price to fallback to default")
	}
	if price != 0.3 {
		t.Fatalf("GetModelPrice returned %v, want 0.3", price)
	}
}

func TestGetCompletionRatio_FallsBackToDefaultAfterCustomOverride(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)

	if err := UpdateCompletionRatioByJSONString(`{}`); err != nil {
		t.Fatalf("UpdateCompletionRatioByJSONString error: %v", err)
	}

	ratio := GetCompletionRatio("gpt-image-1")
	if ratio != 8 {
		t.Fatalf("GetCompletionRatio returned %v, want 8", ratio)
	}
}

func TestNormalizeModelRatioOptionJSON_StripsDefaults(t *testing.T) {
	jsonStr, err := NormalizeModelRatioOptionJSON(`{"gpt-4o":1.25,"custom-model":9.9}`)
	if err != nil {
		t.Fatalf("NormalizeModelRatioOptionJSON error: %v", err)
	}
	values := make(map[string]float64)
	if err := common.Unmarshal([]byte(jsonStr), &values); err != nil {
		t.Fatalf("unmarshal normalized json error: %v", err)
	}
	if _, exists := values["gpt-4o"]; exists {
		t.Fatalf("expected default-valued key to be stripped")
	}
	if values["custom-model"] != 9.9 {
		t.Fatalf("expected custom-model to remain, got %v", values["custom-model"])
	}
}
