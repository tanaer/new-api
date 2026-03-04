package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

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

type FetchGroupsResponse struct {
	UsableGroup []string           `json:"usable_group"`
	GroupRatio  map[string]float64 `json:"group_ratio"`
}

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

	// 从上游获取分组信息
	pricingURL := supplier.BaseURL + "/api/pricing"
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", pricingURL, nil)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("创建请求失败: %v", err)})
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("请求上游失败: %v", err)})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("读取响应失败: %v", err)})
		return
	}

	// 解析上游响应
	var pricingResp map[string]interface{}
	if err := json.Unmarshal(body, &pricingResp); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("解析响应失败: %v", err)})
		return
	}

	// 提取 usable_group（支持 object {name: desc} 和 array [name, ...] 两种格式）
	var usableGroups []string
	if ug, ok := pricingResp["usable_group"]; ok {
		switch ugTyped := ug.(type) {
		case map[string]interface{}:
			// New API 格式: {"group_name": "description", ...}
			for groupName := range ugTyped {
				if groupName != "" { // 跳过空字符串key
					usableGroups = append(usableGroups, groupName)
				}
			}
		case []interface{}:
			// 数组格式: ["group1", "group2", ...]
			for _, g := range ugTyped {
				if gStr, ok := g.(string); ok && gStr != "" {
					usableGroups = append(usableGroups, gStr)
				}
			}
		}
	}

	// 提取 group_ratio
	groupRatios := make(map[string]float64)
	if gr, ok := pricingResp["group_ratio"]; ok {
		if grMap, ok := gr.(map[string]interface{}); ok {
			for k, v := range grMap {
				switch val := v.(type) {
				case float64:
					groupRatios[k] = val
				case json.Number:
					f, _ := val.Float64()
					groupRatios[k] = f
				}
			}
		}
	}

	// 如果 usable_group 为空，尝试从 group_ratio 的 key 中提取
	if len(usableGroups) == 0 {
		for k := range groupRatios {
			if k != "" {
				usableGroups = append(usableGroups, k)
			}
		}
	}

	if len(usableGroups) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "未从上游获取到分组信息"})
		return
	}

	// Upsert 分组
	added := 0
	updated := 0
	for _, groupName := range usableGroups {
		ratio := groupRatios[groupName]
		if ratio == 0 {
			ratio = 1.0
		}
		existing, err := model.GetSupplierGroupByUpstream(id, groupName)
		if err != nil {
			// 不存在，新建
			newGroup := &model.SupplierGroup{
				SupplierID:    id,
				UpstreamGroup: groupName,
				GroupRatio:    ratio,
			}
			if err := model.CreateSupplierGroup(newGroup); err != nil {
				common.SysLog(fmt.Sprintf("failed to create supplier group: %v", err))
				continue
			}
			added++
		} else {
			// 已存在，更新倍率（保留 api_key 和 local_group）
			if existing.GroupRatio != ratio {
				existing.GroupRatio = ratio
				if err := model.UpdateSupplierGroup(existing); err != nil {
					common.SysLog(fmt.Sprintf("failed to update supplier group: %v", err))
				}
				updated++
			}
		}
	}

	// 重新获取分组列表
	groups, _ := model.GetSupplierGroups(id)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("采集完成: %d个分组, 新增%d, 更新%d", len(usableGroups), added, updated),
		"data":    groups,
	})
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

	// 尝试通过上游 API 查询余额
	// 上游如果也是 New API, 需要先登录获取 session，或者用 Cookie/AccessToken
	balanceURL := supplier.BaseURL + "/api/user/self"
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", balanceURL, nil)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("创建请求失败: %v", err)})
		return
	}

	// Cookie 字段实际上可以存放 Cookie 或 AccessToken
	hasAuth := false
	if supplier.Cookie != "" {
		cookie := strings.TrimSpace(supplier.Cookie)
		// 如果以 sk- 或 Bearer 开头，当作 access token
		if strings.HasPrefix(cookie, "sk-") || strings.HasPrefix(cookie, "Bearer ") {
			token := strings.TrimPrefix(cookie, "Bearer ")
			req.Header.Set("Authorization", "Bearer "+token)
		} else {
			// 当作 Cookie
			req.Header.Set("Cookie", cookie)
		}
		// 上游 New API 需要 New-Api-User 头，填 0 即可让它通过基础检测
		req.Header.Set("New-Api-User", "0")
		hasAuth = true
	}
	if !hasAuth {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "未配置凭证信息（Cookie 或 AccessToken），无法查询余额"})
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("请求上游失败: %v", err)})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("读取响应失败: %v", err)})
		return
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("解析响应失败: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// ========== Sync Logs ==========

func GetSyncLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("p", "0"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	supplierID, _ := strconv.Atoi(c.DefaultQuery("supplier_id", "0"))

	logs, total, err := model.GetSyncLogs(page*pageSize, pageSize, supplierID)
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
