package controller

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

const (
	upstreamRequestTimeout      = 30 * time.Second
	upstreamTokenListPageSize   = 100
	upstreamTokenListMaxPages   = 5
	upstream429MaxAttempts      = 3
	upstream429BaseDelay        = 800 * time.Millisecond
	upstreamTokenCreateInterval = 150 * time.Millisecond
)

type upstreamAuthInfo struct {
	authorization string
	cookie        string
	userID        int
}

type upstreamLoginResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Id int `json:"id"`
	} `json:"data"`
}

type upstreamTokenItem struct {
	Id    int    `json:"id"`
	Name  string `json:"name"`
	Key   string `json:"key"`
	Group string `json:"group"`
}

type upstreamTokenListResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Page     int                 `json:"page"`
		PageSize int                 `json:"page_size"`
		Total    int                 `json:"total"`
		Items    []upstreamTokenItem `json:"items"`
	} `json:"data"`
}

type supplierBalanceInfo struct {
	UpstreamUserID int                    `json:"upstream_user_id"`
	Username       string                 `json:"username,omitempty"`
	DisplayName    string                 `json:"display_name,omitempty"`
	RemainingQuota float64                `json:"remaining_quota"`
	UsedQuota      *float64               `json:"used_quota,omitempty"`
	TotalQuota     *float64               `json:"total_quota,omitempty"`
	Raw            map[string]interface{} `json:"raw,omitempty"`
}

func buildCookieHeader(cookies []*http.Cookie) string {
	if len(cookies) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if c == nil || c.Name == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", c.Name, c.Value))
	}
	return strings.Join(parts, "; ")
}

func normalizeBearerToken(raw string) string {
	token := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	return token
}

func parseUpstreamMessage(body []byte) string {
	var payload map[string]interface{}
	if err := common.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if msg, ok := payload["message"].(string); ok {
		return msg
	}
	return ""
}

func getStringFromMap(m map[string]interface{}, key string) (string, bool) {
	value, exists := m[key]
	if !exists {
		return "", false
	}
	str, ok := value.(string)
	if !ok {
		return "", false
	}
	str = strings.TrimSpace(str)
	if str == "" {
		return "", false
	}
	return str, true
}

func getFloatFromAny(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func getFloatFromMap(m map[string]interface{}, keys ...string) (float64, bool) {
	for _, key := range keys {
		value, exists := m[key]
		if !exists {
			continue
		}
		if parsed, ok := getFloatFromAny(value); ok {
			return parsed, true
		}
	}
	return 0, false
}

func parseRetryAfterSeconds(headerValue string) int {
	raw := strings.TrimSpace(headerValue)
	if raw == "" {
		return 0
	}
	seconds, err := strconv.Atoi(raw)
	if err == nil && seconds > 0 {
		return seconds
	}
	return 0
}

func parseUpstreamMessageAndRetryAfter(body []byte, headers http.Header) (string, int) {
	msg := parseUpstreamMessage(body)
	retryAfter := 0
	if headers != nil {
		retryAfter = parseRetryAfterSeconds(headers.Get("Retry-After"))
	}

	var payload map[string]interface{}
	if err := common.Unmarshal(body, &payload); err == nil {
		if retryAfter == 0 {
			if value, ok := payload["retry_after"]; ok {
				switch typed := value.(type) {
				case float64:
					if typed > 0 {
						retryAfter = int(typed)
					}
				case string:
					retryAfter = parseRetryAfterSeconds(typed)
				}
			}
		}
		if msg == "" {
			if value, ok := payload["error"].(string); ok {
				msg = strings.TrimSpace(value)
			}
		}
		if msg == "" {
			if value, ok := payload["msg"].(string); ok {
				msg = strings.TrimSpace(value)
			}
		}
	}
	return msg, retryAfter
}

func waitBeforeRetry(attempt int, retryAfterSeconds int) {
	if attempt >= upstream429MaxAttempts {
		return
	}
	if retryAfterSeconds > 0 {
		time.Sleep(time.Duration(retryAfterSeconds) * time.Second)
		return
	}
	delay := upstream429BaseDelay * time.Duration(attempt)
	time.Sleep(delay)
}

func doUpstreamRequestWith429Retry(client *http.Client, req *http.Request, opName string) ([]byte, int, error) {
	for attempt := 1; attempt <= upstream429MaxAttempts; attempt++ {
		clonedReq := req.Clone(req.Context())
		resp, err := client.Do(clonedReq)
		if err != nil {
			return nil, 0, fmt.Errorf("%s失败: %v", opName, err)
		}

		body, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			return nil, 0, fmt.Errorf("读取%s响应失败: %v", opName, readErr)
		}
		if closeErr != nil {
			return nil, 0, fmt.Errorf("关闭%s响应失败: %v", opName, closeErr)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			msg, retryAfter := parseUpstreamMessageAndRetryAfter(body, resp.Header)
			if msg == "" {
				msg = "HTTP 429"
			}
			waitBeforeRetry(attempt, retryAfter)
			if attempt == upstream429MaxAttempts {
				return nil, resp.StatusCode, fmt.Errorf("%s响应异常: %s", opName, msg)
			}
			continue
		}

		return body, resp.StatusCode, nil
	}
	return nil, 0, fmt.Errorf("%s失败: 重试次数耗尽", opName)
}

func loginUpstreamWithPassword(client *http.Client, supplier *model.Supplier) (*upstreamAuthInfo, error) {
	loginURL := supplier.BaseURL + "/api/user/login"
	payload := map[string]string{
		"username": strings.TrimSpace(supplier.Username),
		"password": supplier.Password,
	}
	bodyBytes, err := common.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("构造登录请求失败: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, loginURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建登录请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("上游登录请求失败: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取登录响应失败: %v", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := parseUpstreamMessage(respBody)
		if msg == "" {
			msg = string(respBody)
		}
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("上游登录失败: %s", msg)
	}

	var loginResp upstreamLoginResponse
	if err := common.Unmarshal(respBody, &loginResp); err != nil {
		return nil, fmt.Errorf("解析上游登录响应失败: %v", err)
	}
	if !loginResp.Success {
		msg := strings.TrimSpace(loginResp.Message)
		if msg == "" {
			msg = "上游登录返回失败"
		}
		return nil, fmt.Errorf("上游登录失败: %s", msg)
	}
	if loginResp.Data.Id <= 0 {
		return nil, fmt.Errorf("上游登录成功但未返回有效用户ID")
	}

	cookieHeader := buildCookieHeader(resp.Cookies())
	if cookieHeader == "" {
		return nil, fmt.Errorf("上游登录成功但未返回会话Cookie")
	}

	return &upstreamAuthInfo{
		cookie: cookieHeader,
		userID: loginResp.Data.Id,
	}, nil
}

func persistUpstreamUserID(supplier *model.Supplier, userID int) {
	if userID <= 0 {
		return
	}
	if supplier.UpstreamUserID == userID {
		return
	}
	supplier.UpstreamUserID = userID
	if err := model.UpdateSupplier(supplier); err != nil {
		common.SysLog(fmt.Sprintf("failed to persist upstream user id: supplier_id=%d, user_id=%d, error=%v", supplier.Id, userID, err))
	}
}

func buildUpstreamAuthForSupplier(client *http.Client, supplier *model.Supplier) (*upstreamAuthInfo, error) {
	var loginErr error

	if strings.TrimSpace(supplier.Username) != "" && strings.TrimSpace(supplier.Password) != "" {
		auth, err := loginUpstreamWithPassword(client, supplier)
		if err == nil {
			persistUpstreamUserID(supplier, auth.userID)
			return auth, nil
		}
		loginErr = err
	}

	rawCredential := strings.TrimSpace(supplier.Cookie)
	if rawCredential != "" {
		auth := &upstreamAuthInfo{userID: supplier.UpstreamUserID}
		if strings.HasPrefix(rawCredential, "sk-") || strings.HasPrefix(strings.ToLower(rawCredential), "bearer ") {
			token := normalizeBearerToken(rawCredential)
			if token == "" {
				return nil, fmt.Errorf("上游 AccessToken 为空")
			}
			auth.authorization = "Bearer " + token
		} else {
			auth.cookie = rawCredential
		}

		if auth.userID <= 0 {
			if loginErr != nil {
				return nil, fmt.Errorf("自动登录失败（%v），且未配置上游用户ID", loginErr)
			}
			return nil, fmt.Errorf("未配置上游用户ID，无法通过上游 New-Api-User 校验")
		}
		return auth, nil
	}

	if loginErr != nil {
		return nil, fmt.Errorf("自动登录失败: %v", loginErr)
	}
	return nil, fmt.Errorf("未配置凭证信息（Cookie/AccessToken 或 上游账号密码）")
}

func applyUpstreamAuthHeaders(req *http.Request, auth *upstreamAuthInfo) {
	if auth == nil {
		return
	}
	if auth.authorization != "" {
		req.Header.Set("Authorization", auth.authorization)
	}
	if auth.cookie != "" {
		req.Header.Set("Cookie", auth.cookie)
	}
	req.Header.Set("New-Api-User", strconv.Itoa(auth.userID))
}

// UpstreamPricingInfo 上游定价信息
type UpstreamPricingInfo struct {
	Groups             map[string]float64  // 分组名 -> 倍率
	GroupModels        map[string][]string // 分组名 -> 支持的模型列表
	GroupEndpoint      map[string]string   // 分组名 -> 默认端点类型
	GroupEndpointTypes map[string][]string // 分组名 -> 支持的端点类型列表
	UsableGroup        map[string]string   // 分组名 -> 分组显示名
	AllModels          []string            // 所有模型列表
	ModelRatios        map[string]float64  // 模型倍率
	CompletionRatios   map[string]float64  // 补全倍率
	ModelPrices        map[string]float64  // 固定价格
	CacheRatios        map[string]float64  // 缓存倍率
}

type supplierModelRule struct {
	Pattern  string
	Category string
}

type supplierModelResolver struct {
	rules                  []supplierProviderRule
	exactCategoryByModel   map[string]string
	prefixRules            []supplierModelRule
	suffixRules            []supplierModelRule
	containsRules          []supplierModelRule
	customModelsByCategory map[string][]string
}

type supplierChannelTarget struct {
	SyncKey       string
	Name          string
	UpstreamGroup string
	LocalGroup    string
	Models        []string
	ApiKey        string
	ChannelType   int
	GroupRatio    float64
	EndpointType  string
	SourceGroup   *model.SupplierGroup
}

type supplierExistingChannelRef struct {
	Channel *model.Channel
	Matched bool
}

type supplierProviderRule struct {
	Category    string   `json:"category"`
	VendorName  string   `json:"vendor_name"`
	ChannelType int      `json:"channel_type"`
	Patterns    []string `json:"patterns"`
}

const (
	supplierChannelSyncKeyField       = "supplier_sync_key"
	supplierChannelUpstreamGroupField = "supplier_upstream_group"
	supplierChannelLocalGroupField    = "supplier_local_group"
	supplierProviderRulesOptionKey    = "SupplierProviderRules"
)

var defaultSupplierProviderRules = []supplierProviderRule{
	{Category: "cc", VendorName: "Anthropic", ChannelType: constant.ChannelTypeAnthropic, Patterns: []string{"claudecode", "claude code", "anthropic", "claude", "cc"}},
	{Category: "sora", VendorName: "OpenAI", ChannelType: constant.ChannelTypeSora, Patterns: []string{"sora"}},
	{Category: "codex", VendorName: "OpenAI", ChannelType: constant.ChannelTypeOpenAI, Patterns: []string{"chatgpt", "openai", "codex", "gpt", "o1", "o3", "o4", "whisper", "dall-e", "omni-moderation", "text-embedding"}},
	{Category: "gemini", VendorName: "Google", ChannelType: constant.ChannelTypeGemini, Patterns: []string{"google", "gemini"}},
	{Category: "pplx", VendorName: "Perplexity", ChannelType: constant.ChannelTypePerplexity, Patterns: []string{"perplexity", "pplx", "sonar"}},
	{Category: "deepseek", VendorName: "DeepSeek", ChannelType: constant.ChannelTypeDeepSeek, Patterns: []string{"deepseek"}},
	{Category: "qwen", VendorName: "阿里巴巴", ChannelType: constant.ChannelTypeAli, Patterns: []string{"tongyi", "qwen", "alibaba", "aliyun", "阿里"}},
	{Category: "moonshot", VendorName: "Moonshot", ChannelType: constant.ChannelTypeMoonshot, Patterns: []string{"moonshot", "kimi"}},
	{Category: "glm", VendorName: "智谱", ChannelType: constant.ChannelTypeZhipu, Patterns: []string{"chatglm", "zhipu", "glm", "智谱"}},
	{Category: "doubao", VendorName: "字节跳动", ChannelType: constant.ChannelTypeVolcEngine, Patterns: []string{"doubao", "字节", "bytedance"}},
	{Category: "hunyuan", VendorName: "腾讯", ChannelType: constant.ChannelTypeTencent, Patterns: []string{"hunyuan", "腾讯", "tencent"}},
	{Category: "ernie", VendorName: "百度", ChannelType: constant.ChannelTypeBaidu, Patterns: []string{"ernie", "wenxin", "百度", "baidu"}},
	{Category: "spark", VendorName: "讯飞", ChannelType: constant.ChannelTypeXunfei, Patterns: []string{"spark", "讯飞", "xfyun"}},
	{Category: "minimax", VendorName: "MiniMax", ChannelType: constant.ChannelTypeMiniMax, Patterns: []string{"minimax", "abab"}},
	{Category: "cohere", VendorName: "Cohere", ChannelType: constant.ChannelTypeCohere, Patterns: []string{"cohere", "command"}},
	{Category: "grok", VendorName: "xAI", ChannelType: constant.ChannelTypeXai, Patterns: []string{"grok", "xai", "x.ai"}},
	{Category: "llama", VendorName: "Meta", ChannelType: constant.ChannelTypeOpenAI, Patterns: []string{"llama", "meta"}},
	{Category: "mistral", VendorName: "Mistral", ChannelType: constant.ChannelTypeMistral, Patterns: []string{"mistral"}},
	{Category: "jina", VendorName: "Jina", ChannelType: constant.ChannelTypeJina, Patterns: []string{"jina"}},
	{Category: "cloudflare", VendorName: "Cloudflare", ChannelType: constant.ChannelCloudflare, Patterns: []string{"cloudflare", "@cf/"}},
}

func cloneSupplierProviderRules(rules []supplierProviderRule) []supplierProviderRule {
	result := make([]supplierProviderRule, 0, len(rules))
	for _, rule := range rules {
		copied := supplierProviderRule{
			Category:    strings.TrimSpace(rule.Category),
			VendorName:  strings.TrimSpace(rule.VendorName),
			ChannelType: rule.ChannelType,
			Patterns:    make([]string, 0, len(rule.Patterns)),
		}
		seen := make(map[string]struct{})
		for _, pattern := range rule.Patterns {
			pattern = strings.TrimSpace(pattern)
			if pattern == "" {
				continue
			}
			if _, exists := seen[pattern]; exists {
				continue
			}
			seen[pattern] = struct{}{}
			copied.Patterns = append(copied.Patterns, pattern)
		}
		if copied.Category == "" || len(copied.Patterns) == 0 {
			continue
		}
		result = append(result, copied)
	}
	return result
}

func mergeSupplierProviderRules(customRules []supplierProviderRule) []supplierProviderRule {
	defaults := cloneSupplierProviderRules(defaultSupplierProviderRules)
	customRules = cloneSupplierProviderRules(customRules)
	if len(customRules) == 0 {
		return defaults
	}

	defaultByCategory := make(map[string]supplierProviderRule, len(defaults))
	for _, rule := range defaults {
		defaultByCategory[rule.Category] = rule
	}

	merged := make([]supplierProviderRule, 0, len(customRules)+len(defaults))
	seen := make(map[string]struct{}, len(customRules))
	for _, rule := range customRules {
		if defaultRule, exists := defaultByCategory[rule.Category]; exists {
			if strings.TrimSpace(rule.VendorName) == "" {
				rule.VendorName = defaultRule.VendorName
			}
			if rule.ChannelType == 0 {
				rule.ChannelType = defaultRule.ChannelType
			}
			if len(rule.Patterns) == 0 {
				rule.Patterns = append([]string(nil), defaultRule.Patterns...)
			}
		}
		if rule.Category == "" || len(rule.Patterns) == 0 {
			continue
		}
		merged = append(merged, rule)
		seen[rule.Category] = struct{}{}
	}

	for _, rule := range defaults {
		if _, exists := seen[rule.Category]; exists {
			continue
		}
		merged = append(merged, rule)
	}
	return merged
}

func getSupplierProviderRules() []supplierProviderRule {
	defaults := cloneSupplierProviderRules(defaultSupplierProviderRules)
	raw := strings.TrimSpace(common.OptionMap[supplierProviderRulesOptionKey])
	if raw == "" {
		return defaults
	}
	var customRules []supplierProviderRule
	if err := common.UnmarshalJsonStr(raw, &customRules); err != nil {
		return defaults
	}
	return mergeSupplierProviderRules(customRules)
}

func getSupplierProviderRuleByCategory(category string, rules []supplierProviderRule) (supplierProviderRule, bool) {
	category = strings.TrimSpace(category)
	for _, rule := range rules {
		if strings.TrimSpace(rule.Category) == category {
			return rule, true
		}
	}
	return supplierProviderRule{}, false
}

func normalizeComparableName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer("-", "", "_", "", " ", "", "·", "", ".", "", "/", "", "(", "", ")", "")
	return replacer.Replace(value)
}

func detectSupplierCategoryWithRules(rules []supplierProviderRule, values ...string) string {
	for _, value := range values {
		raw := strings.ToLower(strings.TrimSpace(value))
		compact := normalizeComparableName(value)
		if raw == "" && compact == "" {
			continue
		}
		for _, rule := range rules {
			for _, pattern := range rule.Patterns {
				patternLower := strings.ToLower(strings.TrimSpace(pattern))
				patternCompact := normalizeComparableName(patternLower)
				if patternLower != "" && strings.Contains(raw, patternLower) {
					return rule.Category
				}
				if patternCompact != "" && strings.Contains(compact, patternCompact) {
					return rule.Category
				}
			}
		}
	}
	return ""
}

func detectSupplierCategory(values ...string) string {
	return detectSupplierCategoryWithRules(getSupplierProviderRules(), values...)
}

func loadSupplierModelResolverWithRules(rules []supplierProviderRule) *supplierModelResolver {
	rules = cloneSupplierProviderRules(rules)
	resolver := &supplierModelResolver{
		rules:                  rules,
		exactCategoryByModel:   make(map[string]string),
		customModelsByCategory: make(map[string][]string),
	}

	vendorNameByID := make(map[int]string)
	models := make([]model.Model, 0)
	if model.DB != nil {
		var vendors []model.Vendor
		_ = model.DB.Find(&vendors).Error
		vendorNameByID = make(map[int]string, len(vendors))
		for i := range vendors {
			vendorNameByID[vendors[i].Id] = strings.TrimSpace(vendors[i].Name)
		}
		_ = model.DB.Find(&models).Error
	}

	customSetByCategory := make(map[string]map[string]struct{})
	for i := range models {
		meta := models[i]
		modelName := strings.TrimSpace(meta.ModelName)
		if modelName == "" {
			continue
		}

		category := detectSupplierCategoryWithRules(rules, modelName)
		if category == "" {
			category = detectSupplierCategoryWithRules(rules, vendorNameByID[meta.VendorID], modelName)
		}
		if category == "" {
			continue
		}

		rule := supplierModelRule{Pattern: modelName, Category: category}
		switch meta.NameRule {
		case model.NameRulePrefix:
			resolver.prefixRules = append(resolver.prefixRules, rule)
		case model.NameRuleSuffix:
			resolver.suffixRules = append(resolver.suffixRules, rule)
		case model.NameRuleContains:
			resolver.containsRules = append(resolver.containsRules, rule)
		default:
			resolver.exactCategoryByModel[modelName] = category
			if meta.SyncOfficial == 0 {
				if _, exists := customSetByCategory[category]; !exists {
					customSetByCategory[category] = make(map[string]struct{})
				}
				customSetByCategory[category][modelName] = struct{}{}
			}
		}
	}

	for category, modelSet := range customSetByCategory {
		models := make([]string, 0, len(modelSet))
		for modelName := range modelSet {
			models = append(models, modelName)
		}
		sort.Strings(models)
		resolver.customModelsByCategory[category] = models
	}

	return resolver
}

func loadSupplierModelResolver() *supplierModelResolver {
	return loadSupplierModelResolverWithRules(getSupplierProviderRules())
}

func (resolver *supplierModelResolver) DetectCategory(modelName string) string {
	if resolver == nil {
		return detectSupplierCategory(modelName)
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return ""
	}
	if category, exists := resolver.exactCategoryByModel[modelName]; exists {
		return category
	}
	for _, rule := range resolver.prefixRules {
		if strings.HasPrefix(modelName, rule.Pattern) {
			return rule.Category
		}
	}
	for _, rule := range resolver.suffixRules {
		if strings.HasSuffix(modelName, rule.Pattern) {
			return rule.Category
		}
	}
	for _, rule := range resolver.containsRules {
		if strings.Contains(modelName, rule.Pattern) {
			return rule.Category
		}
	}
	return detectSupplierCategoryWithRules(resolver.rules, modelName)
}

func (resolver *supplierModelResolver) CustomModelsForCategory(category string) []string {
	if resolver == nil {
		return nil
	}
	models := resolver.customModelsByCategory[strings.TrimSpace(category)]
	if len(models) == 0 {
		return nil
	}
	result := make([]string, len(models))
	copy(result, models)
	return result
}

func ensureSupplierVendor(vendorName string, vendorIDCache map[string]int) int {
	vendorName = strings.TrimSpace(vendorName)
	if vendorName == "" {
		return 0
	}
	if vendorIDCache != nil {
		if id, exists := vendorIDCache[vendorName]; exists {
			return id
		}
	}
	var existing model.Vendor
	if err := model.DB.Where("name = ?", vendorName).First(&existing).Error; err == nil {
		if vendorIDCache != nil {
			vendorIDCache[vendorName] = existing.Id
		}
		return existing.Id
	}
	vendor := &model.Vendor{
		Name:   vendorName,
		Status: 1,
	}
	if err := vendor.Insert(); err != nil {
		if vendorIDCache != nil {
			vendorIDCache[vendorName] = 0
		}
		return 0
	}
	if vendorIDCache != nil {
		vendorIDCache[vendorName] = vendor.Id
	}
	return vendor.Id
}

func syncSupplierModelsToMeta(targets map[string]supplierChannelTarget, resolver *supplierModelResolver) (int, int, []string) {
	if len(targets) == 0 {
		return 0, 0, nil
	}
	warnings := make([]string, 0)
	created := 0
	updated := 0
	vendorIDCache := make(map[string]int)
	processed := make(map[string]struct{})

	for _, target := range targets {
		for _, modelName := range normalizeModelNames(target.Models) {
			modelCategory := resolver.DetectCategory(modelName)
			if modelCategory == "" {
				modelCategory = detectSupplierCategoryWithRules(resolver.rules, target.LocalGroup, target.UpstreamGroup, modelName)
			}
			rule, hasRule := getSupplierProviderRuleByCategory(modelCategory, resolver.rules)
			vendorName := ""
			if hasRule {
				vendorName = rule.VendorName
			}
			vendorID := ensureSupplierVendor(vendorName, vendorIDCache)

			if _, exists := processed[modelName]; exists {
				continue
			}
			processed[modelName] = struct{}{}

			var existing model.Model
			if err := model.DB.Where("model_name = ?", modelName).First(&existing).Error; err == nil {
				changed := false
				if vendorID > 0 && (existing.VendorID == 0 || (existing.SyncOfficial == 0 && existing.VendorID != vendorID)) {
					existing.VendorID = vendorID
					changed = true
				}
				if existing.Status == 0 {
					existing.Status = 1
					changed = true
				}
				if changed {
					if err := existing.Update(); err != nil {
						warnings = append(warnings, fmt.Sprintf("更新模型 %s 失败: %v", modelName, err))
					} else {
						updated++
					}
				}
				continue
			}

			newModel := &model.Model{
				ModelName:    modelName,
				VendorID:     vendorID,
				Status:       1,
				NameRule:     model.NameRuleExact,
				SyncOfficial: 0,
			}
			if err := newModel.Insert(); err != nil {
				warnings = append(warnings, fmt.Sprintf("创建模型 %s 失败: %v", modelName, err))
				continue
			}
			created++
		}
	}

	if created > 0 || updated > 0 {
		model.RefreshPricing()
	}
	return created, updated, warnings
}

func inferChannelTypeByModels(models []string, resolver *supplierModelResolver, fallback int) int {
	if resolver == nil {
		return fallback
	}
	countByType := make(map[int]int)
	for _, modelName := range normalizeModelNames(models) {
		category := resolver.DetectCategory(modelName)
		rule, exists := getSupplierProviderRuleByCategory(category, resolver.rules)
		if !exists || rule.ChannelType == 0 {
			continue
		}
		countByType[rule.ChannelType]++
	}
	bestType := 0
	bestCount := 0
	for channelType, count := range countByType {
		if count > bestCount {
			bestType = channelType
			bestCount = count
		}
	}
	if bestType != 0 {
		return bestType
	}
	return fallback
}

func findLocalGroupInfo(localGroups []LocalGroupInfo, groupName string) (LocalGroupInfo, bool) {
	groupName = strings.TrimSpace(groupName)
	for _, localGroup := range localGroups {
		if strings.TrimSpace(localGroup.Name) == groupName {
			return localGroup, true
		}
	}
	return LocalGroupInfo{}, false
}

func pickNearestLocalGroup(candidates []LocalGroupInfo, upstreamRatio float64) string {
	var bestMatch string
	minDiff := -1.0
	for _, candidate := range candidates {
		diff := candidate.Ratio - upstreamRatio
		if diff < 0 {
			diff = -diff
		}
		if minDiff < 0 || diff < minDiff {
			minDiff = diff
			bestMatch = candidate.Name
		}
	}
	return bestMatch
}

func ensureSupplierLocalGroup(category string, upstreamRatio float64, localGroups *[]LocalGroupInfo, localGroupSet map[string]struct{}) string {
	category = strings.TrimSpace(category)
	if category == "" {
		return ""
	}
	baseName := category
	if _, exists := localGroupSet[baseName]; !exists {
		*localGroups = append(*localGroups, LocalGroupInfo{Name: baseName, Ratio: roundRatio(upstreamRatio), Category: category})
		localGroupSet[baseName] = struct{}{}
		return baseName
	}
	for index := 1; index < 1000; index++ {
		candidate := fmt.Sprintf("%s-%d", baseName, index)
		if _, exists := localGroupSet[candidate]; exists {
			continue
		}
		*localGroups = append(*localGroups, LocalGroupInfo{Name: candidate, Ratio: roundRatio(upstreamRatio), Category: category})
		localGroupSet[candidate] = struct{}{}
		return candidate
	}
	return ""
}

func resolveSupplierLocalGroup(existingLocalGroup string, upstreamGroup string, upstreamRatio float64, category string, localGroups *[]LocalGroupInfo, localGroupSet map[string]struct{}) string {
	existingLocalGroup = strings.TrimSpace(existingLocalGroup)
	if existingLocalGroup != "" {
		if _, exists := localGroupSet[existingLocalGroup]; exists {
			return existingLocalGroup
		}
	}

	if exact, exists := findLocalGroupInfo(*localGroups, upstreamGroup); exists {
		if category == "" || exact.Category == category {
			return exact.Name
		}
	}

	categories := make([]string, 0, 2)
	if category != "" {
		categories = append(categories, category)
	}
	if upstreamCategory := detectSupplierCategory(upstreamGroup); upstreamCategory != "" && upstreamCategory != category {
		categories = append(categories, upstreamCategory)
	}

	for _, currentCategory := range categories {
		candidates := make([]LocalGroupInfo, 0)
		for _, localGroup := range *localGroups {
			if localGroup.Category == currentCategory {
				candidates = append(candidates, localGroup)
			}
		}
		if len(candidates) > 0 {
			return pickNearestLocalGroup(candidates, upstreamRatio)
		}
		if created := ensureSupplierLocalGroup(currentCategory, upstreamRatio, localGroups, localGroupSet); created != "" {
			return created
		}
	}

	if len(*localGroups) > 0 {
		return pickNearestLocalGroup(*localGroups, upstreamRatio)
	}
	return ""
}

func buildSupplierChannelSyncKey(upstreamGroup string, localGroup string) string {
	return strings.TrimSpace(upstreamGroup) + "||" + strings.TrimSpace(localGroup)
}

func buildSupplierChannelName(supplierName string, upstreamGroup string, localGroup string, multiTarget bool) string {
	base := fmt.Sprintf("%s-%s", strings.TrimSpace(supplierName), strings.TrimSpace(upstreamGroup))
	if !multiTarget || strings.TrimSpace(localGroup) == "" {
		return base
	}
	return fmt.Sprintf("%s-%s", base, strings.TrimSpace(localGroup))
}

func getSupplierChannelSyncKey(channel *model.Channel) string {
	if channel == nil {
		return ""
	}
	otherInfo := channel.GetOtherInfo()
	if value, ok := otherInfo[supplierChannelSyncKeyField].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func stampSupplierChannelOtherInfo(channel *model.Channel, target supplierChannelTarget) {
	if channel == nil {
		return
	}
	otherInfo := channel.GetOtherInfo()
	otherInfo[supplierChannelSyncKeyField] = target.SyncKey
	otherInfo[supplierChannelUpstreamGroupField] = target.UpstreamGroup
	otherInfo[supplierChannelLocalGroupField] = target.LocalGroup
	channel.SetOtherInfo(otherInfo)
}

func buildSupplierChannelTargets(supplier *model.Supplier, groups []*model.SupplierGroup, localGroups *[]LocalGroupInfo, localGroupSet map[string]struct{}, resolver *supplierModelResolver) (map[string]supplierChannelTarget, map[int]int, []string) {
	targets := make(map[string]supplierChannelTarget)
	targetCountByGroupID := make(map[int]int)
	warnings := make([]string, 0)

	for _, group := range groups {
		if group == nil {
			continue
		}

		upstreamModels := normalizeModelNames(strings.Split(strings.TrimSpace(group.SupportedModels), ","))
		modelsByCategory := make(map[string][]string)
		unclassifiedModels := make([]string, 0)
		for _, modelName := range upstreamModels {
			category := resolver.DetectCategory(modelName)
			if category == "" {
				unclassifiedModels = append(unclassifiedModels, modelName)
				continue
			}
			modelsByCategory[category] = append(modelsByCategory[category], modelName)
		}

		categories := make([]string, 0, len(modelsByCategory))
		for category := range modelsByCategory {
			categories = append(categories, category)
		}
		sort.Strings(categories)

		primaryCategory := detectSupplierCategory(group.LocalGroup)
		if primaryCategory == "" && len(categories) == 1 {
			primaryCategory = categories[0]
		}
		if primaryCategory == "" {
			primaryCategory = detectSupplierCategory(group.UpstreamGroup)
		}

		resolvedPrimaryLocalGroup := resolveSupplierLocalGroup(group.LocalGroup, group.UpstreamGroup, group.GroupRatio, primaryCategory, localGroups, localGroupSet)
		if strings.TrimSpace(resolvedPrimaryLocalGroup) != strings.TrimSpace(group.LocalGroup) && strings.TrimSpace(resolvedPrimaryLocalGroup) != "" {
			group.LocalGroup = resolvedPrimaryLocalGroup
		}

		if len(categories) == 0 {
			if resolvedPrimaryLocalGroup == "" {
				continue
			}
			models := upstreamModels
			if primaryCategory != "" {
				models = mergeModelNames(models, resolver.CustomModelsForCategory(primaryCategory))
			}
			target := supplierChannelTarget{
				SyncKey:       buildSupplierChannelSyncKey(group.UpstreamGroup, resolvedPrimaryLocalGroup),
				Name:          buildSupplierChannelName(supplier.Name, group.UpstreamGroup, resolvedPrimaryLocalGroup, false),
				UpstreamGroup: group.UpstreamGroup,
				LocalGroup:    resolvedPrimaryLocalGroup,
				Models:        normalizeModelNames(models),
				ApiKey:        strings.TrimSpace(group.ApiKey),
				ChannelType:   inferChannelTypeByModels(models, resolver, getChannelTypeByEndpointType(getSupplierGroupPreferredEndpointType(group))),
				GroupRatio:    group.GroupRatio,
				EndpointType:  getSupplierGroupPreferredEndpointType(group),
				SourceGroup:   group,
			}
			targets[target.SyncKey] = target
			targetCountByGroupID[group.Id]++
			continue
		}

		for _, category := range categories {
			localGroup := resolvedPrimaryLocalGroup
			if len(categories) > 1 || localGroup == "" || detectSupplierCategory(localGroup) != category {
				localGroup = resolveSupplierLocalGroup("", group.UpstreamGroup, group.GroupRatio, category, localGroups, localGroupSet)
			}
			if localGroup == "" {
				continue
			}

			models := mergeModelNames(modelsByCategory[category], resolver.CustomModelsForCategory(category))
			if localGroup == resolvedPrimaryLocalGroup && len(unclassifiedModels) > 0 {
				models = mergeModelNames(models, unclassifiedModels)
			}

			target := supplierChannelTarget{
				SyncKey:       buildSupplierChannelSyncKey(group.UpstreamGroup, localGroup),
				Name:          buildSupplierChannelName(supplier.Name, group.UpstreamGroup, localGroup, len(categories) > 1),
				UpstreamGroup: group.UpstreamGroup,
				LocalGroup:    localGroup,
				Models:        normalizeModelNames(models),
				ApiKey:        strings.TrimSpace(group.ApiKey),
				ChannelType:   inferChannelTypeByModels(models, resolver, getChannelTypeByEndpointType(getSupplierGroupPreferredEndpointType(group))),
				GroupRatio:    group.GroupRatio,
				EndpointType:  getSupplierGroupPreferredEndpointType(group),
				SourceGroup:   group,
			}

			if existing, exists := targets[target.SyncKey]; exists {
				existing.Models = mergeModelNames(existing.Models, target.Models)
				if target.GroupRatio > existing.GroupRatio {
					existing.ApiKey = target.ApiKey
					existing.ChannelType = target.ChannelType
					existing.GroupRatio = target.GroupRatio
					existing.EndpointType = target.EndpointType
					existing.SourceGroup = target.SourceGroup
				}
				targets[target.SyncKey] = existing
			} else {
				targets[target.SyncKey] = target
			}
			targetCountByGroupID[group.Id]++
		}
	}

	return targets, targetCountByGroupID, warnings
}

func joinNormalizedModels(models []string) string {
	return strings.Join(normalizeModelNames(models), ",")
}

func cloneSupplierGroups(groups []*model.SupplierGroup) []*model.SupplierGroup {
	result := make([]*model.SupplierGroup, 0, len(groups))
	for _, group := range groups {
		if group == nil {
			continue
		}
		copied := *group
		result = append(result, &copied)
	}
	return result
}

func buildSupplierGroupResponse(supplier *model.Supplier, groups []*model.SupplierGroup) []gin.H {
	items := make([]gin.H, 0, len(groups))
	if supplier == nil {
		for _, group := range groups {
			if group == nil {
				continue
			}
			items = append(items, gin.H{
				"id":               group.Id,
				"supplier_id":      group.SupplierID,
				"upstream_group":   group.UpstreamGroup,
				"api_key":          group.ApiKey,
				"local_group":      group.LocalGroup,
				"group_ratio":      group.GroupRatio,
				"supported_models": group.SupportedModels,
				"endpoint_type":    group.EndpointType,
				"endpoint_types":   getSupplierGroupEndpointTypes(group),
				"status":           group.Status,
			})
		}
		return items
	}

	localGroups := getLocalGroups()
	localGroupSet := make(map[string]struct{}, len(localGroups))
	for _, localGroup := range localGroups {
		localGroupSet[localGroup.Name] = struct{}{}
	}
	resolver := loadSupplierModelResolver()
	clonedGroups := cloneSupplierGroups(groups)
	targets, _, _ := buildSupplierChannelTargets(supplier, clonedGroups, &localGroups, localGroupSet, resolver)
	targetsByGroupID := make(map[int][]gin.H)
	for _, target := range targets {
		if target.SourceGroup == nil {
			continue
		}
		category := detectSupplierCategoryWithRules(resolver.rules, target.LocalGroup, target.UpstreamGroup, strings.Join(target.Models, ","))
		rule, _ := getSupplierProviderRuleByCategory(category, resolver.rules)
		targetsByGroupID[target.SourceGroup.Id] = append(targetsByGroupID[target.SourceGroup.Id], gin.H{
			"sync_key":          target.SyncKey,
			"local_group":       target.LocalGroup,
			"vendor_name":       rule.VendorName,
			"category":          category,
			"channel_type":      target.ChannelType,
			"channel_type_name": constant.GetChannelTypeName(target.ChannelType),
			"models_count":      len(normalizeModelNames(target.Models)),
			"models":            joinNormalizedModels(target.Models),
			"upstream_group":    target.UpstreamGroup,
		})
	}

	for _, group := range groups {
		if group == nil {
			continue
		}
		previewTargets := targetsByGroupID[group.Id]
		previewLocalGroups := make([]string, 0, len(previewTargets))
		previewVendors := make([]string, 0, len(previewTargets))
		previewTypes := make([]int, 0, len(previewTargets))
		for _, target := range previewTargets {
			if localGroup, ok := target["local_group"].(string); ok && strings.TrimSpace(localGroup) != "" {
				previewLocalGroups = append(previewLocalGroups, localGroup)
			}
			if vendorName, ok := target["vendor_name"].(string); ok && strings.TrimSpace(vendorName) != "" {
				previewVendors = append(previewVendors, vendorName)
			}
			if channelType, ok := target["channel_type"].(int); ok {
				previewTypes = append(previewTypes, channelType)
			}
		}
		items = append(items, gin.H{
			"id":                   group.Id,
			"supplier_id":          group.SupplierID,
			"upstream_group":       group.UpstreamGroup,
			"api_key":              group.ApiKey,
			"local_group":          group.LocalGroup,
			"group_ratio":          group.GroupRatio,
			"supported_models":     group.SupportedModels,
			"endpoint_type":        group.EndpointType,
			"endpoint_types":       getSupplierGroupEndpointTypes(group),
			"status":               group.Status,
			"target_channels":      previewTargets,
			"target_local_groups":  previewLocalGroups,
			"target_vendor_names":  previewVendors,
			"target_channel_types": previewTypes,
		})
	}
	return items
}

func buildSupplierCandidateRatios(targets map[string]supplierChannelTarget, supplierMarkup float64) map[string]float64 {
	candidateRatios := make(map[string]float64)
	for _, target := range targets {
		if strings.TrimSpace(target.LocalGroup) == "" {
			continue
		}
		finalRatio := roundRatio(target.GroupRatio * supplierMarkup)
		if existingRatio, exists := candidateRatios[target.LocalGroup]; !exists || finalRatio > existingRatio {
			candidateRatios[target.LocalGroup] = finalRatio
		}
	}
	return candidateRatios
}

func collectUnderpricedLocalGroupWarnings(candidateRatios map[string]float64, currentRatios map[string]float64) []string {
	if len(candidateRatios) == 0 {
		return nil
	}
	groupNames := make([]string, 0, len(candidateRatios))
	for groupName := range candidateRatios {
		groupNames = append(groupNames, groupName)
	}
	sort.Strings(groupNames)

	warnings := make([]string, 0)
	for _, groupName := range groupNames {
		requiredRatio := roundRatio(candidateRatios[groupName])
		currentRatio := roundRatio(currentRatios[groupName])
		if currentRatio+0.0001 >= requiredRatio {
			continue
		}
		warnings = append(warnings, fmt.Sprintf("本地分组 %s 当前系统倍率 %.3f 低于按现有上游成本推导的建议倍率 %.3f，继续使用可能亏本，请执行完整同步或手动调高倍率", groupName, currentRatio, requiredRatio))
	}
	return warnings
}

func mergeProjectedGroupRatios(base map[string]float64, candidateRatios map[string]float64) map[string]float64 {
	projected := make(map[string]float64, len(base)+len(candidateRatios))
	for groupName, ratio := range base {
		projected[groupName] = roundRatio(ratio)
	}
	for groupName, ratio := range candidateRatios {
		ratio = roundRatio(ratio)
		if currentRatio, exists := projected[groupName]; !exists || ratio > currentRatio {
			projected[groupName] = ratio
		}
	}
	return projected
}

func inspectSupplierRatioRisks(supplier *model.Supplier, groups []*model.SupplierGroup) []string {
	if supplier == nil || len(groups) == 0 {
		return nil
	}
	localGroups := getLocalGroups()
	localGroupSet := make(map[string]struct{}, len(localGroups))
	for _, localGroup := range localGroups {
		localGroupSet[localGroup.Name] = struct{}{}
	}

	resolver := loadSupplierModelResolver()
	clonedGroups := cloneSupplierGroups(groups)
	targets, _, targetWarnings := buildSupplierChannelTargets(supplier, clonedGroups, &localGroups, localGroupSet, resolver)
	candidateRatios := buildSupplierCandidateRatios(targets, supplier.Markup)
	ratioWarnings := collectUnderpricedLocalGroupWarnings(candidateRatios, ratio_setting.GetGroupRatioCopy())

	if len(targetWarnings) == 0 {
		return ratioWarnings
	}
	return append(targetWarnings, ratioWarnings...)
}

func persistSupplierGroupLocalMappings(groups []*model.SupplierGroup) []string {
	warnings := make([]string, 0)
	for _, group := range groups {
		if group == nil || group.Id <= 0 || strings.TrimSpace(group.LocalGroup) == "" {
			continue
		}
		if err := model.UpdateSupplierGroup(group); err != nil {
			warnings = append(warnings, fmt.Sprintf("更新分组 %s 的本地映射失败: %v", group.UpstreamGroup, err))
		}
	}
	return warnings
}

func clearSupplierChannelProfitGuard(channel *model.Channel) bool {
	if channel == nil {
		return false
	}
	otherInfo := channel.GetOtherInfo()
	originalCount := len(otherInfo)
	delete(otherInfo, "profit_guard")
	delete(otherInfo, "profit_guard_current_ratio")
	delete(otherInfo, "profit_guard_required_ratio")
	delete(otherInfo, "profit_guard_updated_at")
	delete(otherInfo, "profit_guard_reason")
	if len(otherInfo) == originalCount {
		return false
	}
	channel.SetOtherInfo(otherInfo)
	return true
}

func protectSupplierChannelFromLoss(channel *model.Channel, target supplierChannelTarget, currentRatio float64, requiredRatio float64) (bool, error) {
	if channel == nil {
		return false, nil
	}
	originalStatus := channel.Status
	originalOtherInfo := channel.OtherInfo
	otherInfo := channel.GetOtherInfo()
	otherInfo["profit_guard"] = true
	otherInfo["profit_guard_current_ratio"] = currentRatio
	otherInfo["profit_guard_required_ratio"] = requiredRatio
	otherInfo["profit_guard_updated_at"] = common.GetTimestamp()
	otherInfo["profit_guard_reason"] = fmt.Sprintf("本地分组 %s 当前系统倍率 %.3f 低于该上游所需倍率 %.3f，已自动保护避免亏损", target.LocalGroup, currentRatio, requiredRatio)
	channel.SetOtherInfo(otherInfo)
	if channel.Status != common.ChannelStatusManuallyDisabled {
		channel.Status = common.ChannelStatusAutoDisabled
	}
	if channel.Status == originalStatus && channel.OtherInfo == originalOtherInfo {
		return false, nil
	}
	return true, channel.Update()
}

func reconcileSupplierChannels(supplier *model.Supplier, targets map[string]supplierChannelTarget, currentGroupRatios map[string]float64) (int, int, int, []string) {
	warnings := make([]string, 0)
	if currentGroupRatios == nil {
		currentGroupRatios = ratio_setting.GetGroupRatioCopy()
	}
	existingChannels, _ := model.GetChannelsBySupplierID(supplier.Id)
	allRefs := make([]*supplierExistingChannelRef, 0, len(existingChannels))
	existingBySyncKey := make(map[string][]*supplierExistingChannelRef)
	existingByLocalGroup := make(map[string][]*supplierExistingChannelRef)
	for _, channel := range existingChannels {
		if channel == nil {
			continue
		}
		ref := &supplierExistingChannelRef{Channel: channel}
		allRefs = append(allRefs, ref)
		syncKey := getSupplierChannelSyncKey(channel)
		if syncKey != "" {
			existingBySyncKey[syncKey] = append(existingBySyncKey[syncKey], ref)
		}
		localGroup := strings.TrimSpace(channel.Group)
		existingByLocalGroup[localGroup] = append(existingByLocalGroup[localGroup], ref)
	}

	targetKeys := make([]string, 0, len(targets))
	for syncKey := range targets {
		targetKeys = append(targetKeys, syncKey)
	}
	sort.Strings(targetKeys)

	channelsCreated := 0
	channelsUpdated := 0
	channelsDeleted := 0

	for _, syncKey := range targetKeys {
		target := targets[syncKey]
		refs := existingBySyncKey[syncKey]
		var mainRef *supplierExistingChannelRef
		if len(refs) > 0 {
			mainRef = refs[0]
		}
		if mainRef == nil {
			for _, ref := range existingByLocalGroup[target.LocalGroup] {
				if ref == nil || ref.Matched {
					continue
				}
				existingSyncKey := getSupplierChannelSyncKey(ref.Channel)
				if existingSyncKey != "" && existingSyncKey != syncKey {
					continue
				}
				mainRef = ref
				break
			}
		}
		if profitable, currentRatio, requiredRatio := isSupplierTargetProfitable(target, supplier, currentGroupRatios); !profitable {
			warnings = append(warnings, fmt.Sprintf("本地分组 %s 当前系统倍率 %.3f 低于该上游所需倍率 %.3f，渠道 %s 已跳过创建/启用以避免亏损", target.LocalGroup, currentRatio, requiredRatio, target.Name))
			if mainRef != nil && mainRef.Channel != nil {
				mainRef.Matched = true
				changed, err := protectSupplierChannelFromLoss(mainRef.Channel, target, currentRatio, requiredRatio)
				if err != nil {
					warnings = append(warnings, fmt.Sprintf("保护渠道 %s 失败: %v", target.Name, err))
				} else if changed {
					channelsUpdated++
				}
			}
			if len(refs) > 1 {
				for _, extraRef := range refs[1:] {
					if extraRef == nil || extraRef.Channel == nil || extraRef.Matched {
						continue
					}
					extraRef.Matched = true
					if err := extraRef.Channel.Delete(); err != nil {
						warnings = append(warnings, fmt.Sprintf("删除重复渠道 %d(%s) 失败: %v", extraRef.Channel.Id, target.Name, err))
						continue
					}
					channelsDeleted++
				}
			}
			continue
		}
		if strings.TrimSpace(target.ApiKey) == "" {
			warnings = append(warnings, fmt.Sprintf("本地分组 %s 未获取到可用密钥，跳过本次渠道更新（上游分组: %s）", target.LocalGroup, target.UpstreamGroup))
			continue
		}

		models := joinNormalizedModels(target.Models)
		if models == "" {
			warnings = append(warnings, fmt.Sprintf("本地分组 %s 未识别到可用模型，跳过渠道更新并清理旧渠道（上游分组: %s）", target.LocalGroup, target.UpstreamGroup))
			continue
		}

		if mainRef == nil {
			channel := &model.Channel{
				Type:       target.ChannelType,
				Key:        target.ApiKey,
				Name:       target.Name,
				BaseURL:    &supplier.BaseURL,
				Group:      target.LocalGroup,
				Models:     models,
				Status:     common.ChannelStatusEnabled,
				SupplierID: supplier.Id,
			}
			stampSupplierChannelOtherInfo(channel, target)
			if err := channel.Insert(); err != nil {
				warnings = append(warnings, fmt.Sprintf("创建渠道 %s 失败: %v", target.Name, err))
				continue
			}
			channelsCreated++
			continue
		}

		mainRef.Matched = true
		mainChannel := mainRef.Channel
		changed := false
		if clearSupplierChannelProfitGuard(mainChannel) {
			changed = true
		}
		originalOtherInfo := mainChannel.OtherInfo
		stampSupplierChannelOtherInfo(mainChannel, target)
		if originalOtherInfo != mainChannel.OtherInfo {
			changed = true
		}
		if mainChannel.Key != target.ApiKey {
			mainChannel.Key = target.ApiKey
			changed = true
		}
		if mainChannel.Name != target.Name {
			mainChannel.Name = target.Name
			changed = true
		}
		if strings.TrimSpace(mainChannel.Group) != target.LocalGroup {
			mainChannel.Group = target.LocalGroup
			changed = true
		}
		if mainChannel.Models != models {
			mainChannel.Models = models
			changed = true
		}
		if mainChannel.Type != target.ChannelType {
			mainChannel.Type = target.ChannelType
			changed = true
		}
		if mainChannel.BaseURL == nil || strings.TrimSpace(*mainChannel.BaseURL) != supplier.BaseURL {
			mainChannel.BaseURL = &supplier.BaseURL
			changed = true
		}
		if mainChannel.SupplierID != supplier.Id {
			mainChannel.SupplierID = supplier.Id
			changed = true
		}
		if mainChannel.Status != common.ChannelStatusEnabled {
			mainChannel.Status = common.ChannelStatusEnabled
			changed = true
		}
		if changed {
			if err := mainChannel.Update(); err != nil {
				warnings = append(warnings, fmt.Sprintf("更新渠道 %s 失败: %v", target.Name, err))
			} else {
				channelsUpdated++
			}
		}

		if len(refs) > 1 {
			for _, extraRef := range refs[1:] {
				if extraRef == nil || extraRef.Channel == nil || extraRef.Matched {
					continue
				}
				extraRef.Matched = true
				if err := extraRef.Channel.Delete(); err != nil {
					warnings = append(warnings, fmt.Sprintf("删除重复渠道 %d(%s) 失败: %v", extraRef.Channel.Id, target.Name, err))
					continue
				}
				channelsDeleted++
			}
		}
	}

	for _, ref := range allRefs {
		if ref == nil || ref.Channel == nil || ref.Matched {
			continue
		}
		if err := ref.Channel.Delete(); err != nil {
			warnings = append(warnings, fmt.Sprintf("删除渠道 %d(%s) 失败: %v", ref.Channel.Id, ref.Channel.Name, err))
			continue
		}
		channelsDeleted++
	}

	return channelsCreated, channelsUpdated, channelsDeleted, warnings
}

func parseStringArray(raw interface{}) []string {
	result := make([]string, 0)
	seen := make(map[string]struct{})
	appendValue := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, exists := seen[v]; exists {
			return
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}

	switch typed := raw.(type) {
	case []interface{}:
		for _, item := range typed {
			if s, ok := item.(string); ok {
				appendValue(s)
			}
		}
	case []string:
		for _, s := range typed {
			appendValue(s)
		}
	case string:
		appendValue(typed)
	}
	return result
}

func normalizeEndpointType(endpointType string) string {
	et := strings.ToLower(strings.TrimSpace(endpointType))
	if et == "" {
		return string(constant.EndpointTypeOpenAI)
	}
	return et
}

func normalizeEndpointTypes(raw interface{}) []string {
	items := parseStringArray(raw)
	if len(items) == 0 {
		return []string{string(constant.EndpointTypeOpenAI)}
	}
	result := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		et := normalizeEndpointType(item)
		if _, exists := seen[et]; exists {
			continue
		}
		seen[et] = struct{}{}
		result = append(result, et)
	}
	return result
}

func endpointSetToList(endpointSet map[string]struct{}) []string {
	if len(endpointSet) == 0 {
		return []string{string(constant.EndpointTypeOpenAI)}
	}
	endpoints := make([]string, 0, len(endpointSet))
	for endpoint := range endpointSet {
		endpoints = append(endpoints, endpoint)
	}
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i] == string(constant.EndpointTypeOpenAI) && endpoints[j] != string(constant.EndpointTypeOpenAI) {
			return true
		}
		if endpoints[j] == string(constant.EndpointTypeOpenAI) && endpoints[i] != string(constant.EndpointTypeOpenAI) {
			return false
		}
		return endpoints[i] < endpoints[j]
	})
	return endpoints
}

func choosePreferredEndpointType(endpointTypes []string) string {
	if len(endpointTypes) == 0 {
		return string(constant.EndpointTypeOpenAI)
	}
	for _, endpointType := range endpointTypes {
		if endpointType == string(constant.EndpointTypeOpenAI) {
			return endpointType
		}
	}
	return endpointTypes[0]
}

// parsePricingResponse 解析上游 pricing 接口，获取完整的分组、模型、端点类型信息
func parsePricingResponse(body []byte) (*UpstreamPricingInfo, error) {
	var pricingResp map[string]interface{}
	if err := common.Unmarshal(body, &pricingResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %v", err)
	}

	info := &UpstreamPricingInfo{
		Groups:             make(map[string]float64),
		GroupModels:        make(map[string][]string),
		GroupEndpoint:      make(map[string]string),
		GroupEndpointTypes: make(map[string][]string),
		UsableGroup:        make(map[string]string),
		AllModels:          make([]string, 0),
		ModelRatios:        make(map[string]float64),
		CompletionRatios:   make(map[string]float64),
		ModelPrices:        make(map[string]float64),
		CacheRatios:        make(map[string]float64),
	}

	groupModelSet := make(map[string]map[string]struct{})
	groupEndpointSet := make(map[string]map[string]struct{})
	groupFallbackEndpointSet := make(map[string]map[string]struct{})
	allModelSet := make(map[string]struct{})

	// 解析 group_ratio
	if gr, ok := pricingResp["group_ratio"]; ok {
		if grMap, ok := gr.(map[string]interface{}); ok {
			for groupName, ratioRaw := range grMap {
				groupName = strings.TrimSpace(groupName)
				if groupName == "" {
					continue
				}
				if ratio, ok := getFloatFromAny(ratioRaw); ok {
					info.Groups[groupName] = ratio
				}
			}
		}
	}

	// 解析 usable_group（兼容 object / array 两种格式）
	if ug, ok := pricingResp["usable_group"]; ok {
		switch typed := ug.(type) {
		case map[string]interface{}:
			for groupName, aliasRaw := range typed {
				groupName = strings.TrimSpace(groupName)
				if groupName == "" {
					continue
				}
				if alias, ok := aliasRaw.(string); ok {
					info.UsableGroup[groupName] = alias
				}
				if _, exists := info.Groups[groupName]; !exists {
					info.Groups[groupName] = 1.0
				}
			}
		case []interface{}:
			for _, item := range typed {
				groupName, ok := item.(string)
				if !ok {
					continue
				}
				groupName = strings.TrimSpace(groupName)
				if groupName == "" {
					continue
				}
				if _, exists := info.Groups[groupName]; !exists {
					info.Groups[groupName] = 1.0
				}
			}
		}
	}

	// 解析 data 数组：模型、分组、端点类型
	if data, ok := pricingResp["data"]; ok {
		if dataArray, ok := data.([]interface{}); ok {
			for _, item := range dataArray {
				itemMap, ok := item.(map[string]interface{})
				if !ok {
					continue
				}

				modelName, _ := itemMap["model_name"].(string)
				modelName = strings.TrimSpace(modelName)
				enableGroups := parseStringArray(itemMap["enable_groups"])
				endpointTypes := normalizeEndpointTypes(itemMap["supported_endpoint_types"])

				quotaTypeRaw, hasQuotaType := getFloatFromAny(itemMap["quota_type"])
				if modelName != "" {
					if hasQuotaType && int(quotaTypeRaw) == 1 {
						if modelPrice, ok := getFloatFromAny(itemMap["model_price"]); ok && modelPrice >= 0 {
							info.ModelPrices[modelName] = roundRatio(modelPrice)
						}
					} else {
						if modelRatio, ok := getFloatFromAny(itemMap["model_ratio"]); ok && modelRatio >= 0 {
							info.ModelRatios[modelName] = roundRatio(modelRatio)
						}
						if completionRatio, ok := getFloatFromAny(itemMap["completion_ratio"]); ok && completionRatio >= 0 {
							info.CompletionRatios[modelName] = roundRatio(completionRatio)
						}
						if cacheRatio, ok := getFloatFromAny(itemMap["cache_ratio"]); ok && cacheRatio >= 0 {
							info.CacheRatios[modelName] = roundRatio(cacheRatio)
						}
					}
				}

				for _, groupName := range enableGroups {
					groupName = strings.TrimSpace(groupName)
					if groupName == "" {
						continue
					}
					if _, exists := info.Groups[groupName]; !exists {
						info.Groups[groupName] = 1.0
					}
				}

				if len(enableGroups) == 0 {
					continue
				}

				if modelName != "" {
					if _, exists := allModelSet[modelName]; !exists {
						allModelSet[modelName] = struct{}{}
						info.AllModels = append(info.AllModels, modelName)
					}
					for _, groupName := range enableGroups {
						if _, exists := groupModelSet[groupName]; !exists {
							groupModelSet[groupName] = make(map[string]struct{})
						}
						groupModelSet[groupName][modelName] = struct{}{}
						if _, exists := groupEndpointSet[groupName]; !exists {
							groupEndpointSet[groupName] = make(map[string]struct{})
						}
						for _, endpointType := range endpointTypes {
							groupEndpointSet[groupName][endpointType] = struct{}{}
						}
					}
					continue
				}

				// model_name 为空，作为分组级兜底端点配置
				for _, groupName := range enableGroups {
					if _, exists := groupFallbackEndpointSet[groupName]; !exists {
						groupFallbackEndpointSet[groupName] = make(map[string]struct{})
					}
					for _, endpointType := range endpointTypes {
						groupFallbackEndpointSet[groupName][endpointType] = struct{}{}
					}
				}
			}
		}
	}

	// 输出分组模型
	for groupName, modelSet := range groupModelSet {
		models := make([]string, 0, len(modelSet))
		for modelName := range modelSet {
			models = append(models, modelName)
		}
		sort.Strings(models)
		info.GroupModels[groupName] = models
	}
	sort.Strings(info.AllModels)

	// 输出分组端点（优先模型端点，其次分组兜底；默认 openai）
	for groupName := range info.Groups {
		endpointSet := make(map[string]struct{})
		if modelEndpointSet, exists := groupEndpointSet[groupName]; exists {
			for endpointType := range modelEndpointSet {
				endpointSet[endpointType] = struct{}{}
			}
		}
		if len(endpointSet) == 0 {
			if fallbackSet, exists := groupFallbackEndpointSet[groupName]; exists {
				for endpointType := range fallbackSet {
					endpointSet[endpointType] = struct{}{}
				}
			}
		}

		endpointTypes := endpointSetToList(endpointSet)
		info.GroupEndpointTypes[groupName] = endpointTypes
		info.GroupEndpoint[groupName] = choosePreferredEndpointType(endpointTypes)
	}

	return info, nil
}

// fetchUpstreamPricing 获取上游定价信息
func fetchUpstreamPricing(client *http.Client, supplier *model.Supplier) (*UpstreamPricingInfo, error) {
	pricingURL := supplier.BaseURL + "/api/pricing"
	req, err := http.NewRequest(http.MethodGet, pricingURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求上游失败: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %v", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := parseUpstreamMessage(body)
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("上游响应异常: %s", msg)
	}

	return parsePricingResponse(body)
}

// autoMapLocalGroup 自动映射上游分组到本地分组
// 规则：
// - 上游名称包含 cc、claude → 对应本地 cc 开头分组
// - 上游名称包含 codex、openai → 对应本地 codex 开头分组
// - 上游名称包含 gemini → 对应本地 gemini 开头分组
// - 确认分组范围后，找倍率最接近的去对应
func autoMapLocalGroup(upstreamGroup string, upstreamRatio float64, localGroups []LocalGroupInfo) string {
	if exact, exists := findLocalGroupInfo(localGroups, upstreamGroup); exists {
		return exact.Name
	}
	category := detectSupplierCategory(upstreamGroup)
	if category == "" {
		return ""
	}
	candidates := make([]LocalGroupInfo, 0)
	for _, localGroup := range localGroups {
		if localGroup.Category == "" {
			localGroup.Category = detectSupplierCategory(localGroup.Name)
		}
		if localGroup.Category == category {
			candidates = append(candidates, localGroup)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	return pickNearestLocalGroup(candidates, upstreamRatio)
}

// LocalGroupInfo 本地分组信息
type LocalGroupInfo struct {
	Name     string
	Ratio    float64
	Category string
}

// getLocalGroups 获取本地分组列表及其倍率
func getLocalGroups() []LocalGroupInfo {
	ratioMap := ratio_setting.GetGroupRatioCopy()
	result := make([]LocalGroupInfo, 0, len(ratioMap))
	for name, ratio := range ratioMap {
		result = append(result, LocalGroupInfo{
			Name:     name,
			Ratio:    roundRatio(ratio),
			Category: detectSupplierCategory(name),
		})
	}
	return result
}

func protectSyncedGroupRatios(candidateRatios map[string]float64) (map[string]float64, []string, int) {
	currentRatios := ratio_setting.GetGroupRatioCopy()
	protectedRatios := make(map[string]float64, len(candidateRatios))
	warnings := make([]string, 0)
	heldCount := 0

	groupNames := make([]string, 0, len(candidateRatios))
	for groupName := range candidateRatios {
		groupNames = append(groupNames, groupName)
	}
	sort.Strings(groupNames)

	for _, groupName := range groupNames {
		syncedRatio := roundRatio(candidateRatios[groupName])
		protectedRatio := syncedRatio
		if currentRatio, exists := currentRatios[groupName]; exists {
			currentRatio = roundRatio(currentRatio)
			if currentRatio > protectedRatio {
				protectedRatio = currentRatio
				heldCount++
				warnings = append(warnings, fmt.Sprintf("本地分组 %s 当前系统倍率 %.3f 高于同步结果 %.3f，为避免亏本已保留当前倍率", groupName, currentRatio, syncedRatio))
			}
		}
		protectedRatios[groupName] = protectedRatio
	}

	return protectedRatios, warnings, heldCount
}

func getRequiredSupplierLocalRatio(target supplierChannelTarget, supplier *model.Supplier) float64 {
	if supplier == nil {
		return 0
	}
	return roundRatio(target.GroupRatio * supplier.Markup)
}

func isSupplierTargetProfitable(target supplierChannelTarget, supplier *model.Supplier, currentGroupRatios map[string]float64) (bool, float64, float64) {
	localGroup := strings.TrimSpace(target.LocalGroup)
	if localGroup == "" || supplier == nil {
		return true, 0, 0
	}
	currentRatio, exists := currentGroupRatios[localGroup]
	if !exists {
		return true, 0, 0
	}
	currentRatio = roundRatio(currentRatio)
	requiredRatio := getRequiredSupplierLocalRatio(target, supplier)
	return currentRatio >= requiredRatio, currentRatio, requiredRatio
}

// roundRatio 倍率保留3位小数
func roundRatio(ratio float64) float64 {
	return math.Round(ratio*1000) / 1000
}

func isSuspiciousSupplierPricing(modelName string, modelRatio float64, completionRatio float64) bool {
	normalized := ratio_setting.FormatMatchingModelName(strings.TrimSpace(modelName))
	_, exists := ratio_setting.GetDefaultModelRatioMap()[normalized]
	if exists {
		return false
	}
	if roundRatio(modelRatio) != 37.5 {
		return false
	}
	return completionRatio <= 0 || completionRatio == 1 || completionRatio == 5
}

func hasLocalDefaultPricing(modelName string) bool {
	normalized := ratio_setting.FormatMatchingModelName(strings.TrimSpace(modelName))
	if normalized == "" {
		return false
	}
	if _, exists := ratio_setting.GetDefaultModelRatioMap()[normalized]; exists {
		return true
	}
	if _, exists := ratio_setting.GetDefaultModelPriceMap()[normalized]; exists {
		return true
	}
	return false
}

func shouldSkipUnknownSupplierPricing(modelName string, modelRatio float64, completionRatio float64) (bool, string) {
	if hasLocalDefaultPricing(modelName) {
		return true, "本地已有标准售价"
	}
	if isSuspiciousSupplierPricing(modelName, modelRatio, completionRatio) {
		return true, fmt.Sprintf("上游返回疑似兜底倍率 %.3f/%.3f", modelRatio, completionRatio)
	}
	if roundRatio(modelRatio) >= 20 && completionRatio <= 1 {
		return true, fmt.Sprintf("上游返回高成本倍率 %.3f/%.3f，本地未配置标准售价", modelRatio, completionRatio)
	}
	return false, ""
}

func syncSupplierModelPricing(info *UpstreamPricingInfo, targets map[string]supplierChannelTarget) (int, []string) {
	if info == nil || len(targets) == 0 {
		return 0, nil
	}
	persistOptions := model.DB != nil
	targetModelSet := make(map[string]struct{})
	for _, target := range targets {
		for _, modelName := range normalizeModelNames(target.Models) {
			targetModelSet[modelName] = struct{}{}
		}
	}
	if len(targetModelSet) == 0 {
		return 0, nil
	}

	modelRatios := ratio_setting.GetModelRatioCopy()
	completionRatios := ratio_setting.GetCompletionRatioCopy()
	modelPrices := ratio_setting.GetModelPriceCopy()
	cacheRatios := ratio_setting.GetCacheRatioCopy()

	warnings := make([]string, 0)
	changedModelRatio := false
	changedCompletionRatio := false
	changedModelPrice := false
	changedCacheRatio := false
	updatedModels := make(map[string]struct{})

	for modelName := range targetModelSet {
		if hasLocalDefaultPricing(modelName) {
			if _, exists := modelPrices[modelName]; exists {
				delete(modelPrices, modelName)
				changedModelPrice = true
			}
			if _, exists := modelRatios[modelName]; exists {
				delete(modelRatios, modelName)
				changedModelRatio = true
			}
			if _, exists := completionRatios[modelName]; exists {
				delete(completionRatios, modelName)
				changedCompletionRatio = true
			}
			continue
		}

		if price, ok := info.ModelPrices[modelName]; ok && price >= 0 {
			if current, exists := modelPrices[modelName]; !exists || current != price {
				modelPrices[modelName] = price
				changedModelPrice = true
			}
			if _, exists := modelRatios[modelName]; exists {
				delete(modelRatios, modelName)
				changedModelRatio = true
			}
			if _, exists := completionRatios[modelName]; exists {
				delete(completionRatios, modelName)
				changedCompletionRatio = true
			}
			updatedModels[modelName] = struct{}{}
			continue
		}

		modelRatio, hasModelRatio := info.ModelRatios[modelName]
		completionRatio, hasCompletionRatio := info.CompletionRatios[modelName]
		if hasModelRatio {
			if skip, reason := shouldSkipUnknownSupplierPricing(modelName, modelRatio, completionRatio); skip {
				if _, exists := modelRatios[modelName]; exists {
					delete(modelRatios, modelName)
					changedModelRatio = true
				}
				if _, exists := completionRatios[modelName]; exists {
					delete(completionRatios, modelName)
					changedCompletionRatio = true
				}
				if _, exists := modelPrices[modelName]; exists {
					delete(modelPrices, modelName)
					changedModelPrice = true
				}
				warnings = append(warnings, fmt.Sprintf("模型 %s %s，已跳过价格同步", modelName, reason))
				continue
			}
			if current, exists := modelRatios[modelName]; !exists || current != modelRatio {
				modelRatios[modelName] = modelRatio
				changedModelRatio = true
			}
			if _, exists := modelPrices[modelName]; exists {
				delete(modelPrices, modelName)
				changedModelPrice = true
			}
			updatedModels[modelName] = struct{}{}
		}
		if hasCompletionRatio {
			if current, exists := completionRatios[modelName]; !exists || current != completionRatio {
				completionRatios[modelName] = completionRatio
				changedCompletionRatio = true
			}
		}
		if cacheRatio, ok := info.CacheRatios[modelName]; ok {
			if current, exists := cacheRatios[modelName]; !exists || current != cacheRatio {
				cacheRatios[modelName] = cacheRatio
				changedCacheRatio = true
			}
		}
	}

	if !changedModelRatio && !changedCompletionRatio && !changedModelPrice && !changedCacheRatio {
		return len(updatedModels), warnings
	}

	if changedModelRatio {
		jsonBytes, err := common.Marshal(modelRatios)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("序列化 ModelRatio 失败: %v", err))
		} else if persistOptions {
			if err := model.UpdateOption("ModelRatio", string(jsonBytes)); err != nil {
				warnings = append(warnings, fmt.Sprintf("保存 ModelRatio 失败: %v", err))
			}
		} else if err := ratio_setting.UpdateModelRatioByJSONString(string(jsonBytes)); err != nil {
			warnings = append(warnings, fmt.Sprintf("应用 ModelRatio 失败: %v", err))
		}
	}
	if changedCompletionRatio {
		jsonBytes, err := common.Marshal(completionRatios)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("序列化 CompletionRatio 失败: %v", err))
		} else if persistOptions {
			if err := model.UpdateOption("CompletionRatio", string(jsonBytes)); err != nil {
				warnings = append(warnings, fmt.Sprintf("保存 CompletionRatio 失败: %v", err))
			}
		} else if err := ratio_setting.UpdateCompletionRatioByJSONString(string(jsonBytes)); err != nil {
			warnings = append(warnings, fmt.Sprintf("应用 CompletionRatio 失败: %v", err))
		}
	}
	if changedModelPrice {
		jsonBytes, err := common.Marshal(modelPrices)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("序列化 ModelPrice 失败: %v", err))
		} else if persistOptions {
			if err := model.UpdateOption("ModelPrice", string(jsonBytes)); err != nil {
				warnings = append(warnings, fmt.Sprintf("保存 ModelPrice 失败: %v", err))
			}
		} else if err := ratio_setting.UpdateModelPriceByJSONString(string(jsonBytes)); err != nil {
			warnings = append(warnings, fmt.Sprintf("应用 ModelPrice 失败: %v", err))
		}
	}
	if changedCacheRatio {
		jsonBytes, err := common.Marshal(cacheRatios)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("序列化 CacheRatio 失败: %v", err))
		} else if persistOptions {
			if err := model.UpdateOption("CacheRatio", string(jsonBytes)); err != nil {
				warnings = append(warnings, fmt.Sprintf("保存 CacheRatio 失败: %v", err))
			}
		} else if err := ratio_setting.UpdateCacheRatioByJSONString(string(jsonBytes)); err != nil {
			warnings = append(warnings, fmt.Sprintf("应用 CacheRatio 失败: %v", err))
		}
	}
	if len(updatedModels) > 0 && persistOptions {
		model.RefreshPricing()
	}
	return len(updatedModels), warnings
}

func getChannelTypeByEndpointType(endpointType string) int {
	switch normalizeEndpointType(endpointType) {
	case string(constant.EndpointTypeAnthropic):
		return constant.ChannelTypeAnthropic
	case string(constant.EndpointTypeGemini):
		return constant.ChannelTypeGemini
	default:
		return constant.ChannelTypeOpenAI
	}
}

// syncSupplierGroupsFromUpstream 同步供应商分组（仅采集，不修改本地 GroupRatio）
func syncSupplierGroupsFromUpstream(client *http.Client, supplier *model.Supplier) ([]*model.SupplierGroup, *UpstreamPricingInfo, int, int, int, int, error) {
	info, err := fetchUpstreamPricing(client, supplier)
	if err != nil {
		return nil, nil, 0, 0, 0, 0, err
	}
	if len(info.Groups) == 0 {
		return nil, info, 0, 0, 0, 0, fmt.Errorf("上游 pricing 未返回可用分组，已中止同步以避免误删")
	}

	// 获取本地分组用于自动映射
	localGroups := getLocalGroups()

	added := 0
	updated := 0
	removed := 0
	upstreamGroupSet := make(map[string]struct{}, len(info.Groups))

	for groupName, ratio := range info.Groups {
		groupName = strings.TrimSpace(groupName)
		if groupName == "" {
			continue
		}
		upstreamGroupSet[groupName] = struct{}{}
		if ratio == 0 {
			ratio = 1.0
		}
		// 倍率保留3位小数
		ratio = roundRatio(ratio)

		// 获取该分组支持的模型
		models := info.GroupModels[groupName]
		sort.Strings(models)
		modelsStr := strings.Join(models, ",")

		// 获取通道类型（支持端点列表 + 首选端点）
		endpointTypes := info.GroupEndpointTypes[groupName]
		if len(endpointTypes) == 0 {
			endpointTypes = []string{string(constant.EndpointTypeOpenAI)}
		}
		endpointType := normalizeEndpointType(info.GroupEndpoint[groupName])
		endpointTypesJSONBytes, err := common.Marshal(endpointTypes)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to marshal endpoint_types: supplier_id=%d group=%s err=%v", supplier.Id, groupName, err))
			endpointTypesJSONBytes = []byte("[]")
		}
		endpointTypesJSON := string(endpointTypesJSONBytes)

		existing, err := model.GetSupplierGroupByUpstream(supplier.Id, groupName)
		if err != nil {
			// 新分组，自动映射本地分组
			localGroup := autoMapLocalGroup(groupName, ratio, localGroups)

			newGroup := &model.SupplierGroup{
				SupplierID:      supplier.Id,
				UpstreamGroup:   groupName,
				GroupRatio:      ratio,
				SupportedModels: modelsStr,
				EndpointType:    endpointType,
				EndpointTypes:   endpointTypesJSON,
				LocalGroup:      localGroup, // 自动映射
			}
			if err := model.CreateSupplierGroup(newGroup); err != nil {
				common.SysLog(fmt.Sprintf("failed to create supplier group: %v", err))
				continue
			}
			added++
			continue
		}

		// 更新已有分组
		changed := false
		if existing.GroupRatio != ratio {
			existing.GroupRatio = ratio
			changed = true
		}
		if existing.SupportedModels != modelsStr {
			existing.SupportedModels = modelsStr
			changed = true
		}
		if existing.EndpointType != endpointType {
			existing.EndpointType = endpointType
			changed = true
		}
		if strings.TrimSpace(existing.EndpointTypes) != endpointTypesJSON {
			existing.EndpointTypes = endpointTypesJSON
			changed = true
		}
		// 仅在未人工设置映射时自动补齐
		if strings.TrimSpace(existing.LocalGroup) == "" {
			autoMappedGroup := autoMapLocalGroup(groupName, ratio, localGroups)
			if autoMappedGroup != "" {
				existing.LocalGroup = autoMappedGroup
				changed = true
			}
		}

		if changed {
			if err := model.UpdateSupplierGroup(existing); err != nil {
				common.SysLog(fmt.Sprintf("failed to update supplier group: %v", err))
				continue
			}
			updated++
		}
	}

	existingGroups, _ := model.GetSupplierGroups(supplier.Id)
	for _, group := range existingGroups {
		if group == nil {
			continue
		}
		if _, exists := upstreamGroupSet[strings.TrimSpace(group.UpstreamGroup)]; exists {
			continue
		}
		if err := model.DeleteSupplierGroup(group.Id); err != nil {
			common.SysLog(fmt.Sprintf("failed to delete stale supplier group: supplier_id=%d group=%s err=%v", supplier.Id, group.UpstreamGroup, err))
			continue
		}
		removed++
	}

	groups, _ := model.GetSupplierGroups(supplier.Id)
	return groups, info, len(info.Groups), added, updated, removed, nil
}

func buildSupplierGroupTokenName(supplierID int, upstreamGroup string) string {
	group := strings.TrimSpace(upstreamGroup)
	group = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(group)
	if group == "" {
		group = "default"
	}
	name := fmt.Sprintf("supplier-%d-%s", supplierID, group)
	runes := []rune(name)
	if len(runes) > 50 {
		name = string(runes[:50])
	}
	return name
}

func normalizeUpstreamTokenKey(raw string) string {
	return strings.TrimPrefix(strings.TrimSpace(raw), "sk-")
}

func fetchUpstreamTokenPage(client *http.Client, supplier *model.Supplier, auth *upstreamAuthInfo, page int) ([]upstreamTokenItem, int, error) {
	params := url.Values{}
	params.Set("p", strconv.Itoa(page))
	params.Set("size", strconv.Itoa(upstreamTokenListPageSize))
	listURL := supplier.BaseURL + "/api/token/?" + params.Encode()

	req, err := http.NewRequest(http.MethodGet, listURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("创建查询 token 列表请求失败: %v", err)
	}
	applyUpstreamAuthHeaders(req, auth)

	body, statusCode, err := doUpstreamRequestWith429Retry(client, req, "查询上游 token 列表")
	if err != nil {
		return nil, 0, err
	}
	if statusCode < 200 || statusCode >= 300 {
		msg, _ := parseUpstreamMessageAndRetryAfter(body, nil)
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", statusCode)
		}
		return nil, 0, fmt.Errorf("查询上游 token 列表响应异常: %s", msg)
	}

	var listResp upstreamTokenListResponse
	if err := common.Unmarshal(body, &listResp); err != nil {
		return nil, 0, fmt.Errorf("解析查询 token 列表响应失败: %v", err)
	}
	if !listResp.Success {
		msg := strings.TrimSpace(listResp.Message)
		if msg == "" {
			msg = "查询上游 token 列表返回失败"
		}
		return nil, 0, fmt.Errorf("%s", msg)
	}

	return listResp.Data.Items, listResp.Data.Total, nil
}

func fetchAllUpstreamTokens(client *http.Client, supplier *model.Supplier, auth *upstreamAuthInfo) ([]upstreamTokenItem, error) {
	allItems := make([]upstreamTokenItem, 0)
	total := 0
	totalPages := 1

	for page := 1; page <= totalPages; page++ {
		if page > upstreamTokenListMaxPages {
			return allItems, fmt.Errorf("上游 token 数量过多，最多仅拉取前%d页，请稍后重试", upstreamTokenListMaxPages)
		}

		items, currentTotal, err := fetchUpstreamTokenPage(client, supplier, auth, page)
		if err != nil {
			return nil, err
		}
		allItems = append(allItems, items...)

		if currentTotal > 0 {
			total = currentTotal
			totalPages = (currentTotal + upstreamTokenListPageSize - 1) / upstreamTokenListPageSize
			if totalPages < 1 {
				totalPages = 1
			}
		} else {
			if len(items) < upstreamTokenListPageSize {
				break
			}
			totalPages = page + 1
		}

		if total > 0 && len(allItems) >= total {
			break
		}
		if len(items) == 0 {
			break
		}
	}

	return allItems, nil
}

func buildTokenNameKeyMap(tokens []upstreamTokenItem) map[string]string {
	keyMap := make(map[string]string, len(tokens))
	for _, item := range tokens {
		name := strings.TrimSpace(item.Name)
		key := normalizeUpstreamTokenKey(item.Key)
		if name == "" || key == "" {
			continue
		}
		if _, exists := keyMap[name]; !exists {
			keyMap[name] = key
		}
	}
	return keyMap
}

func createUpstreamGroupToken(client *http.Client, supplier *model.Supplier, auth *upstreamAuthInfo, tokenName string, groupName string) (string, error) {
	createURL := supplier.BaseURL + "/api/token/"
	payload := map[string]interface{}{
		"name":                 tokenName,
		"expired_time":         -1,
		"remain_quota":         0,
		"unlimited_quota":      true,
		"model_limits_enabled": false,
		"group":                groupName,
	}
	bodyBytes, err := common.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("构造创建 token 请求失败: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, createURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("创建 token 请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	applyUpstreamAuthHeaders(req, auth)

	body, statusCode, err := doUpstreamRequestWith429Retry(client, req, "创建上游 token")
	if err != nil {
		return "", err
	}
	if statusCode < 200 || statusCode >= 300 {
		msg, _ := parseUpstreamMessageAndRetryAfter(body, nil)
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", statusCode)
		}
		return "", fmt.Errorf("创建上游 token 响应异常: %s", msg)
	}

	var createResp map[string]interface{}
	if err := common.Unmarshal(body, &createResp); err != nil {
		return "", fmt.Errorf("解析创建 token 响应失败: %v", err)
	}

	if successAny, exists := createResp["success"]; exists {
		if success, ok := successAny.(bool); ok && !success {
			msg := strings.TrimSpace(parseUpstreamMessage(body))
			if msg == "" {
				msg = "创建上游 token 返回失败"
			}
			return "", fmt.Errorf("%s", msg)
		}
	}

	if data, ok := createResp["data"].(map[string]interface{}); ok {
		if key, ok := data["key"].(string); ok {
			return normalizeUpstreamTokenKey(key), nil
		}
	}
	if key, ok := createResp["key"].(string); ok {
		return normalizeUpstreamTokenKey(key), nil
	}
	return "", nil
}

func saveSupplierGroupToken(group *model.SupplierGroup, tokenKey string) error {
	key := normalizeUpstreamTokenKey(tokenKey)
	if key == "" {
		return fmt.Errorf("空 token key")
	}
	group.ApiKey = key
	return model.UpdateSupplierGroup(group)
}

func autoProvisionGroupTokens(client *http.Client, supplier *model.Supplier, groups []*model.SupplierGroup, auth *upstreamAuthInfo) (int, int, []string) {
	created := 0
	reused := 0
	failures := make([]string, 0)

	existingTokens, err := fetchAllUpstreamTokens(client, supplier, auth)
	if err != nil {
		return created, reused, []string{fmt.Sprintf("批量查询上游 token 失败: %v", err)}
	}
	tokenKeyByName := buildTokenNameKeyMap(existingTokens)

	type pendingTokenResolution struct {
		group     *model.SupplierGroup
		tokenName string
	}
	pendingResolutions := make([]pendingTokenResolution, 0)

	for _, group := range groups {
		if group == nil || strings.TrimSpace(group.ApiKey) != "" {
			continue
		}

		tokenName := buildSupplierGroupTokenName(supplier.Id, group.UpstreamGroup)
		if key, exists := tokenKeyByName[tokenName]; exists && key != "" {
			if err := saveSupplierGroupToken(group, key); err != nil {
				failures = append(failures, fmt.Sprintf("分组 %s 回填已存在 token 失败: %v", group.UpstreamGroup, err))
				continue
			}
			reused++
			continue
		}

		time.Sleep(upstreamTokenCreateInterval)
		newKey, err := createUpstreamGroupToken(client, supplier, auth, tokenName, group.UpstreamGroup)
		if err != nil {
			failures = append(failures, fmt.Sprintf("分组 %s 创建 token 失败: %v", group.UpstreamGroup, err))
			continue
		}

		if newKey != "" {
			tokenKeyByName[tokenName] = newKey
			if err := saveSupplierGroupToken(group, newKey); err != nil {
				failures = append(failures, fmt.Sprintf("分组 %s 回填新建 token 失败: %v", group.UpstreamGroup, err))
				continue
			}
			created++
			continue
		}

		pendingResolutions = append(pendingResolutions, pendingTokenResolution{
			group:     group,
			tokenName: tokenName,
		})
	}

	if len(pendingResolutions) > 0 {
		refreshedTokens, refreshErr := fetchAllUpstreamTokens(client, supplier, auth)
		if refreshErr != nil {
			for _, pending := range pendingResolutions {
				failures = append(failures, fmt.Sprintf("分组 %s 创建后回查 token 失败: %v", pending.group.UpstreamGroup, refreshErr))
			}
			return created, reused, failures
		}

		latestTokenKeyByName := buildTokenNameKeyMap(refreshedTokens)
		for name, key := range latestTokenKeyByName {
			tokenKeyByName[name] = key
		}

		for _, pending := range pendingResolutions {
			key := tokenKeyByName[pending.tokenName]
			if key == "" {
				failures = append(failures, fmt.Sprintf("分组 %s 创建 token 后未查询到 key", pending.group.UpstreamGroup))
				continue
			}
			if err := saveSupplierGroupToken(pending.group, key); err != nil {
				failures = append(failures, fmt.Sprintf("分组 %s 回填 key 失败: %v", pending.group.UpstreamGroup, err))
				continue
			}
			created++
		}
	}

	return created, reused, failures
}

func fetchUpstreamUserSelf(client *http.Client, supplier *model.Supplier, auth *upstreamAuthInfo) (map[string]interface{}, error) {
	userSelfURL := supplier.BaseURL + "/api/user/self"
	req, err := http.NewRequest(http.MethodGet, userSelfURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %v", err)
	}
	applyUpstreamAuthHeaders(req, auth)

	body, statusCode, err := doUpstreamRequestWith429Retry(client, req, "查询上游余额")
	if err != nil {
		return nil, err
	}
	if statusCode < 200 || statusCode >= 300 {
		msg, _ := parseUpstreamMessageAndRetryAfter(body, nil)
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", statusCode)
		}
		return nil, fmt.Errorf("查询上游余额响应异常: %s", msg)
	}

	var result map[string]interface{}
	if err := common.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %v", err)
	}
	return result, nil
}

func extractUserDataFromSelfResponse(result map[string]interface{}) map[string]interface{} {
	if result == nil {
		return map[string]interface{}{}
	}
	if successAny, exists := result["success"]; exists {
		if success, ok := successAny.(bool); ok && !success {
			return map[string]interface{}{}
		}
	}
	if data, ok := result["data"].(map[string]interface{}); ok && data != nil {
		return data
	}
	return result
}

func extractSupplierBalanceInfo(result map[string]interface{}, fallbackUserID int) supplierBalanceInfo {
	userData := extractUserDataFromSelfResponse(result)
	info := supplierBalanceInfo{
		UpstreamUserID: fallbackUserID,
		Raw:            userData,
	}

	if idValue, ok := getFloatFromMap(userData, "id", "user_id"); ok && idValue > 0 {
		info.UpstreamUserID = int(idValue)
	}
	if username, ok := getStringFromMap(userData, "username"); ok {
		info.Username = username
	}
	if displayName, ok := getStringFromMap(userData, "display_name"); ok {
		info.DisplayName = displayName
	}

	if remain, ok := getFloatFromMap(userData, "quota", "remain_quota", "total_available", "balance", "total_balance"); ok {
		info.RemainingQuota = remain
	}
	if used, ok := getFloatFromMap(userData, "used_quota", "total_used", "used_balance"); ok {
		info.UsedQuota = &used
	}

	if info.UsedQuota != nil {
		total := info.RemainingQuota + *info.UsedQuota
		info.TotalQuota = &total
	} else if total, ok := getFloatFromMap(userData, "total_quota", "total_granted", "total"); ok {
		info.TotalQuota = &total
	}

	return info
}

// ========== Supplier CRUD ==========

func GetAllSuppliers(c *gin.Context) {
	suppliers, err := model.GetAllSuppliers()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    suppliers,
	})
}

func GetSupplier(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的ID"})
		return
	}
	supplier, err := model.GetSupplierById(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "供应商不存在"})
		return
	}
	groups, _ := model.GetSupplierGroups(id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"supplier": supplier,
			"groups":   groups,
		},
	})
}

func GetSupplierGroups(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的ID"})
		return
	}
	if _, err = model.GetSupplierById(id); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "供应商不存在"})
		return
	}
	groups, err := model.GetSupplierGroups(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	supplier, _ := model.GetSupplierById(id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    buildSupplierGroupResponse(supplier, groups),
	})
}

func CreateSupplier(c *gin.Context) {
	var supplier model.Supplier
	if err := c.ShouldBindJSON(&supplier); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的请求参数"})
		return
	}
	if supplier.Name == "" || supplier.BaseURL == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "名称和API地址不能为空"})
		return
	}
	supplier.BaseURL = strings.TrimRight(supplier.BaseURL, "/")
	if err := model.CreateSupplier(&supplier); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "创建成功"})
}

func UpdateSupplier(c *gin.Context) {
	var supplier model.Supplier
	if err := c.ShouldBindJSON(&supplier); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的请求参数"})
		return
	}
	if supplier.Id == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "供应商ID不能为空"})
		return
	}
	supplier.BaseURL = strings.TrimRight(supplier.BaseURL, "/")
	if err := model.UpdateSupplier(&supplier); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "更新成功"})
}

func DeleteSupplier(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的ID"})
		return
	}
	if err := model.DeleteSupplier(id); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "删除成功"})
}

// ========== Group Management ==========

func FetchSupplierGroups(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的ID"})
		return
	}
	supplier, err := model.GetSupplierById(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "供应商不存在"})
		return
	}

	client := &http.Client{Timeout: upstreamRequestTimeout}
	groups, _, totalGroups, added, updated, removed, err := syncSupplierGroupsFromUpstream(client, supplier)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	warnings := inspectSupplierRatioRisks(supplier, groups)

	resp := gin.H{
		"success":         true,
		"partial_success": len(warnings) > 0,
		"message":         fmt.Sprintf("采集完成: %d个分组, 新增%d, 更新%d, 清理%d", totalGroups, added, updated, removed),
		"data":            buildSupplierGroupResponse(supplier, groups),
		"profit_warnings": warnings,
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	c.JSON(http.StatusOK, resp)
}

func FetchSupplierGroupsWithKeys(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的ID"})
		return
	}
	supplier, err := model.GetSupplierById(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "供应商不存在"})
		return
	}

	client := &http.Client{Timeout: upstreamRequestTimeout}
	groups, _, totalGroups, added, updated, removed, err := syncSupplierGroupsFromUpstream(client, supplier)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	auth, authErr := buildUpstreamAuthForSupplier(client, supplier)
	if authErr != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("分组采集成功，但自动创建密钥失败: %v", authErr),
			"data":    buildSupplierGroupResponse(supplier, groups),
		})
		return
	}
	persistUpstreamUserID(supplier, auth.userID)

	created, reused, failures := autoProvisionGroupTokens(client, supplier, groups, auth)
	groups, _ = model.GetSupplierGroups(id)
	riskWarnings := inspectSupplierRatioRisks(supplier, groups)
	warnings := append([]string{}, failures...)
	warnings = append(warnings, riskWarnings...)

	message := fmt.Sprintf("采集完成: %d个分组, 新增%d, 更新%d, 清理%d, 新增密钥%d, 复用密钥%d", totalGroups, added, updated, removed, created, reused)
	if len(failures) > 0 {
		message += fmt.Sprintf("，失败%d项", len(failures))
	}

	resp := gin.H{
		"success":          len(failures) == 0,
		"partial_success":  len(warnings) > 0,
		"message":          message,
		"data":             buildSupplierGroupResponse(supplier, groups),
		"upstream_user_id": auth.userID,
		"key_created":      created,
		"key_reused":       reused,
		"key_failed":       len(failures),
		"profit_warnings":  riskWarnings,
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	c.JSON(http.StatusOK, resp)
}

func UpdateSupplierGroup(c *gin.Context) {
	var group model.SupplierGroup
	if err := c.ShouldBindJSON(&group); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的请求参数"})
		return
	}
	if group.Id == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "分组ID不能为空"})
		return
	}
	if err := model.UpdateSupplierGroup(&group); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "更新成功"})
}

// ========== Markup Management ==========

type MarkupRequest struct {
	Markup float64 `json:"markup"`
}

func UpdateSupplierMarkup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的ID"})
		return
	}

	var req MarkupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的请求参数"})
		return
	}

	supplier, err := model.GetSupplierById(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "供应商不存在"})
		return
	}

	// 更新倍率
	supplier.Markup = req.Markup
	if err := model.UpdateSupplier(supplier); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	// 触发通道状态重算
	enabled, disabled := adjustChannelsByMarkup(supplier)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("倍率已更新为 %.2f, 启用%d个通道, 禁用%d个通道", req.Markup, enabled, disabled),
	})
}

func BulkUpdateMarkup(c *gin.Context) {
	var req MarkupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的请求参数"})
		return
	}

	if err := model.BatchUpdateSupplierMarkup(req.Markup); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	// 遍历所有供应商，重算通道状态
	suppliers, err := model.GetAllSuppliers()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	totalEnabled := 0
	totalDisabled := 0
	for _, supplier := range suppliers {
		supplier.Markup = req.Markup
		enabled, disabled := adjustChannelsByMarkup(supplier)
		totalEnabled += enabled
		totalDisabled += disabled
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("所有供应商倍率已设置为 %.2f, 启用%d个通道, 禁用%d个通道", req.Markup, totalEnabled, totalDisabled),
	})
}

// adjustChannelsByMarkup 根据供应商倍率调整通道状态
// 返回启用数和禁用数
func adjustChannelsByMarkup(supplier *model.Supplier) (int, int) {
	maxRatio, err := model.GetMaxGroupRatioBySupplier(supplier.Id)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to get max group ratio: supplier_id=%d, error=%v", supplier.Id, err))
		return 0, 0
	}

	threshold := maxRatio * supplier.Markup
	channels, err := model.GetChannelsBySupplierID(supplier.Id)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to get channels: supplier_id=%d, error=%v", supplier.Id, err))
		return 0, 0
	}

	// 获取分组倍率映射：local_group -> group_ratio
	groups, _ := model.GetSupplierGroups(supplier.Id)
	groupRatioMap := make(map[string]float64)
	for _, g := range groups {
		if g.LocalGroup != "" {
			groupRatioMap[g.LocalGroup] = g.GroupRatio
		}
	}

	enabled := 0
	disabled := 0
	for _, channel := range channels {
		// 获取通道的分组
		channelGroups := channel.GetGroups()
		maxChannelRatio := 0.0
		for _, cg := range channelGroups {
			if ratio, ok := groupRatioMap[cg]; ok && ratio > maxChannelRatio {
				maxChannelRatio = ratio
			}
		}

		if maxChannelRatio > threshold {
			// 禁用通道
			if channel.Status == common.ChannelStatusEnabled {
				model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusAutoDisabled, fmt.Sprintf("分组倍率 %.2f 超过阈值 %.2f", maxChannelRatio, threshold))
				disabled++
			}
		} else {
			// 启用通道
			if channel.Status == common.ChannelStatusAutoDisabled {
				model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusEnabled, "倍率调整后恢复启用")
				enabled++
			}
		}
	}

	return enabled, disabled
}

// ========== Balance Check ==========

func CheckSupplierBalance(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的ID"})
		return
	}
	supplier, err := model.GetSupplierById(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "供应商不存在"})
		return
	}

	client := &http.Client{Timeout: upstreamRequestTimeout}
	auth, err := buildUpstreamAuthForSupplier(client, supplier)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	result, err := fetchUpstreamUserSelf(client, supplier, auth)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	info := extractSupplierBalanceInfo(result, auth.userID)
	if info.UpstreamUserID > 0 {
		persistUpstreamUserID(supplier, info.UpstreamUserID)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"upstream_user_id": info.UpstreamUserID,
			"username":         info.Username,
			"display_name":     info.DisplayName,
			"remaining_quota":  info.RemainingQuota,
			"used_quota":       info.UsedQuota,
			"total_quota":      info.TotalQuota,
			"raw":              info.Raw,
		},
	})
}

// ========== Sync Logs ==========

func GetSyncLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	supplierID, _ := strconv.Atoi(c.DefaultQuery("supplier_id", "0"))

	logs, total, err := model.GetSyncLogs(pageInfo.GetStartIdx(), pageInfo.GetPageSize(), supplierID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    logs,
		"total":   total,
	})
}

// ========== Channel Creation ==========

func parseStoredEndpointTypes(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	endpointTypes := make([]string, 0)
	seen := make(map[string]struct{})
	appendEndpoint := func(value string) {
		normalized := normalizeEndpointType(value)
		if _, exists := seen[normalized]; exists {
			return
		}
		seen[normalized] = struct{}{}
		endpointTypes = append(endpointTypes, normalized)
	}

	if strings.HasPrefix(raw, "[") {
		var items []string
		if err := common.Unmarshal([]byte(raw), &items); err == nil {
			for _, item := range items {
				appendEndpoint(item)
			}
			if len(endpointTypes) > 0 {
				return endpointTypes
			}
		}
	}

	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		appendEndpoint(part)
	}
	return endpointTypes
}

func getSupplierGroupEndpointTypes(group *model.SupplierGroup) []string {
	if group == nil {
		return []string{string(constant.EndpointTypeOpenAI)}
	}
	if endpointTypes := parseStoredEndpointTypes(group.EndpointTypes); len(endpointTypes) > 0 {
		return endpointTypes
	}
	return []string{normalizeEndpointType(group.EndpointType)}
}

func getSupplierGroupPreferredEndpointType(group *model.SupplierGroup) string {
	return choosePreferredEndpointType(getSupplierGroupEndpointTypes(group))
}

// SyncSupplierFull 一键更新渠道（采集 + 密钥 + 倍率 + 渠道增改删）
func SyncSupplierFull(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的ID"})
		return
	}

	supplier, err := model.GetSupplierById(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "供应商不存在"})
		return
	}

	client := &http.Client{Timeout: upstreamRequestTimeout}
	warnings := make([]string, 0)
	details := make(map[string]interface{})
	steps := make([]gin.H, 0, 6)
	appendStep := func(name string, success bool, startedAt time.Time, message string) {
		step := gin.H{
			"name":    name,
			"success": success,
			"cost_ms": time.Since(startedAt).Milliseconds(),
		}
		if strings.TrimSpace(message) != "" {
			step["message"] = message
		}
		steps = append(steps, step)
	}

	localGroups := getLocalGroups()
	localGroupSet := make(map[string]struct{}, len(localGroups))
	for _, localGroup := range localGroups {
		localGroupSet[localGroup.Name] = struct{}{}
	}

	// Step 1: 采集分组
	stepStartedAt := time.Now()
	groups, upstreamPricingInfo, totalGroups, groupsAdded, groupsUpdated, groupsRemoved, err := syncSupplierGroupsFromUpstream(client, supplier)
	if err != nil {
		appendStep("sync_groups", false, stepStartedAt, err.Error())
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("采集分组失败: %v", err),
			"steps":   steps,
		})
		return
	}
	appendStep("sync_groups", true, stepStartedAt, fmt.Sprintf("共%d个分组", totalGroups))
	details["groups_total"] = totalGroups
	details["groups_added"] = groupsAdded
	details["groups_updated"] = groupsUpdated
	details["groups_removed"] = groupsRemoved

	// Step 2: 生成/回填分组密钥
	stepStartedAt = time.Now()
	keysCreated := 0
	keysReused := 0
	keysFailed := 0
	auth, authErr := buildUpstreamAuthForSupplier(client, supplier)
	if authErr != nil {
		warnings = append(warnings, fmt.Sprintf("认证失败，跳过密钥生成: %v", authErr))
		appendStep("provision_keys", false, stepStartedAt, authErr.Error())
	} else {
		persistUpstreamUserID(supplier, auth.userID)
		created, reused, keyFailures := autoProvisionGroupTokens(client, supplier, groups, auth)
		keysCreated = created
		keysReused = reused
		keysFailed = len(keyFailures)
		if keysFailed > 0 {
			warnings = append(warnings, keyFailures...)
		}
		groups, _ = model.GetSupplierGroups(id)
		appendStep("provision_keys", keysFailed == 0, stepStartedAt, fmt.Sprintf("新增%d, 复用%d, 失败%d", keysCreated, keysReused, keysFailed))
	}
	details["keys_created"] = keysCreated
	details["keys_reused"] = keysReused
	details["keys_failed"] = keysFailed

	// Step 3: 构建智能渠道目标并同步倍率
	stepStartedAt = time.Now()
	resolver := loadSupplierModelResolver()
	channelTargets, targetCountByGroupID, targetWarnings := buildSupplierChannelTargets(supplier, groups, &localGroups, localGroupSet, resolver)
	warnings = append(warnings, targetWarnings...)
	warnings = append(warnings, persistSupplierGroupLocalMappings(groups)...)
	groups, _ = model.GetSupplierGroups(id)
	modelsCreated, modelsUpdated, modelWarnings := syncSupplierModelsToMeta(channelTargets, resolver)
	warnings = append(warnings, modelWarnings...)
	details["models_created"] = modelsCreated
	details["models_updated"] = modelsUpdated

	pricingSynced, pricingWarnings := syncSupplierModelPricing(upstreamPricingInfo, channelTargets)
	warnings = append(warnings, pricingWarnings...)
	details["model_pricing_synced"] = pricingSynced

	candidateRatios := make(map[string]float64)
	for _, target := range channelTargets {
		if strings.TrimSpace(target.LocalGroup) == "" {
			continue
		}
		finalRatio := roundRatio(target.GroupRatio * supplier.Markup)
		if existingRatio, exists := candidateRatios[target.LocalGroup]; !exists || finalRatio > existingRatio {
			candidateRatios[target.LocalGroup] = finalRatio
		}
	}

	updatedRatios, heldWarnings, ratiosHeldForProtection := protectSyncedGroupRatios(candidateRatios)
	warnings = append(warnings, heldWarnings...)

	ratioSyncFailed := false
	if len(updatedRatios) > 0 {
		ratio_setting.BatchUpdateGroupRatios(updatedRatios)
		newGroupRatioJSON := ratio_setting.GroupRatio2JSONString()
		if err := model.UpdateOption("GroupRatio", newGroupRatioJSON); err != nil {
			ratioSyncFailed = true
			warnings = append(warnings, fmt.Sprintf("保存倍率失败: %v", err))
		}
	}
	appendStep("sync_ratios", !ratioSyncFailed, stepStartedAt, fmt.Sprintf("同步%d个, 目标渠道%d个, 模型+%d/~%d, 定价%d个, 保留%d个", len(updatedRatios), len(channelTargets), modelsCreated, modelsUpdated, pricingSynced, ratiosHeldForProtection))
	details["ratios_synced"] = len(updatedRatios)
	details["ratios_skipped_invalid_local_group"] = 0
	details["ratios_held_for_protection"] = ratiosHeldForProtection
	details["channel_targets"] = len(channelTargets)

	// Step 4: 统计未映射分组
	unmappedGroups := make([]map[string]interface{}, 0)
	for _, group := range groups {
		if group == nil || targetCountByGroupID[group.Id] > 0 {
			continue
		}
		unmappedGroups = append(unmappedGroups, map[string]interface{}{
			"id":               group.Id,
			"upstream_group":   group.UpstreamGroup,
			"local_group":      group.LocalGroup,
			"group_ratio":      group.GroupRatio,
			"supported_models": group.SupportedModels,
			"endpoint_type":    group.EndpointType,
			"endpoint_types":   getSupplierGroupEndpointTypes(group),
			"has_api_key":      strings.TrimSpace(group.ApiKey) != "",
		})
	}
	details["unmapped_count"] = len(unmappedGroups)

	// Step 5: 渠道增改删对齐（支持一个上游分组拆分多个本地渠道）
	stepStartedAt = time.Now()
	channelsCreated, channelsUpdated, channelsDeleted, channelWarnings := reconcileSupplierChannels(supplier, channelTargets, updatedRatios)
	warnings = append(warnings, channelWarnings...)
	appendStep("reconcile_channels", true, stepStartedAt, fmt.Sprintf("新增%d, 更新%d, 删除%d", channelsCreated, channelsUpdated, channelsDeleted))

	details["channels_created"] = channelsCreated
	details["channels_updated"] = channelsUpdated
	details["channels_deleted"] = channelsDeleted

	// 重置代理缓存
	service.ResetProxyClientCache()

	// 记录同步日志
	logDetails := fmt.Sprintf("分组:+%d/~%d/-%d, 密钥:+%d/=%d, 倍率:%d, 定价:%d, 渠道:+%d/~%d/-%d, 未映射:%d",
		groupsAdded, groupsUpdated, groupsRemoved, keysCreated, keysReused, len(updatedRatios), pricingSynced, channelsCreated, channelsUpdated, channelsDeleted, len(unmappedGroups))
	model.CreateSyncLog(&model.SupplierGroupSyncLog{
		SupplierID:   id,
		SupplierName: supplier.Name,
		SyncType:     "full_sync",
		Details:      logDetails,
	})

	message := fmt.Sprintf("更新完成: 分组+%d/~%d/-%d, 密钥+%d/=%d, 模型+%d/~%d, 定价同步%d个, 渠道+%d/~%d/-%d, 未映射%d个",
		groupsAdded, groupsUpdated, groupsRemoved, keysCreated, keysReused, modelsCreated, modelsUpdated, pricingSynced, channelsCreated, channelsUpdated, channelsDeleted, len(unmappedGroups))
	if len(unmappedGroups) > 0 {
		message += "（请手动复核未映射分组）"
	}

	c.JSON(http.StatusOK, gin.H{
		"success":         true,
		"partial_success": len(warnings) > 0,
		"message":         message,
		"details":         details,
		"steps":           steps,
		"warnings":        warnings,
		"groups":          buildSupplierGroupResponse(supplier, groups),
		"unmapped_groups": unmappedGroups,
	})
}

// BatchCreateChannelsRequest 批量创建渠道请求
type BatchCreateChannelsRequest struct {
	GroupIDs []int `json:"group_ids"` // 要创建渠道的分组ID列表
}

// BatchCreateChannels 批量创建渠道
func BatchCreateChannels(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的ID"})
		return
	}

	supplier, err := model.GetSupplierById(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "供应商不存在"})
		return
	}

	var req BatchCreateChannelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}

	if len(req.GroupIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "请选择要创建渠道的分组"})
		return
	}

	allGroups, _ := model.GetSupplierGroups(id)
	groupMap := make(map[int]*model.SupplierGroup)
	for _, g := range allGroups {
		groupMap[g.Id] = g
	}

	selectedGroups := make([]*model.SupplierGroup, 0, len(req.GroupIDs))
	warnings := make([]string, 0)
	for _, groupID := range req.GroupIDs {
		group, exists := groupMap[groupID]
		if !exists {
			warnings = append(warnings, fmt.Sprintf("分组ID %d 不存在", groupID))
			continue
		}
		selectedGroups = append(selectedGroups, group)
	}

	localGroups := getLocalGroups()
	localGroupSet := make(map[string]struct{}, len(localGroups))
	for _, localGroup := range localGroups {
		localGroupSet[localGroup.Name] = struct{}{}
	}
	resolver := loadSupplierModelResolver()
	targets, _, targetWarnings := buildSupplierChannelTargets(supplier, selectedGroups, &localGroups, localGroupSet, resolver)
	warnings = append(warnings, targetWarnings...)
	warnings = append(warnings, persistSupplierGroupLocalMappings(selectedGroups)...)

	existingChannels, _ := model.GetChannelsBySupplierID(id)
	existingBySyncKey := make(map[string]struct{}, len(existingChannels))
	for _, ch := range existingChannels {
		if ch == nil {
			continue
		}
		if syncKey := getSupplierChannelSyncKey(ch); syncKey != "" {
			existingBySyncKey[syncKey] = struct{}{}
		}
	}

	created := 0
	skipped := 0
	targetKeys := make([]string, 0, len(targets))
	for syncKey := range targets {
		targetKeys = append(targetKeys, syncKey)
	}
	sort.Strings(targetKeys)

	for _, syncKey := range targetKeys {
		target := targets[syncKey]
		if strings.TrimSpace(target.LocalGroup) == "" {
			warnings = append(warnings, fmt.Sprintf("分组 %s 未映射本地分组，跳过", target.UpstreamGroup))
			skipped++
			continue
		}
		if profitable, currentRatio, requiredRatio := isSupplierTargetProfitable(target, supplier, ratio_setting.GetGroupRatioCopy()); !profitable {
			warnings = append(warnings, fmt.Sprintf("本地分组 %s 当前系统倍率 %.3f 低于该上游所需倍率 %.3f，渠道 %s 已跳过创建以避免亏损", target.LocalGroup, currentRatio, requiredRatio, target.Name))
			skipped++
			continue
		}
		if strings.TrimSpace(target.ApiKey) == "" {
			warnings = append(warnings, fmt.Sprintf("分组 %s 没有API密钥，跳过", target.UpstreamGroup))
			skipped++
			continue
		}
		if _, exists := existingBySyncKey[syncKey]; exists {
			warnings = append(warnings, fmt.Sprintf("渠道 %s 已存在，跳过", target.Name))
			skipped++
			continue
		}

		channel := &model.Channel{
			Type:       target.ChannelType,
			Key:        target.ApiKey,
			Name:       target.Name,
			BaseURL:    &supplier.BaseURL,
			Group:      target.LocalGroup,
			Models:     joinNormalizedModels(target.Models),
			Status:     common.ChannelStatusEnabled,
			SupplierID: id,
		}
		stampSupplierChannelOtherInfo(channel, target)
		if err := channel.Insert(); err != nil {
			warnings = append(warnings, fmt.Sprintf("创建渠道 %s 失败: %v", target.Name, err))
			continue
		}
		created++
		existingBySyncKey[syncKey] = struct{}{}
	}

	service.ResetProxyClientCache()

	message := fmt.Sprintf("批量创建完成: 成功%d个, 跳过%d个", created, skipped)
	if len(warnings) > 0 && created == 0 {
		message = "没有创建任何渠道"
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  created > 0,
		"message":  message,
		"created":  created,
		"skipped":  skipped,
		"warnings": warnings,
	})
}

// BatchMapLocalGroupRequest 批量设置本地分组映射
type BatchMapLocalGroupRequest struct {
	Mappings []struct {
		GroupID    int    `json:"group_id"`
		LocalGroup string `json:"local_group"`
	} `json:"mappings"`
}

// BatchMapLocalGroup 批量设置本地分组映射
func BatchMapLocalGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的ID"})
		return
	}

	var req BatchMapLocalGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}

	if len(req.Mappings) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "请提供映射关系"})
		return
	}

	// 获取所有分组
	allGroups, _ := model.GetSupplierGroups(id)
	groupMap := make(map[int]*model.SupplierGroup)
	for _, g := range allGroups {
		groupMap[g.Id] = g
	}

	var warnings []string
	updated := 0

	for _, mapping := range req.Mappings {
		group, exists := groupMap[mapping.GroupID]
		if !exists {
			warnings = append(warnings, fmt.Sprintf("分组ID %d 不存在", mapping.GroupID))
			continue
		}

		group.LocalGroup = strings.TrimSpace(mapping.LocalGroup)
		if err := model.UpdateSupplierGroup(group); err != nil {
			warnings = append(warnings, fmt.Sprintf("更新分组 %s 失败: %v", group.UpstreamGroup, err))
			continue
		}
		updated++
	}

	message := fmt.Sprintf("批量映射完成: 成功%d个", updated)
	groups, _ := model.GetSupplierGroups(id)
	supplier, _ := model.GetSupplierById(id)

	c.JSON(http.StatusOK, gin.H{
		"success":  updated > 0,
		"message":  message,
		"updated":  updated,
		"warnings": warnings,
		"groups":   buildSupplierGroupResponse(supplier, groups),
	})
}
func GetSupplierProviderRules(c *gin.Context) {
	rules := getSupplierProviderRules()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    rules,
	})
}

func TestSupplierProviderRules(c *gin.Context) {
	var req struct {
		Rules         []supplierProviderRule `json:"rules"`
		UpstreamGroup string                 `json:"upstream_group"`
		Models        []string               `json:"models"`
		LocalGroup    string                 `json:"local_group"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}
	rules := cloneSupplierProviderRules(req.Rules)
	if len(rules) == 0 {
		rules = getSupplierProviderRules()
	}
	resolver := loadSupplierModelResolverWithRules(rules)
	models := normalizeModelNames(req.Models)
	category := detectSupplierCategoryWithRules(rules, req.LocalGroup, req.UpstreamGroup, strings.Join(models, ","))
	if category == "" {
		for _, modelName := range models {
			if category = resolver.DetectCategory(modelName); category != "" {
				break
			}
		}
	}
	rule, _ := getSupplierProviderRuleByCategory(category, rules)
	channelType := inferChannelTypeByModels(models, resolver, 1)
	if channelType == 1 && rule.ChannelType != 0 {
		channelType = rule.ChannelType
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"category":          category,
			"vendor_name":       rule.VendorName,
			"channel_type":      channelType,
			"channel_type_name": constant.GetChannelTypeName(channelType),
			"patterns":          rule.Patterns,
			"models":            models,
			"upstream_group":    strings.TrimSpace(req.UpstreamGroup),
			"local_group":       strings.TrimSpace(req.LocalGroup),
		},
	})
}

func UpdateSupplierProviderRules(c *gin.Context) {
	var req struct {
		Rules []supplierProviderRule `json:"rules"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}
	rules := cloneSupplierProviderRules(req.Rules)
	if len(rules) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "规则不能为空"})
		return
	}
	payload, err := common.Marshal(rules)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("序列化规则失败: %v", err)})
		return
	}
	if err := model.UpdateOption(supplierProviderRulesOptionKey, string(payload)); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("保存规则失败: %v", err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "规则已保存", "data": rules})
}

func RepairSuppliersByProviderRules(c *gin.Context) {
	suppliers, err := model.GetAllSuppliers()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "获取供应商列表失败"})
		return
	}
	if len(suppliers) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "没有供应商数据"})
		return
	}

	resolver := loadSupplierModelResolver()
	localGroups := getLocalGroups()
	localGroupSet := make(map[string]struct{}, len(localGroups))
	for _, localGroup := range localGroups {
		localGroupSet[localGroup.Name] = struct{}{}
	}

	totalDetails := map[string]int{
		"suppliers_total":     len(suppliers),
		"suppliers_processed": 0,
		"models_created":      0,
		"models_updated":      0,
		"channels_created":    0,
		"channels_updated":    0,
		"channels_deleted":    0,
		"channel_targets":     0,
	}
	allWarnings := make([]string, 0)

	for _, supplier := range suppliers {
		groups, err := model.GetSupplierGroups(supplier.Id)
		if err != nil {
			allWarnings = append(allWarnings, fmt.Sprintf("[%s] 读取分组失败: %v", supplier.Name, err))
			continue
		}
		if len(groups) == 0 {
			continue
		}
		totalDetails["suppliers_processed"]++

		targets, _, targetWarnings := buildSupplierChannelTargets(supplier, groups, &localGroups, localGroupSet, resolver)
		for _, warning := range targetWarnings {
			allWarnings = append(allWarnings, fmt.Sprintf("[%s] %s", supplier.Name, warning))
		}
		for _, warning := range persistSupplierGroupLocalMappings(groups) {
			allWarnings = append(allWarnings, fmt.Sprintf("[%s] %s", supplier.Name, warning))
		}

		modelsCreated, modelsUpdated, modelWarnings := syncSupplierModelsToMeta(targets, resolver)
		totalDetails["models_created"] += modelsCreated
		totalDetails["models_updated"] += modelsUpdated
		for _, warning := range modelWarnings {
			allWarnings = append(allWarnings, fmt.Sprintf("[%s] %s", supplier.Name, warning))
		}

		channelsCreated, channelsUpdated, channelsDeleted, channelWarnings := reconcileSupplierChannels(supplier, targets, nil)
		totalDetails["channels_created"] += channelsCreated
		totalDetails["channels_updated"] += channelsUpdated
		totalDetails["channels_deleted"] += channelsDeleted
		totalDetails["channel_targets"] += len(targets)
		for _, warning := range channelWarnings {
			allWarnings = append(allWarnings, fmt.Sprintf("[%s] %s", supplier.Name, warning))
		}
	}

	service.ResetProxyClientCache()
	message := fmt.Sprintf("规则重跑完成: 处理%d个供应商, 模型+%d/~%d, 定价同步%d个, 渠道+%d/~%d/-%d",
		totalDetails["suppliers_processed"],
		totalDetails["models_created"],
		totalDetails["models_updated"],
		totalDetails["model_pricing_synced"],
		totalDetails["channels_created"],
		totalDetails["channels_updated"],
		totalDetails["channels_deleted"],
	)
	c.JSON(http.StatusOK, gin.H{
		"success":         true,
		"partial_success": len(allWarnings) > 0,
		"message":         message,
		"details":         totalDetails,
		"warnings":        allWarnings,
	})
}

func SyncAllSuppliersFull(c *gin.Context) {
	suppliers, err := model.GetAllSuppliers()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "获取供应商列表失败"})
		return
	}

	if len(suppliers) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "没有供应商数据"})
		return
	}

	allWarnings := make([]string, 0)
	totalDetails := map[string]int{
		"suppliers_total":                    len(suppliers),
		"suppliers_processed":                0,
		"groups_total":                       0,
		"groups_added":                       0,
		"groups_updated":                     0,
		"groups_removed":                     0,
		"keys_created":                       0,
		"keys_reused":                        0,
		"keys_failed":                        0,
		"ratios_skipped_invalid_local_group": 0,
		"channels_created":                   0,
		"channels_updated":                   0,
		"channels_deleted":                   0,
		"channel_targets":                    0,
	}
	allRatios := make(map[string]float64)
	localGroups := getLocalGroups()
	localGroupSet := make(map[string]struct{}, len(localGroups))
	for _, localGroup := range localGroups {
		localGroupSet[localGroup.Name] = struct{}{}
	}
	resolver := loadSupplierModelResolver()

	for _, supplier := range suppliers {
		if supplier.Status != common.ChannelStatusEnabled {
			continue
		}
		totalDetails["suppliers_processed"]++
		client := &http.Client{Timeout: upstreamRequestTimeout}

		groups, upstreamPricingInfo, totalGroups, added, updated, removed, syncErr := syncSupplierGroupsFromUpstream(client, supplier)
		if syncErr != nil {
			allWarnings = append(allWarnings, fmt.Sprintf("[%s] 采集失败: %v", supplier.Name, syncErr))
			continue
		}
		totalDetails["groups_total"] += totalGroups
		totalDetails["groups_added"] += added
		totalDetails["groups_updated"] += updated
		totalDetails["groups_removed"] += removed

		auth, authErr := buildUpstreamAuthForSupplier(client, supplier)
		if authErr != nil {
			allWarnings = append(allWarnings, fmt.Sprintf("[%s] 认证失败，跳过密钥生成: %v", supplier.Name, authErr))
		} else {
			persistUpstreamUserID(supplier, auth.userID)
			created, reused, keyFailures := autoProvisionGroupTokens(client, supplier, groups, auth)
			totalDetails["keys_created"] += created
			totalDetails["keys_reused"] += reused
			totalDetails["keys_failed"] += len(keyFailures)
			for _, failure := range keyFailures {
				allWarnings = append(allWarnings, fmt.Sprintf("[%s] %s", supplier.Name, failure))
			}
			groups, _ = model.GetSupplierGroups(supplier.Id)
		}

		channelTargets, _, targetWarnings := buildSupplierChannelTargets(supplier, groups, &localGroups, localGroupSet, resolver)
		for _, warning := range targetWarnings {
			allWarnings = append(allWarnings, fmt.Sprintf("[%s] %s", supplier.Name, warning))
		}
		for _, warning := range persistSupplierGroupLocalMappings(groups) {
			allWarnings = append(allWarnings, fmt.Sprintf("[%s] %s", supplier.Name, warning))
		}
		groups, _ = model.GetSupplierGroups(supplier.Id)
		modelsCreated, modelsUpdated, modelWarnings := syncSupplierModelsToMeta(channelTargets, resolver)
		totalDetails["models_created"] += modelsCreated
		totalDetails["models_updated"] += modelsUpdated
		for _, warning := range modelWarnings {
			allWarnings = append(allWarnings, fmt.Sprintf("[%s] %s", supplier.Name, warning))
		}
		pricingSynced, pricingWarnings := syncSupplierModelPricing(upstreamPricingInfo, channelTargets)
		totalDetails["model_pricing_synced"] += pricingSynced
		for _, warning := range pricingWarnings {
			allWarnings = append(allWarnings, fmt.Sprintf("[%s] %s", supplier.Name, warning))
		}
		totalDetails["channel_targets"] += len(channelTargets)

		for _, target := range channelTargets {
			if strings.TrimSpace(target.LocalGroup) == "" {
				continue
			}
			finalRatio := roundRatio(target.GroupRatio * supplier.Markup)
			if existingRatio, exists := allRatios[target.LocalGroup]; !exists || finalRatio > existingRatio {
				allRatios[target.LocalGroup] = finalRatio
			}
		}

		projectedGroupRatios := mergeProjectedGroupRatios(ratio_setting.GetGroupRatioCopy(), allRatios)
		created, updated, deleted, channelWarnings := reconcileSupplierChannels(supplier, channelTargets, projectedGroupRatios)
		totalDetails["channels_created"] += created
		totalDetails["channels_updated"] += updated
		totalDetails["channels_deleted"] += deleted
		for _, warning := range channelWarnings {
			allWarnings = append(allWarnings, fmt.Sprintf("[%s] %s", supplier.Name, warning))
		}
	}

	allRatios, heldWarnings, ratiosHeldForProtection := protectSyncedGroupRatios(allRatios)
	allWarnings = append(allWarnings, heldWarnings...)
	if len(allRatios) > 0 {
		ratio_setting.BatchUpdateGroupRatios(allRatios)
		newGroupRatioJSON := ratio_setting.GroupRatio2JSONString()
		if err := model.UpdateOption("GroupRatio", newGroupRatioJSON); err != nil {
			allWarnings = append(allWarnings, fmt.Sprintf("保存倍率失败: %v", err))
		}
	}
	totalDetails["ratios_synced"] = len(allRatios)
	totalDetails["ratios_held_for_protection"] = ratiosHeldForProtection

	service.ResetProxyClientCache()

	message := fmt.Sprintf("同步完成: %d个供应商, 分组+%d/~%d/-%d, 模型+%d/~%d, 定价同步%d个, 渠道+%d/~%d/-%d, 倍率%d个",
		totalDetails["suppliers_processed"],
		totalDetails["groups_added"],
		totalDetails["groups_updated"],
		totalDetails["groups_removed"],
		totalDetails["models_created"],
		totalDetails["models_updated"],
		totalDetails["model_pricing_synced"],
		totalDetails["channels_created"],
		totalDetails["channels_updated"],
		totalDetails["channels_deleted"],
		len(allRatios))

	c.JSON(http.StatusOK, gin.H{
		"success":         true,
		"partial_success": len(allWarnings) > 0,
		"message":         message,
		"details":         totalDetails,
		"warnings":        allWarnings,
		"ratios":          allRatios,
	})
}
