package service

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

const (
	channelRouteStatsTTL            = 30 * time.Second
	channelRouteCostTTL             = 10 * time.Minute
	channelRouteSuccessRateLookback = 72 * time.Hour
	channelRouteUnknownCostScore    = 1e12
	channelRouteUnknownResponseTime = math.MaxInt32
	channelRouteSupplierUpstreamKey = "supplier_upstream_group"
)

type channelRouteLogStats struct {
	SuccessCount int64
	ErrorCount   int64
	FetchedAt    time.Time
}

type channelRouteCostCacheEntry struct {
	CostScore float64
	FetchedAt time.Time
}

type channelRouteStatsRow struct {
	ChannelId int
	Type      int
	Count     int64
}

type channelRouteCandidate struct {
	Channel      *model.Channel
	SuccessRate  float64
	SampleCount  int64
	CostScore    float64
	ResponseTime int
}

var (
	channelRouteStatsCache sync.Map
	channelRouteCostCache  sync.Map
)

func getChannelByUserGroupRoutePolicy(ctx *gin.Context, userGroup, group, modelName string, retry int) (*model.Channel, error) {
	policy, ok := ratio_setting.GetUserGroupRoutePolicy(userGroup)
	if !ok {
		return model.GetRandomSatisfiedChannel(group, modelName, retry)
	}

	channels, err := model.GetSatisfiedChannels(group, modelName, retry)
	if err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return nil, nil
	}

	now := time.Now()
	stats, err := getChannelRouteStats(channels, now)
	if err != nil {
		return nil, err
	}
	costs, err := getChannelRouteCosts(channels, now)
	if err != nil {
		return nil, err
	}
	candidates := buildChannelRouteCandidates(channels, stats, costs, now)
	selected := pickChannelByRoutePolicy(policy, candidates)
	if selected != nil {
		for _, candidate := range candidates {
			if candidate.Channel != nil && candidate.Channel.Id == selected.Id {
				logger.LogDebug(ctx,
					"selected channel by route policy: userGroup=%s group=%s model=%s mode=%s minSuccessRate=%.2f channelId=%d successRate=%.2f sampleCount=%d costScore=%.3f responseTime=%d",
					userGroup,
					group,
					modelName,
					policy.Mode,
					policy.MinSuccessRate,
					selected.Id,
					candidate.SuccessRate,
					candidate.SampleCount,
					candidate.CostScore,
					candidate.ResponseTime,
				)
				break
			}
		}
	}
	return selected, nil
}

func getChannelRouteStats(channels []*model.Channel, now time.Time) (map[int]channelRouteLogStats, error) {
	result := make(map[int]channelRouteLogStats, len(channels))
	missingIDs := make([]int, 0)
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		if cached, ok := channelRouteStatsCache.Load(channel.Id); ok {
			stats := cached.(channelRouteLogStats)
			if now.Sub(stats.FetchedAt) <= channelRouteStatsTTL {
				result[channel.Id] = stats
				continue
			}
		}
		missingIDs = append(missingIDs, channel.Id)
	}
	if len(missingIDs) == 0 {
		return result, nil
	}

	since := now.Add(-channelRouteSuccessRateLookback).Unix()
	rows := make([]channelRouteStatsRow, 0)
	err := model.LOG_DB.Model(&model.Log{}).
		Select("channel_id, type, COUNT(*) AS count").
		Where("channel_id IN ?", missingIDs).
		Where("created_at >= ?", since).
		Where("type IN ?", []int{model.LogTypeConsume, model.LogTypeError}).
		Group("channel_id, type").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	loaded := make(map[int]channelRouteLogStats, len(missingIDs))
	for _, row := range rows {
		stats := loaded[row.ChannelId]
		if row.Type == model.LogTypeConsume {
			stats.SuccessCount = row.Count
		} else if row.Type == model.LogTypeError {
			stats.ErrorCount = row.Count
		}
		stats.FetchedAt = now
		loaded[row.ChannelId] = stats
	}
	for _, channelID := range missingIDs {
		stats := loaded[channelID]
		stats.FetchedAt = now
		loaded[channelID] = stats
		channelRouteStatsCache.Store(channelID, stats)
		result[channelID] = stats
	}
	return result, nil
}

func getChannelRouteCosts(channels []*model.Channel, now time.Time) (map[int]float64, error) {
	result := make(map[int]float64, len(channels))
	pendingChannels := make([]*model.Channel, 0)
	supplierIDs := make([]int, 0)
	supplierIDSet := make(map[int]struct{})

	for _, channel := range channels {
		if channel == nil {
			continue
		}
		if cached, ok := channelRouteCostCache.Load(channel.Id); ok {
			entry := cached.(channelRouteCostCacheEntry)
			if now.Sub(entry.FetchedAt) <= channelRouteCostTTL {
				result[channel.Id] = entry.CostScore
				continue
			}
		}
		pendingChannels = append(pendingChannels, channel)
		if channel.SupplierID > 0 {
			if _, exists := supplierIDSet[channel.SupplierID]; !exists {
				supplierIDSet[channel.SupplierID] = struct{}{}
				supplierIDs = append(supplierIDs, channel.SupplierID)
			}
		}
	}
	if len(pendingChannels) == 0 {
		return result, nil
	}

	groupsBySupplierKey := make(map[string]float64)
	supplierMarkupByID := make(map[int]float64)
	if len(supplierIDs) > 0 {
		var suppliers []*model.Supplier
		if err := model.DB.Where("id IN ?", supplierIDs).Find(&suppliers).Error; err != nil {
			return nil, err
		}
		for _, supplier := range suppliers {
			if supplier == nil {
				continue
			}
			supplierMarkupByID[supplier.Id] = supplier.Markup
		}
		var supplierGroups []*model.SupplierGroup
		if err := model.DB.Where("supplier_id IN ?", supplierIDs).Find(&supplierGroups).Error; err != nil {
			return nil, err
		}
		for _, group := range supplierGroups {
			if group == nil {
				continue
			}
			key := fmt.Sprintf("%d||%s", group.SupplierID, strings.TrimSpace(group.UpstreamGroup))
			groupsBySupplierKey[key] = group.GroupRatio
		}
	}

	for _, channel := range pendingChannels {
		costScore := channelRouteUnknownCostScore
		if channel.SupplierID > 0 {
			upstreamGroup := ""
			otherInfo := channel.GetOtherInfo()
			if value, ok := otherInfo[channelRouteSupplierUpstreamKey].(string); ok {
				upstreamGroup = strings.TrimSpace(value)
			}
			if upstreamGroup != "" {
				key := fmt.Sprintf("%d||%s", channel.SupplierID, upstreamGroup)
				if ratio, ok := groupsBySupplierKey[key]; ok {
					markup := supplierMarkupByID[channel.SupplierID]
					if markup <= 0 {
						markup = 1
					}
					costScore = ratio * markup
				}
			}
		}
		result[channel.Id] = costScore
		channelRouteCostCache.Store(channel.Id, channelRouteCostCacheEntry{
			CostScore: costScore,
			FetchedAt: now,
		})
	}
	return result, nil
}

func buildChannelRouteCandidates(channels []*model.Channel, stats map[int]channelRouteLogStats, costs map[int]float64, now time.Time) []channelRouteCandidate {
	candidates := make([]channelRouteCandidate, 0, len(channels))
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		channelStats := stats[channel.Id]
		sampleCount := channelStats.SuccessCount + channelStats.ErrorCount
		successRate := 0.0
		if sampleCount > 0 {
			successRate = float64(channelStats.SuccessCount) * 100 / float64(sampleCount)
		}
		responseTime := channel.ResponseTime
		if responseTime <= 0 {
			responseTime = channelRouteUnknownResponseTime
		}
		costScore, ok := costs[channel.Id]
		if !ok {
			costScore = channelRouteUnknownCostScore
		}
		candidates = append(candidates, channelRouteCandidate{
			Channel:      channel,
			SuccessRate:  successRate,
			SampleCount:  sampleCount,
			CostScore:    costScore,
			ResponseTime: responseTime,
		})
	}
	return candidates
}

func pickChannelByRoutePolicy(policy ratio_setting.GroupRoutePolicy, candidates []channelRouteCandidate) *model.Channel {
	if len(candidates) == 0 {
		return nil
	}
	filtered := make([]channelRouteCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.SuccessRate >= policy.MinSuccessRate {
			filtered = append(filtered, candidate)
		}
	}
	if len(filtered) == 0 {
		filtered = candidates
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		left := filtered[i]
		right := filtered[j]
		switch policy.Mode {
		case ratio_setting.GroupRouteModeExperienceFirst:
			if left.SuccessRate != right.SuccessRate {
				return left.SuccessRate > right.SuccessRate
			}
			if left.SampleCount != right.SampleCount {
				return left.SampleCount > right.SampleCount
			}
			if left.ResponseTime != right.ResponseTime {
				return left.ResponseTime < right.ResponseTime
			}
			if left.CostScore != right.CostScore {
				return left.CostScore < right.CostScore
			}
		default:
			if left.CostScore != right.CostScore {
				return left.CostScore < right.CostScore
			}
			if left.SuccessRate != right.SuccessRate {
				return left.SuccessRate > right.SuccessRate
			}
			if left.SampleCount != right.SampleCount {
				return left.SampleCount > right.SampleCount
			}
			if left.ResponseTime != right.ResponseTime {
				return left.ResponseTime < right.ResponseTime
			}
		}
		if left.Channel.GetWeight() != right.Channel.GetWeight() {
			return left.Channel.GetWeight() > right.Channel.GetWeight()
		}
		return left.Channel.Id < right.Channel.Id
	})

	return filtered[0].Channel
}
