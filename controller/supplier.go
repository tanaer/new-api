package controller

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
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
	Groups          map[string]float64    // 分组名 -> 倍率
	GroupModels     map[string][]string   // 分组名 -> 支持的模型列表
	GroupEndpoint   map[string]string     // 分组名 -> 默认通道类型
	UsableGroup     map[string]string     // 分组名 -> 分组显示名
	AllModels       []string              // 所有模型列表
}

// parsePricingResponse 解析上游 pricing 接口，获取完整的分组、模型、通道类型信息
func parsePricingResponse(body []byte) (*UpstreamPricingInfo, error) {
	var pricingResp map[string]interface{}
	if err := common.Unmarshal(body, &pricingResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %v", err)
	}

	info := &UpstreamPricingInfo{
		Groups:        make(map[string]float64),
		GroupModels:   make(map[string][]string),
		GroupEndpoint: make(map[string]string),
		UsableGroup:   make(map[string]string),
		AllModels:     make([]string, 0),
	}

	// 解析 group_ratio
	if gr, ok := pricingResp["group_ratio"]; ok {
		if grMap, ok := gr.(map[string]interface{}); ok {
			for k, v := range grMap {
				switch val := v.(type) {
				case float64:
					info.Groups[k] = val
				case string:
					f, err := strconv.ParseFloat(val, 64)
					if err == nil {
						info.Groups[k] = f
					}
				}
			}
		}
	}

	// 解析 usable_group
	if ug, ok := pricingResp["usable_group"]; ok {
		if ugMap, ok := ug.(map[string]interface{}); ok {
			for k, v := range ugMap {
				if vs, ok := v.(string); ok {
					info.UsableGroup[k] = vs
				}
			}
		}
	}

	// 解析 data 数组，获取每个模型支持的分组和通道类型
	modelGroupMap := make(map[string][]string) // 模型 -> 分组列表
	modelEndpointMap := make(map[string]string) // 模型 -> 通道类型

	if data, ok := pricingResp["data"]; ok {
		if dataArray, ok := data.([]interface{}); ok {
			for _, item := range dataArray {
				if itemMap, ok := item.(map[string]interface{}); ok {
					modelName := ""
					if mn, ok := itemMap["model_name"].(string); ok {
						modelName = mn
					}

					// 获取 enable_groups
					var enableGroups []string
					if eg, ok := itemMap["enable_groups"]; ok {
						if egArray, ok := eg.([]interface{}); ok {
							for _, g := range egArray {
								if gs, ok := g.(string); ok && gs != "" {
									enableGroups = append(enableGroups, gs)
								}
							}
						}
					}

					// 获取 supported_endpoint_types
					var endpointType string
					if se, ok := itemMap["supported_endpoint_types"]; ok {
						if seArray, ok := se.([]interface{}); ok && len(seArray) > 0 {
							if first, ok := seArray[0].(string); ok {
								endpointType = first
							}
						}
					}
					if endpointType == "" {
						endpointType = "openai"
					}

					if modelName != "" {
						modelGroupMap[modelName] = enableGroups
						modelEndpointMap[modelName] = endpointType
						info.AllModels = append(info.AllModels, modelName)
					} else {
						// 空模型名表示全局配置，记录每个分组的默认通道类型
						for _, g := range enableGroups {
							info.GroupEndpoint[g] = endpointType
						}
					}
				}
			}
		}
	}

	// 反转：分组 -> 支持的模型列表
	for model, groups := range modelGroupMap {
		for _, g := range groups {
			info.GroupModels[g] = append(info.GroupModels[g], model)
		}
	}

	// 为没有模型信息的分组设置默认通道类型
	for group := range info.Groups {
		if _, ok := info.GroupEndpoint[group]; !ok {
			info.GroupEndpoint[group] = "openai"
		}
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

// roundRatio 倍率保留3位小数
func roundRatio(ratio float64) float64 {
	return math.Round(ratio*1000) / 1000
}

// getChannelTypeByGroupName 根据分组名称关键字选择渠道类型
// 规则：
// - cc/claude 关键字 → Anthropic Claude (14)
// - codex 关键字 → OpenAI (1)
// - gemini 关键字 → Google Gemini (24)
// - grok 关键字 → xAI (48)
// - 其他 → OpenAI (1)
func getChannelTypeByGroupName(groupName string) int {
	nameLower := strings.ToLower(groupName)

	// 检查关键字
	if strings.Contains(nameLower, "cc") || strings.Contains(nameLower, "claude") {
		return constant.ChannelTypeAnthropic
	}
	if strings.Contains(nameLower, "codex") {
		return constant.ChannelTypeOpenAI // Codex 使用 OpenAI 类型
	}
	if strings.Contains(nameLower, "gemini") {
		return constant.ChannelTypeGemini
	}
	if strings.Contains(nameLower, "grok") {
		return constant.ChannelTypeXai
	}

	// 默认使用 OpenAI 类型
	return constant.ChannelTypeOpenAI
}

// syncSupplierGroupsFromUpstream 同步供应商分组（仅采集，不修改本地 GroupRatio）
func syncSupplierGroupsFromUpstream(client *http.Client, supplier *model.Supplier) ([]*model.SupplierGroup, int, int, int, error) {
	info, err := fetchUpstreamPricing(client, supplier)
	if err != nil {
		return nil, 0, 0, 0, err
	}

	// 获取本地分组用于自动映射
	localGroups := getLocalGroups()

	added := 0
	updated := 0

	for groupName, ratio := range info.Groups {
		if ratio == 0 {
			ratio = 1.0
		}
		// 倍率保留3位小数
		ratio = roundRatio(ratio)

		// 获取该分组支持的模型
		models := info.GroupModels[groupName]
		modelsStr := strings.Join(models, ",")

		// 获取通道类型
		endpointType := info.GroupEndpoint[groupName]
		if endpointType == "" {
			endpointType = "openai"
		}

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

		if changed {
			if err := model.UpdateSupplierGroup(existing); err != nil {
				common.SysLog(fmt.Sprintf("failed to update supplier group: %v", err))
				continue
			}
			updated++
		}
	}

	groups, _ := model.GetSupplierGroups(supplier.Id)
	return groups, len(info.Groups), added, updated, nil
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
	if strings.TrimSpace(group.LocalGroup) == "" {
		group.LocalGroup = group.UpstreamGroup
	}
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
	groups, totalGroups, added, updated, err := syncSupplierGroupsFromUpstream(client, supplier)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("采集完成: %d个分组, 新增%d, 更新%d", totalGroups, added, updated),
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
	groups, totalGroups, added, updated, err := syncSupplierGroupsFromUpstream(client, supplier)
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

	message := fmt.Sprintf("采集完成: %d个分组, 新增%d, 更新%d, 新增密钥%d, 复用密钥%d", totalGroups, added, updated, created, reused)
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

// SyncSupplierFull 一站式同步供应商
// 1. 采集分组（从 pricing 获取完整信息：倍率、模型、通道类型）
// 2. 自动映射本地分组
// 3. 生成/回填分组密钥
// 4. 同步倍率到系统（只同步已映射的本地分组）
// 5. 创建/更新/删除渠道
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
	var warnings []string
	details := make(map[string]interface{})

	// ========== Step 1: 采集分组 ==========
	groups, totalGroups, groupsAdded, groupsUpdated, err := syncSupplierGroupsFromUpstream(client, supplier)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("采集分组失败: %v", err)})
		return
	}
	details["groups_total"] = totalGroups
	details["groups_added"] = groupsAdded
	details["groups_updated"] = groupsUpdated

	// ========== Step 2: 生成/回填分组密钥 ==========
	auth, authErr := buildUpstreamAuthForSupplier(client, supplier)
	if authErr != nil {
		warnings = append(warnings, fmt.Sprintf("认证失败，跳过密钥生成: %v", authErr))
	} else {
		persistUpstreamUserID(supplier, auth.userID)
		created, reused, keyFailures := autoProvisionGroupTokens(client, supplier, groups, auth)
		details["keys_created"] = created
		details["keys_reused"] = reused
		if len(keyFailures) > 0 {
			warnings = append(warnings, keyFailures...)
		}
		// 刷新分组数据
		groups, _ = model.GetSupplierGroups(id)
	}

	// ========== Step 3: 同步倍率到系统（只同步已映射的本地分组）==========
	updatedRatios := make(map[string]float64)
	syncedRatios := 0
	for _, group := range groups {
		if strings.TrimSpace(group.LocalGroup) == "" {
			continue
		}
		// 计算最终倍率 = 供应商分组倍率 × Markup，保留3位小数
		finalRatio := roundRatio(group.GroupRatio * supplier.Markup)
		updatedRatios[group.LocalGroup] = finalRatio
		syncedRatios++
	}

	if len(updatedRatios) > 0 {
		ratio_setting.BatchUpdateGroupRatios(updatedRatios)
		newGroupRatioJSON := ratio_setting.GroupRatio2JSONString()
		if err := model.UpdateOption("GroupRatio", newGroupRatioJSON); err != nil {
			warnings = append(warnings, fmt.Sprintf("保存倍率失败: %v", err))
		}
	}
	details["ratios_synced"] = syncedRatios

	// ========== Step 4: 创建/更新渠道 ==========
	channelsCreated := 0
	channelsUpdated := 0
	channelsDisabled := 0

	// 获取现有渠道
	existingChannels, _ := model.GetChannelsBySupplierID(id)
	existingMap := make(map[string]*model.Channel) // local_group -> channel
	for _, ch := range existingChannels {
		existingMap[ch.Group] = ch
	}

	// 上游分组集合（已映射本地分组的）
	upstreamLocalGroups := make(map[string]bool)

	for _, group := range groups {
		if strings.TrimSpace(group.ApiKey) == "" || strings.TrimSpace(group.LocalGroup) == "" {
			continue
		}
		upstreamLocalGroups[group.LocalGroup] = true

		// 根据分组名称关键字选择渠道类型
		channelType := getChannelTypeByGroupName(group.LocalGroup)

		if existingCh, exists := existingMap[group.LocalGroup]; exists {
			// 更新现有渠道
			changed := false
			if existingCh.Key != group.ApiKey {
				existingCh.Key = group.ApiKey
				changed = true
			}
			if existingCh.Models != group.SupportedModels {
				existingCh.Models = group.SupportedModels
				changed = true
			}
			if existingCh.Type != channelType {
				existingCh.Type = channelType
				changed = true
			}
			if changed {
				if err := existingCh.Update(); err != nil {
					warnings = append(warnings, fmt.Sprintf("更新渠道 %s 失败: %v", group.LocalGroup, err))
				} else {
					channelsUpdated++
				}
			}
		} else {
			// 创建新渠道
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
			} else {
				channelsCreated++
			}
		}
	}

	// ========== Step 5: 禁用上游不存在的渠道 ==========
	for localGroup, ch := range existingMap {
		if !upstreamLocalGroups[localGroup] {
			// 上游已无此分组，禁用渠道
			if ch.Status == common.ChannelStatusEnabled {
				model.UpdateChannelStatus(ch.Id, "", common.ChannelStatusAutoDisabled, "上游分组已移除")
				channelsDisabled++
			}
		}
	}

	details["channels_created"] = channelsCreated
	details["channels_updated"] = channelsUpdated
	details["channels_disabled"] = channelsDisabled

	// 重置代理缓存
	service.ResetProxyClientCache()

	// 记录同步日志
	logDetails := fmt.Sprintf("分组:+%d/~%d, 倍率:%d, 渠道:+%d/~%d/-%d",
		groupsAdded, groupsUpdated, syncedRatios, channelsCreated, channelsUpdated, channelsDisabled)
	model.CreateSyncLog(&model.SupplierGroupSyncLog{
		SupplierID:   id,
		SupplierName: supplier.Name,
		SyncType:     "full_sync",
		Details:      logDetails,
	})

	// 构建响应消息
	message := fmt.Sprintf("同步完成: 分组+%d/~%d, 倍率%d个, 渠道+%d/~%d/-%d",
		groupsAdded, groupsUpdated, syncedRatios, channelsCreated, channelsUpdated, channelsDisabled)

	c.JSON(http.StatusOK, gin.H{
		"success":  len(warnings) == 0,
		"message":  message,
		"details":  details,
		"warnings": warnings,
		"groups":   groups,
	})
}

// SyncAllSuppliersFull 同步所有供应商（一站式）
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

	var allWarnings []string
	totalDetails := make(map[string]int)
	allRatios := make(map[string]float64)

	for _, supplier := range suppliers {
		if supplier.Status != common.ChannelStatusEnabled {
			continue
		}

		client := &http.Client{Timeout: upstreamRequestTimeout}

		// 采集分组
		groups, _, added, updated, err := syncSupplierGroupsFromUpstream(client, supplier)
		if err != nil {
			allWarnings = append(allWarnings, fmt.Sprintf("[%s] 采集失败: %v", supplier.Name, err))
			continue
		}
		totalDetails["groups_added"] += added
		totalDetails["groups_updated"] += updated

		// 生成密钥
		auth, authErr := buildUpstreamAuthForSupplier(client, supplier)
		if authErr == nil {
			persistUpstreamUserID(supplier, auth.userID)
			_, _, keyFailures := autoProvisionGroupTokens(client, supplier, groups, auth)
			allWarnings = append(allWarnings, keyFailures...)
			groups, _ = model.GetSupplierGroups(supplier.Id)
		}

		// 收集倍率
		for _, group := range groups {
			if strings.TrimSpace(group.LocalGroup) == "" {
				continue
			}
			finalRatio := roundRatio(group.GroupRatio * supplier.Markup)
			if existingRatio, exists := allRatios[group.LocalGroup]; exists {
				if finalRatio > existingRatio {
					allRatios[group.LocalGroup] = finalRatio
				}
			} else {
				allRatios[group.LocalGroup] = finalRatio
			}
		}

		// 更新渠道
		existingChannels, _ := model.GetChannelsBySupplierID(supplier.Id)
		existingMap := make(map[string]*model.Channel)
		for _, ch := range existingChannels {
			existingMap[ch.Group] = ch
		}

		upstreamLocalGroups := make(map[string]bool)
		for _, group := range groups {
			if strings.TrimSpace(group.ApiKey) == "" || strings.TrimSpace(group.LocalGroup) == "" {
				continue
			}
			upstreamLocalGroups[group.LocalGroup] = true

			if _, exists := existingMap[group.LocalGroup]; !exists {
				// 根据分组名称关键字选择渠道类型
				channelType := getChannelTypeByGroupName(group.LocalGroup)
				channel := &model.Channel{
					Type:        channelType,
					Key:         group.ApiKey,
					Name:        fmt.Sprintf("%s-%s", supplier.Name, group.LocalGroup),
					BaseURL:     &supplier.BaseURL,
					Group:       group.LocalGroup,
					Models:      group.SupportedModels,
					Status:      common.ChannelStatusEnabled,
					CreatedTime: common.GetTimestamp(),
					SupplierID:  supplier.Id,
				}
				if err := channel.Insert(); err == nil {
					totalDetails["channels_created"]++
				}
			} else {
				totalDetails["channels_updated"]++
			}
		}

		for localGroup, ch := range existingMap {
			if !upstreamLocalGroups[localGroup] && ch.Status == common.ChannelStatusEnabled {
				model.UpdateChannelStatus(ch.Id, "", common.ChannelStatusAutoDisabled, "上游分组已移除")
				totalDetails["channels_disabled"]++
			}
		}
	}

	// 同步倍率到系统
	if len(allRatios) > 0 {
		ratio_setting.BatchUpdateGroupRatios(allRatios)
		newGroupRatioJSON := ratio_setting.GroupRatio2JSONString()
		if err := model.UpdateOption("GroupRatio", newGroupRatioJSON); err != nil {
			allWarnings = append(allWarnings, fmt.Sprintf("保存倍率失败: %v", err))
		}
	}

	service.ResetProxyClientCache()

	message := fmt.Sprintf("同步完成: %d个供应商, 倍率%d个, 渠道+%d/-%d",
		len(suppliers), len(allRatios), totalDetails["channels_created"], totalDetails["channels_disabled"])

	c.JSON(http.StatusOK, gin.H{
		"success":  len(allWarnings) == 0,
		"message":  message,
		"details":  totalDetails,
		"warnings": allWarnings,
		"ratios":   allRatios,
	})
}
