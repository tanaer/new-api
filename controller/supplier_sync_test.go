package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func TestAutoMapLocalGroup_ReassignsToNearestExistingCategory(t *testing.T) {
	localGroups := []LocalGroupInfo{
		{Name: "cc低配", Ratio: 1.20, Category: "cc"},
		{Name: "cc逆向", Ratio: 1.55, Category: "cc"},
		{Name: "codex", Ratio: 2.00, Category: "codex"},
	}

	mapped := autoMapLocalGroup("Claudecode-Max", 1.52, localGroups)
	require.Equal(t, "cc逆向", mapped)
}

func TestBuildSupplierChannelTargets_SplitsMixedVendorGroup(t *testing.T) {
	supplier := &model.Supplier{Id: 1, Name: "monking", BaseURL: "https://example.com"}
	groups := []*model.SupplierGroup{{
		Id:              11,
		SupplierID:      1,
		UpstreamGroup:   "混合分组",
		LocalGroup:      "",
		GroupRatio:      1.5,
		SupportedModels: "claude-3-7-sonnet,gpt-4o,gpt-4.1",
		ApiKey:          "sk-test",
		EndpointType:    "openai",
	}}
	localGroups := []LocalGroupInfo{
		{Name: "cc主力", Ratio: 1.45, Category: "cc"},
		{Name: "codex主力", Ratio: 1.48, Category: "codex"},
	}
	localGroupSet := map[string]struct{}{
		"cc主力":    {},
		"codex主力": {},
	}
	resolver := &supplierModelResolver{
		exactCategoryByModel: map[string]string{
			"claude-3-7-sonnet": "cc",
			"gpt-4o":            "codex",
			"gpt-4.1":           "codex",
		},
		customModelsByCategory: map[string][]string{
			"cc":    {"claude-code"},
			"codex": {"o1-preview"},
		},
	}

	targets, countByGroupID, warnings := buildSupplierChannelTargets(supplier, groups, &localGroups, localGroupSet, resolver)
	require.Empty(t, warnings)
	require.Equal(t, 2, countByGroupID[11])
	require.Len(t, targets, 2)

	ccTarget, ok := targets[buildSupplierChannelSyncKey("混合分组", "cc主力")]
	require.True(t, ok)
	require.Equal(t, []string{"claude-3-7-sonnet", "claude-code"}, ccTarget.Models)
	require.Equal(t, "monking-混合分组-cc主力", ccTarget.Name)

	codexTarget, ok := targets[buildSupplierChannelSyncKey("混合分组", "codex主力")]
	require.True(t, ok)
	require.Equal(t, []string{"gpt-4o", "gpt-4.1", "o1-preview"}, codexTarget.Models)
	require.Equal(t, "monking-混合分组-codex主力", codexTarget.Name)
}

func TestInferChannelTypeByModels(t *testing.T) {
	resolver := &supplierModelResolver{
		rules: []supplierProviderRule{
			{Category: "cc", ChannelType: 14, Patterns: []string{"claude"}},
			{Category: "gemini", ChannelType: 24, Patterns: []string{"gemini"}},
		},
		exactCategoryByModel: map[string]string{
			"claude-3-7-sonnet": "cc",
			"gemini-2.5-pro":    "gemini",
		},
		customModelsByCategory: map[string][]string{},
	}

	require.Equal(t, 14, inferChannelTypeByModels([]string{"claude-3-7-sonnet"}, resolver, 1))
	require.Equal(t, 24, inferChannelTypeByModels([]string{"gemini-2.5-pro"}, resolver, 1))
	require.Equal(t, 1, inferChannelTypeByModels([]string{"unknown-model"}, resolver, 1))
}

func TestDetectSupplierCategoryWithCustomRules(t *testing.T) {
	rules := []supplierProviderRule{
		{Category: "cc", VendorName: "Anthropic", ChannelType: 14, Patterns: []string{"claude", "claude code"}},
		{Category: "gemini", VendorName: "Google", ChannelType: 24, Patterns: []string{"gemini"}},
	}

	require.Equal(t, "cc", detectSupplierCategoryWithRules(rules, "Claude Code Pro"))
	require.Equal(t, "gemini", detectSupplierCategoryWithRules(rules, "gemini-2.5-pro"))
	require.Equal(t, "", detectSupplierCategoryWithRules(rules, "unknown-model"))
}

func TestLoadSupplierModelResolverWithRules(t *testing.T) {
	rules := []supplierProviderRule{
		{Category: "cc", VendorName: "Anthropic", ChannelType: 14, Patterns: []string{"claude"}},
	}
	resolver := loadSupplierModelResolverWithRules(rules)
	require.NotNil(t, resolver)
	require.Equal(t, "cc", resolver.DetectCategory("claude-3-7-sonnet"))
}

func TestMergeSupplierProviderRules_FillsMissingChannelTypeFromDefaults(t *testing.T) {
	rules := mergeSupplierProviderRules([]supplierProviderRule{{
		Category:   "cc",
		VendorName: "Anthropic Claude",
		Patterns:   []string{"claude", "claude code"},
	}})

	rule, ok := getSupplierProviderRuleByCategory("cc", rules)
	require.True(t, ok)
	require.Equal(t, constant.ChannelTypeAnthropic, rule.ChannelType)
	require.Equal(t, "Anthropic Claude", rule.VendorName)

	geminiRule, ok := getSupplierProviderRuleByCategory("gemini", rules)
	require.True(t, ok)
	require.Equal(t, constant.ChannelTypeGemini, geminiRule.ChannelType)
}

func TestSyncSupplierModelPricing_MergesUpstreamPricing(t *testing.T) {
	modelRatios := ratio_setting.GetModelRatioCopy()
	completionRatios := ratio_setting.GetCompletionRatioCopy()
	modelPrices := ratio_setting.GetModelPriceCopy()
	cacheRatios := ratio_setting.GetCacheRatioCopy()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelRatioByJSONString(marshalMapForTest(t, modelRatios))
		_ = ratio_setting.UpdateCompletionRatioByJSONString(marshalMapForTest(t, completionRatios))
		_ = ratio_setting.UpdateModelPriceByJSONString(marshalMapForTest(t, modelPrices))
		_ = ratio_setting.UpdateCacheRatioByJSONString(marshalMapForTest(t, cacheRatios))
	})

	info := &UpstreamPricingInfo{
		ModelRatios: map[string]float64{
			"claude-3-7-sonnet": 1.5,
			"unknown-fallback":  37.5,
		},
		CompletionRatios: map[string]float64{
			"claude-3-7-sonnet": 5,
			"unknown-fallback":  5,
		},
		ModelPrices: map[string]float64{
			"sora-2": 0.3,
		},
		CacheRatios: map[string]float64{
			"claude-3-7-sonnet": 0.1,
		},
	}
	targets := map[string]supplierChannelTarget{
		"a": {Models: []string{"claude-3-7-sonnet", "sora-2", "unknown-fallback"}},
	}

	_ = ratio_setting.UpdateModelRatioByJSONString(`{}`)
	_ = ratio_setting.UpdateCompletionRatioByJSONString(`{}`)
	_ = ratio_setting.UpdateModelPriceByJSONString(`{}`)
	_ = ratio_setting.UpdateCacheRatioByJSONString(`{}`)

	synced, warnings := syncSupplierModelPricing(info, targets)
	require.Equal(t, 1, synced)
	require.NotEmpty(t, warnings)

	ratio, ok, _ := ratio_setting.GetModelRatio("claude-3-7-sonnet")
	require.True(t, ok)
	require.Equal(t, 1.5, ratio)
	require.Equal(t, 5.0, ratio_setting.GetCompletionRatio("claude-3-7-sonnet"))

	price, ok := ratio_setting.GetModelPrice("sora-2", false)
	require.True(t, ok)
	require.Equal(t, 0.3, price)
}

func TestSyncSupplierModelPricing_PreservesLocalDefaultPricing(t *testing.T) {
	modelRatios := ratio_setting.GetModelRatioCopy()
	completionRatios := ratio_setting.GetCompletionRatioCopy()
	modelPrices := ratio_setting.GetModelPriceCopy()
	cacheRatios := ratio_setting.GetCacheRatioCopy()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelRatioByJSONString(marshalMapForTest(t, modelRatios))
		_ = ratio_setting.UpdateCompletionRatioByJSONString(marshalMapForTest(t, completionRatios))
		_ = ratio_setting.UpdateModelPriceByJSONString(marshalMapForTest(t, modelPrices))
		_ = ratio_setting.UpdateCacheRatioByJSONString(marshalMapForTest(t, cacheRatios))
	})

	_ = ratio_setting.UpdateModelRatioByJSONString(`{"glm-4":50}`)
	_ = ratio_setting.UpdateCompletionRatioByJSONString(`{"glm-4":1}`)

	info := &UpstreamPricingInfo{
		ModelRatios: map[string]float64{
			"glm-4": 50,
		},
		CompletionRatios: map[string]float64{
			"glm-4": 1,
		},
	}
	targets := map[string]supplierChannelTarget{
		"a": {Models: []string{"glm-4"}},
	}

	synced, warnings := syncSupplierModelPricing(info, targets)
	require.Equal(t, 0, synced)
	require.Empty(t, warnings)

	ratio, ok, _ := ratio_setting.GetConfiguredModelRatio("glm-4")
	require.True(t, ok)
	require.Equal(t, 7.143, ratio)
	require.Equal(t, 1.0, ratio_setting.GetCompletionRatio("glm-4"))
}

func TestSyncSupplierModelPricing_SkipsUnknownHighCostPricing(t *testing.T) {
	modelRatios := ratio_setting.GetModelRatioCopy()
	completionRatios := ratio_setting.GetCompletionRatioCopy()
	modelPrices := ratio_setting.GetModelPriceCopy()
	cacheRatios := ratio_setting.GetCacheRatioCopy()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelRatioByJSONString(marshalMapForTest(t, modelRatios))
		_ = ratio_setting.UpdateCompletionRatioByJSONString(marshalMapForTest(t, completionRatios))
		_ = ratio_setting.UpdateModelPriceByJSONString(marshalMapForTest(t, modelPrices))
		_ = ratio_setting.UpdateCacheRatioByJSONString(marshalMapForTest(t, cacheRatios))
	})

	_ = ratio_setting.UpdateModelRatioByJSONString(`{"mystery-audio":88}`)
	_ = ratio_setting.UpdateCompletionRatioByJSONString(`{"mystery-audio":1}`)

	info := &UpstreamPricingInfo{
		ModelRatios: map[string]float64{
			"mystery-audio": 175,
		},
		CompletionRatios: map[string]float64{
			"mystery-audio": 0,
		},
	}
	targets := map[string]supplierChannelTarget{
		"a": {Models: []string{"mystery-audio"}},
	}

	synced, warnings := syncSupplierModelPricing(info, targets)
	require.Equal(t, 0, synced)
	require.NotEmpty(t, warnings)

	_, ok, _ := ratio_setting.GetConfiguredModelRatio("mystery-audio")
	require.False(t, ok)
	require.Equal(t, 1.0, ratio_setting.GetCompletionRatio("mystery-audio"))
}

func marshalMapForTest(t *testing.T, values map[string]float64) string {
	t.Helper()
	jsonBytes, err := common.Marshal(values)
	require.NoError(t, err)
	return string(jsonBytes)
}
