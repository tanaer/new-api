package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	supplierRatioSyncInterval  = 6 * time.Hour  // 倍率同步频率
	supplierGroupCheckInterval = 24 * time.Hour // 分组变动检测频率
)

var (
	supplierSyncOnce    sync.Once
	supplierSyncRunning atomic.Bool
)

// StartSupplierSyncTask 启动供应商同步定时任务
func StartSupplierSyncTask() {
	supplierSyncOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		// 分组倍率同步 - 每6小时
		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("supplier ratio sync task started: interval=%s", supplierRatioSyncInterval))
			ticker := time.NewTicker(supplierRatioSyncInterval)
			defer ticker.Stop()

			// 延迟1分钟首次执行，避免启动时并发压力
			time.Sleep(1 * time.Minute)
			runSupplierRatioSync()
			for range ticker.C {
				runSupplierRatioSync()
			}
		})

		// 分组变动检测 - 每24小时
		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("supplier group change detection task started: interval=%s", supplierGroupCheckInterval))
			ticker := time.NewTicker(supplierGroupCheckInterval)
			defer ticker.Stop()

			// 延迟2分钟首次执行
			time.Sleep(2 * time.Minute)
			runSupplierGroupChangeDetection()
			for range ticker.C {
				runSupplierGroupChangeDetection()
			}
		})
	})
}

// runSupplierRatioSync 执行倍率同步
func runSupplierRatioSync() {
	if !supplierSyncRunning.CompareAndSwap(false, true) {
		return
	}
	defer supplierSyncRunning.Store(false)

	ctx := context.Background()
	suppliers, err := model.GetAllEnabledSuppliers()
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("supplier ratio sync: failed to get suppliers: %v", err))
		return
	}

	for _, supplier := range suppliers {
		changes := syncSupplierRatios(supplier)
		if len(changes) > 0 {
			// 记录同步日志
			detailsJSON, _ := json.Marshal(changes)
			syncLog := &model.SupplierGroupSyncLog{
				SupplierID:   supplier.Id,
				SupplierName: supplier.Name,
				SyncType:     "ratio_change",
				Details:      string(detailsJSON),
			}
			if err := model.CreateSyncLog(syncLog); err != nil {
				logger.LogWarn(ctx, fmt.Sprintf("supplier ratio sync: failed to create log: %v", err))
			}

			// 发送通知
			notifyContent := fmt.Sprintf("# 分组倍率变动通知\n\n**供应商**: %s\n\n", supplier.Name)
			for _, change := range changes {
				notifyContent += fmt.Sprintf("- **%s**: %.4f → %.4f\n", change["group"], change["old_ratio"], change["new_ratio"])
			}
			if err := model.SendNotification("分组倍率变动 - "+supplier.Name, notifyContent); err != nil {
				logger.LogWarn(ctx, fmt.Sprintf("supplier ratio sync: failed to send notification: %v", err))
			}

			logger.LogInfo(ctx, fmt.Sprintf("supplier ratio sync: supplier=%s, changes=%d", supplier.Name, len(changes)))
		}
	}
}

type ratioChange = map[string]interface{}

// syncSupplierRatios 同步单个供应商的倍率
func syncSupplierRatios(supplier *model.Supplier) []ratioChange {
	pricingURL := supplier.BaseURL + "/api/pricing"
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(pricingURL)
	if err != nil {
		common.SysLog(fmt.Sprintf("supplier ratio sync: failed to fetch pricing: supplier=%s, error=%v", supplier.Name, err))
		return nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var pricingResp map[string]interface{}
	if err := json.Unmarshal(body, &pricingResp); err != nil {
		return nil
	}

	// 提取 group_ratio
	groupRatios := make(map[string]float64)
	if gr, ok := pricingResp["group_ratio"]; ok {
		if grMap, ok := gr.(map[string]interface{}); ok {
			for k, v := range grMap {
				if f, ok := v.(float64); ok {
					groupRatios[k] = f
				}
			}
		}
	}

	// 对比本地分组倍率
	localGroups, err := model.GetSupplierGroups(supplier.Id)
	if err != nil {
		return nil
	}

	var changes []ratioChange
	for _, lg := range localGroups {
		newRatio, exists := groupRatios[lg.UpstreamGroup]
		if !exists {
			continue
		}
		if lg.GroupRatio != newRatio {
			oldRatio := lg.GroupRatio
			lg.GroupRatio = newRatio
			if err := model.UpdateSupplierGroup(lg); err != nil {
				common.SysLog(fmt.Sprintf("supplier ratio sync: failed to update group: %v", err))
				continue
			}
			changes = append(changes, ratioChange{
				"group":     lg.UpstreamGroup,
				"old_ratio": oldRatio,
				"new_ratio": newRatio,
			})
		}
	}

	return changes
}

// runSupplierGroupChangeDetection 执行分组变动检测
func runSupplierGroupChangeDetection() {
	ctx := context.Background()
	suppliers, err := model.GetAllEnabledSuppliers()
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("supplier group detection: failed to get suppliers: %v", err))
		return
	}

	for _, supplier := range suppliers {
		added, removed := detectGroupChanges(supplier)
		if len(added) > 0 || len(removed) > 0 {
			details := map[string]interface{}{
				"added":   added,
				"removed": removed,
			}
			detailsJSON, _ := json.Marshal(details)

			syncType := "group_changed"
			if len(added) > 0 && len(removed) == 0 {
				syncType = "group_added"
			} else if len(removed) > 0 && len(added) == 0 {
				syncType = "group_removed"
			}

			syncLog := &model.SupplierGroupSyncLog{
				SupplierID:   supplier.Id,
				SupplierName: supplier.Name,
				SyncType:     syncType,
				Details:      string(detailsJSON),
			}
			if err := model.CreateSyncLog(syncLog); err != nil {
				logger.LogWarn(ctx, fmt.Sprintf("supplier group detection: failed to create log: %v", err))
			}

			// 发送通知
			notifyContent := fmt.Sprintf("# 分组变动通知\n\n**供应商**: %s\n\n", supplier.Name)
			if len(added) > 0 {
				notifyContent += "**新增分组**:\n"
				for _, g := range added {
					notifyContent += fmt.Sprintf("- %s\n", g)
				}
			}
			if len(removed) > 0 {
				notifyContent += "\n**移除分组**:\n"
				for _, g := range removed {
					notifyContent += fmt.Sprintf("- %s\n", g)
				}
			}
			notifyContent += "\n请及时到后台重新匹配分组映射。"
			if err := model.SendNotification("分组变动 - "+supplier.Name, notifyContent); err != nil {
				logger.LogWarn(ctx, fmt.Sprintf("supplier group detection: failed to send notification: %v", err))
			}

			logger.LogInfo(ctx, fmt.Sprintf("supplier group detection: supplier=%s, added=%d, removed=%d", supplier.Name, len(added), len(removed)))
		}
	}
}

// detectGroupChanges 检测单个供应商的分组变动
func detectGroupChanges(supplier *model.Supplier) (added []string, removed []string) {
	pricingURL := supplier.BaseURL + "/api/pricing"
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(pricingURL)
	if err != nil {
		return nil, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil
	}

	var pricingResp map[string]interface{}
	if err := json.Unmarshal(body, &pricingResp); err != nil {
		return nil, nil
	}

	// 提取 usable_group（支持 object 和 array 两种格式）
	upstreamGroups := make(map[string]bool)
	if ug, ok := pricingResp["usable_group"]; ok {
		switch ugTyped := ug.(type) {
		case map[string]interface{}:
			for groupName := range ugTyped {
				if groupName != "" {
					upstreamGroups[groupName] = true
				}
			}
		case []interface{}:
			for _, g := range ugTyped {
				if gStr, ok := g.(string); ok && gStr != "" {
					upstreamGroups[gStr] = true
				}
			}
		}
	}
	// 如果 usable_group 为空，从 group_ratio 提取
	if len(upstreamGroups) == 0 {
		if gr, ok := pricingResp["group_ratio"]; ok {
			if grMap, ok := gr.(map[string]interface{}); ok {
				for k := range grMap {
					if k != "" {
						upstreamGroups[k] = true
					}
				}
			}
		}
	}

	// 获取本地分组
	localGroups, err := model.GetSupplierGroups(supplier.Id)
	if err != nil {
		return nil, nil
	}
	localGroupMap := make(map[string]bool)
	for _, lg := range localGroups {
		localGroupMap[lg.UpstreamGroup] = true
	}

	// 检测新增
	for g := range upstreamGroups {
		if !localGroupMap[g] {
			added = append(added, g)
		}
	}

	// 检测移除
	for _, lg := range localGroups {
		if !upstreamGroups[lg.UpstreamGroup] {
			removed = append(removed, lg.UpstreamGroup)
		}
	}

	return added, removed
}
