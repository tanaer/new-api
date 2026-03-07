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
	upstreamLower := strings.ToLower(upstreamGroup)

	// 确定分组类别
	var category string
	if strings.Contains(upstreamLower, "cc") || strings.Contains(upstreamLower, "claude") {
		category = "cc"
	} else if strings.Contains(upstreamLower, "codex") || strings.Contains(upstreamLower, "openai") {
		category = "codex"
	} else if strings.Contains(upstreamLower, "gemini") {
		category = "gemini"
	} else {
		// 无法分类，不自动映射
		return ""
	}

	// 筛选同类别的本地分组
	var candidates []LocalGroupInfo
	for _, lg := range localGroups {
		lgLower := strings.ToLower(lg.Name)
		if category == "cc" && strings.HasPrefix(lgLower, "cc") {
			candidates = append(candidates, lg)
		} else if category == "codex" && strings.HasPrefix(lgLower, "codex") {
			candidates = append(candidates, lg)
		} else if category == "gemini" && strings.HasPrefix(lgLower, "gemini") {
			candidates = append(candidates, lg)
		}
	}

	if len(candidates) == 0 {
		return ""
	}

	// 找倍率最接近的
	var bestMatch string
	minDiff := -1.0
	for _, c := range candidates {
		diff := c.Ratio - upstreamRatio
		if diff < 0 {
			diff = -diff
		}
		if minDiff < 0 || diff < minDiff {
			minDiff = diff
			bestMatch = c.Name
		}
	}

	return bestMatch
}

// LocalGroupInfo 本地分组信息
type LocalGroupInfo struct {
	Name  string
	Ratio float64
}

// getLocalGroups 获取本地分组列表及其倍率
func getLocalGroups() []LocalGroupInfo {
	ratioMap := ratio_setting.GetGroupRatioCopy()
	result := make([]LocalGroupInfo, 0, len(ratioMap))
	for name, ratio := range ratioMap {
		result = append(result, LocalGroupInfo{Name: name, Ratio: roundRatio(ratio)})
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

// roundRatio 倍率保留3位小数
func roundRatio(ratio float64) float64 {
	return math.Round(ratio*1000) / 1000
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
func syncSupplierGroupsFromUpstream(client *http.Client, supplier *model.Supplier) ([]*model.SupplierGroup, int, int, int, int, error) {
	info, err := fetchUpstreamPricing(client, supplier)
	if err != nil {
		return nil, 0, 0, 0, 0, err
	}
	if len(info.Groups) == 0 {
		return nil, 0, 0, 0, 0, fmt.Errorf("上游 pricing 未返回可用分组，已中止同步以避免误删")
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
	return groups, len(info.Groups), added, updated, removed, nil
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
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    groups,
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
	groups, totalGroups, added, updated, removed, err := syncSupplierGroupsFromUpstream(client, supplier)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("采集完成: %d个分组, 新增%d, 更新%d, 清理%d", totalGroups, added, updated, removed),
		"data":    groups,
	})
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
	groups, totalGroups, added, updated, removed, err := syncSupplierGroupsFromUpstream(client, supplier)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	auth, authErr := buildUpstreamAuthForSupplier(client, supplier)
	if authErr != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("分组采集成功，但自动创建密钥失败: %v", authErr),
			"data":    groups,
		})
		return
	}
	persistUpstreamUserID(supplier, auth.userID)

	created, reused, failures := autoProvisionGroupTokens(client, supplier, groups, auth)
	groups, _ = model.GetSupplierGroups(id)

	message := fmt.Sprintf("采集完成: %d个分组, 新增%d, 更新%d, 清理%d, 新增密钥%d, 复用密钥%d", totalGroups, added, updated, removed, created, reused)
	if len(failures) > 0 {
		message += fmt.Sprintf("，失败%d项", len(failures))
	}

	resp := gin.H{
		"success":          len(failures) == 0,
		"message":          message,
		"data":             groups,
		"upstream_user_id": auth.userID,
		"key_created":      created,
		"key_reused":       reused,
		"key_failed":       len(failures),
	}
	if len(failures) > 0 {
		resp["warnings"] = failures
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
	groups, totalGroups, groupsAdded, groupsUpdated, groupsRemoved, err := syncSupplierGroupsFromUpstream(client, supplier)
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

	// Step 3: 同步倍率到系统（只同步已映射且合法的本地分组，且不自动下调现有倍率）
	stepStartedAt = time.Now()
	candidateRatios := make(map[string]float64)
	ratiosSkippedInvalidLocalGroup := 0
	for _, group := range groups {
		if group == nil {
			continue
		}
		localGroup := strings.TrimSpace(group.LocalGroup)
		if localGroup == "" {
			continue
		}
		if _, exists := localGroupSet[localGroup]; !exists {
			ratiosSkippedInvalidLocalGroup++
			warnings = append(warnings, fmt.Sprintf("本地分组 %s 不存在于系统分组配置，跳过倍率同步（上游分组: %s）", localGroup, group.UpstreamGroup))
			continue
		}
		finalRatio := roundRatio(group.GroupRatio * supplier.Markup)
		if existingRatio, exists := candidateRatios[localGroup]; !exists || finalRatio > existingRatio {
			candidateRatios[localGroup] = finalRatio
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
	appendStep("sync_ratios", !ratioSyncFailed, stepStartedAt, fmt.Sprintf("同步%d个, 跳过%d个, 保留%d个", len(updatedRatios), ratiosSkippedInvalidLocalGroup, ratiosHeldForProtection))
	details["ratios_synced"] = len(updatedRatios)
	details["ratios_skipped_invalid_local_group"] = ratiosSkippedInvalidLocalGroup
	details["ratios_held_for_protection"] = ratiosHeldForProtection

	// Step 4: 统计未映射分组
	unmappedGroups := make([]map[string]interface{}, 0)
	for _, group := range groups {
		if group == nil || strings.TrimSpace(group.LocalGroup) != "" {
			continue
		}
		unmappedGroups = append(unmappedGroups, map[string]interface{}{
			"id":               group.Id,
			"upstream_group":   group.UpstreamGroup,
			"group_ratio":      group.GroupRatio,
			"supported_models": group.SupportedModels,
			"endpoint_type":    group.EndpointType,
			"endpoint_types":   getSupplierGroupEndpointTypes(group),
			"has_api_key":      strings.TrimSpace(group.ApiKey) != "",
		})
	}
	details["unmapped_count"] = len(unmappedGroups)

	// Step 5: 渠道增改删对齐（上游无 -> 硬删除）
	stepStartedAt = time.Now()
	existingChannels, _ := model.GetChannelsBySupplierID(id)
	existingByGroup := make(map[string][]*model.Channel)
	for _, ch := range existingChannels {
		if ch == nil {
			continue
		}
		groupName := strings.TrimSpace(ch.Group)
		existingByGroup[groupName] = append(existingByGroup[groupName], ch)
	}

	targetGroups := make(map[string]*model.SupplierGroup)
	activeMappedGroups := make(map[string]struct{})
	for _, group := range groups {
		if group == nil {
			continue
		}
		localGroup := strings.TrimSpace(group.LocalGroup)
		if localGroup == "" {
			continue
		}
		if _, exists := localGroupSet[localGroup]; !exists {
			continue
		}
		activeMappedGroups[localGroup] = struct{}{}
		if strings.TrimSpace(group.ApiKey) == "" {
			warnings = append(warnings, fmt.Sprintf("本地分组 %s 未获取到可用密钥，跳过本次渠道更新（上游分组: %s）", localGroup, group.UpstreamGroup))
			continue
		}
		if existingGroup, exists := targetGroups[localGroup]; exists {
			if group.GroupRatio > existingGroup.GroupRatio {
				targetGroups[localGroup] = group
			}
			warnings = append(warnings, fmt.Sprintf("本地分组 %s 存在多个上游分组映射，按最高倍率保留（候选: %s）", localGroup, group.UpstreamGroup))
			continue
		}
		targetGroups[localGroup] = group
	}

	channelsCreated := 0
	channelsUpdated := 0
	channelsDeleted := 0

	for localGroup, group := range targetGroups {
		endpointType := getSupplierGroupPreferredEndpointType(group)
		channelType := getChannelTypeByEndpointType(endpointType)
		models := strings.TrimSpace(group.SupportedModels)

		existingList := existingByGroup[localGroup]
		if len(existingList) == 0 {
			channel := &model.Channel{
				Type:        channelType,
				Key:         group.ApiKey,
				Name:        fmt.Sprintf("%s-%s", supplier.Name, localGroup),
				BaseURL:     &supplier.BaseURL,
				Group:       localGroup,
				Models:      models,
				Status:      common.ChannelStatusEnabled,
				CreatedTime: common.GetTimestamp(),
				SupplierID:  id,
			}
			if err := channel.Insert(); err != nil {
				warnings = append(warnings, fmt.Sprintf("创建渠道 %s 失败: %v", localGroup, err))
				continue
			}
			channelsCreated++
			continue
		}

		mainChannel := existingList[0]
		changed := false
		if mainChannel.Key != group.ApiKey {
			mainChannel.Key = group.ApiKey
			changed = true
		}
		if mainChannel.Models != models {
			mainChannel.Models = models
			changed = true
		}
		if mainChannel.Type != channelType {
			mainChannel.Type = channelType
			changed = true
		}
		if mainChannel.BaseURL == nil || strings.TrimSpace(*mainChannel.BaseURL) != supplier.BaseURL {
			mainChannel.BaseURL = &supplier.BaseURL
			changed = true
		}
		if mainChannel.Status != common.ChannelStatusEnabled {
			mainChannel.Status = common.ChannelStatusEnabled
			changed = true
		}
		if changed {
			if err := mainChannel.Update(); err != nil {
				warnings = append(warnings, fmt.Sprintf("更新渠道 %s 失败: %v", localGroup, err))
			} else {
				channelsUpdated++
			}
		}

		// 同组重复渠道清理：保留第一条，其余硬删除
		if len(existingList) > 1 {
			for _, extraChannel := range existingList[1:] {
				if err := extraChannel.Delete(); err != nil {
					warnings = append(warnings, fmt.Sprintf("删除重复渠道 %d(%s) 失败: %v", extraChannel.Id, localGroup, err))
					continue
				}
				channelsDeleted++
			}
		}
	}

	// 删除上游已不存在的渠道（硬删除）
	for localGroup, channelList := range existingByGroup {
		if _, exists := activeMappedGroups[localGroup]; exists {
			continue
		}
		for _, channel := range channelList {
			if err := channel.Delete(); err != nil {
				warnings = append(warnings, fmt.Sprintf("删除渠道 %d(%s) 失败: %v", channel.Id, localGroup, err))
				continue
			}
			channelsDeleted++
		}
	}
	appendStep("reconcile_channels", true, stepStartedAt, fmt.Sprintf("新增%d, 更新%d, 删除%d", channelsCreated, channelsUpdated, channelsDeleted))

	details["channels_created"] = channelsCreated
	details["channels_updated"] = channelsUpdated
	details["channels_deleted"] = channelsDeleted

	// 重置代理缓存
	service.ResetProxyClientCache()

	// 记录同步日志
	logDetails := fmt.Sprintf("分组:+%d/~%d/-%d, 密钥:+%d/=%d, 倍率:%d, 渠道:+%d/~%d/-%d, 未映射:%d",
		groupsAdded, groupsUpdated, groupsRemoved, keysCreated, keysReused, len(updatedRatios), channelsCreated, channelsUpdated, channelsDeleted, len(unmappedGroups))
	model.CreateSyncLog(&model.SupplierGroupSyncLog{
		SupplierID:   id,
		SupplierName: supplier.Name,
		SyncType:     "full_sync",
		Details:      logDetails,
	})

	message := fmt.Sprintf("更新完成: 分组+%d/~%d/-%d, 密钥+%d/=%d, 渠道+%d/~%d/-%d, 未映射%d个",
		groupsAdded, groupsUpdated, groupsRemoved, keysCreated, keysReused, channelsCreated, channelsUpdated, channelsDeleted, len(unmappedGroups))
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
		"groups":          groups,
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

	// 获取现有渠道
	existingChannels, _ := model.GetChannelsBySupplierID(id)
	existingMap := make(map[string]bool) // local_group -> exists
	for _, ch := range existingChannels {
		existingMap[ch.Group] = true
	}

	// 获取所有分组
	allGroups, _ := model.GetSupplierGroups(id)
	groupMap := make(map[int]*model.SupplierGroup)
	for _, g := range allGroups {
		groupMap[g.Id] = g
	}

	var warnings []string
	created := 0
	skipped := 0

	for _, groupID := range req.GroupIDs {
		group, exists := groupMap[groupID]
		if !exists {
			warnings = append(warnings, fmt.Sprintf("分组ID %d 不存在", groupID))
			continue
		}

		// 检查是否有本地分组映射
		if strings.TrimSpace(group.LocalGroup) == "" {
			warnings = append(warnings, fmt.Sprintf("分组 %s 未映射本地分组，跳过", group.UpstreamGroup))
			skipped++
			continue
		}

		// 检查是否有API密钥
		if strings.TrimSpace(group.ApiKey) == "" {
			warnings = append(warnings, fmt.Sprintf("分组 %s 没有API密钥，跳过", group.UpstreamGroup))
			skipped++
			continue
		}

		// 检查渠道是否已存在
		if existingMap[group.LocalGroup] {
			warnings = append(warnings, fmt.Sprintf("本地分组 %s 的渠道已存在，跳过", group.LocalGroup))
			skipped++
			continue
		}

		// 创建渠道
		endpointType := getSupplierGroupPreferredEndpointType(group)
		channelType := getChannelTypeByEndpointType(endpointType)
		channel := &model.Channel{
			Type:        channelType,
			Key:         group.ApiKey,
			Name:        fmt.Sprintf("%s-%s", supplier.Name, group.LocalGroup),
			BaseURL:     &supplier.BaseURL,
			Group:       group.LocalGroup,
			Models:      group.SupportedModels,
			Status:      common.ChannelStatusEnabled,
			CreatedTime: common.GetTimestamp(),
			SupplierID:  id,
		}
		if err := channel.Insert(); err != nil {
			warnings = append(warnings, fmt.Sprintf("创建渠道 %s 失败: %v", group.LocalGroup, err))
			continue
		}
		created++
	}

	// 重置代理缓存
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

	c.JSON(http.StatusOK, gin.H{
		"success":  updated > 0,
		"message":  message,
		"updated":  updated,
		"warnings": warnings,
		"groups":   groups,
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
	}
	allRatios := make(map[string]float64)
	localGroups := getLocalGroups()
	localGroupSet := make(map[string]struct{}, len(localGroups))
	for _, localGroup := range localGroups {
		localGroupSet[localGroup.Name] = struct{}{}
	}

	for _, supplier := range suppliers {
		if supplier.Status != common.ChannelStatusEnabled {
			continue
		}
		totalDetails["suppliers_processed"]++
		client := &http.Client{Timeout: upstreamRequestTimeout}

		groups, totalGroups, added, updated, removed, syncErr := syncSupplierGroupsFromUpstream(client, supplier)
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

		for _, group := range groups {
			if group == nil {
				continue
			}
			localGroup := strings.TrimSpace(group.LocalGroup)
			if localGroup == "" {
				continue
			}
			if _, exists := localGroupSet[localGroup]; !exists {
				totalDetails["ratios_skipped_invalid_local_group"]++
				allWarnings = append(allWarnings, fmt.Sprintf("[%s] 本地分组 %s 不存在于系统分组配置，跳过倍率同步（上游分组: %s）", supplier.Name, localGroup, group.UpstreamGroup))
				continue
			}
			finalRatio := roundRatio(group.GroupRatio * supplier.Markup)
			if existingRatio, exists := allRatios[localGroup]; !exists || finalRatio > existingRatio {
				allRatios[localGroup] = finalRatio
			}
		}

		existingChannels, _ := model.GetChannelsBySupplierID(supplier.Id)
		existingByGroup := make(map[string][]*model.Channel)
		for _, channel := range existingChannels {
			if channel == nil {
				continue
			}
			groupName := strings.TrimSpace(channel.Group)
			existingByGroup[groupName] = append(existingByGroup[groupName], channel)
		}

		targetGroups := make(map[string]*model.SupplierGroup)
		activeMappedGroups := make(map[string]struct{})
		for _, group := range groups {
			if group == nil {
				continue
			}
			localGroup := strings.TrimSpace(group.LocalGroup)
			if localGroup == "" {
				continue
			}
			if _, exists := localGroupSet[localGroup]; !exists {
				continue
			}
			activeMappedGroups[localGroup] = struct{}{}
			if strings.TrimSpace(group.ApiKey) == "" {
				allWarnings = append(allWarnings, fmt.Sprintf("[%s] 本地分组 %s 未获取到可用密钥，跳过本次渠道更新（上游分组: %s）", supplier.Name, localGroup, group.UpstreamGroup))
				continue
			}
			if existingGroup, exists := targetGroups[localGroup]; exists {
				if group.GroupRatio > existingGroup.GroupRatio {
					targetGroups[localGroup] = group
				}
				allWarnings = append(allWarnings, fmt.Sprintf("[%s] 本地分组 %s 存在多个上游分组映射，按最高倍率保留（候选: %s）", supplier.Name, localGroup, group.UpstreamGroup))
				continue
			}
			targetGroups[localGroup] = group
		}

		for localGroup, group := range targetGroups {
			endpointType := getSupplierGroupPreferredEndpointType(group)
			channelType := getChannelTypeByEndpointType(endpointType)
			models := strings.TrimSpace(group.SupportedModels)

			existingList := existingByGroup[localGroup]
			if len(existingList) == 0 {
				channel := &model.Channel{
					Type:        channelType,
					Key:         group.ApiKey,
					Name:        fmt.Sprintf("%s-%s", supplier.Name, localGroup),
					BaseURL:     &supplier.BaseURL,
					Group:       localGroup,
					Models:      models,
					Status:      common.ChannelStatusEnabled,
					CreatedTime: common.GetTimestamp(),
					SupplierID:  supplier.Id,
				}
				if err := channel.Insert(); err != nil {
					allWarnings = append(allWarnings, fmt.Sprintf("[%s] 创建渠道 %s 失败: %v", supplier.Name, localGroup, err))
					continue
				}
				totalDetails["channels_created"]++
				continue
			}

			mainChannel := existingList[0]
			changed := false
			if mainChannel.Key != group.ApiKey {
				mainChannel.Key = group.ApiKey
				changed = true
			}
			if mainChannel.Models != models {
				mainChannel.Models = models
				changed = true
			}
			if mainChannel.Type != channelType {
				mainChannel.Type = channelType
				changed = true
			}
			if mainChannel.BaseURL == nil || strings.TrimSpace(*mainChannel.BaseURL) != supplier.BaseURL {
				mainChannel.BaseURL = &supplier.BaseURL
				changed = true
			}
			if mainChannel.Status != common.ChannelStatusEnabled {
				mainChannel.Status = common.ChannelStatusEnabled
				changed = true
			}
			if changed {
				if err := mainChannel.Update(); err != nil {
					allWarnings = append(allWarnings, fmt.Sprintf("[%s] 更新渠道 %s 失败: %v", supplier.Name, localGroup, err))
				} else {
					totalDetails["channels_updated"]++
				}
			}

			if len(existingList) > 1 {
				for _, extraChannel := range existingList[1:] {
					if err := extraChannel.Delete(); err != nil {
						allWarnings = append(allWarnings, fmt.Sprintf("[%s] 删除重复渠道 %d(%s) 失败: %v", supplier.Name, extraChannel.Id, localGroup, err))
						continue
					}
					totalDetails["channels_deleted"]++
				}
			}
		}

		for localGroup, channelList := range existingByGroup {
			if _, exists := activeMappedGroups[localGroup]; exists {
				continue
			}
			for _, channel := range channelList {
				if err := channel.Delete(); err != nil {
					allWarnings = append(allWarnings, fmt.Sprintf("[%s] 删除渠道 %d(%s) 失败: %v", supplier.Name, channel.Id, localGroup, err))
					continue
				}
				totalDetails["channels_deleted"]++
			}
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

	message := fmt.Sprintf("同步完成: %d个供应商, 分组+%d/~%d/-%d, 渠道+%d/~%d/-%d, 倍率%d个",
		totalDetails["suppliers_processed"],
		totalDetails["groups_added"],
		totalDetails["groups_updated"],
		totalDetails["groups_removed"],
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
