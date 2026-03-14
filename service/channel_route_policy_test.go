package service

import (
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func seedRouteTestChannel(t *testing.T, channel *model.Channel) {
	t.Helper()
	require.NoError(t, model.DB.Create(channel).Error)
}

func seedRouteTestLog(t *testing.T, log *model.Log) {
	t.Helper()
	require.NoError(t, model.LOG_DB.Create(log).Error)
}

func TestPickChannelByRoutePolicy_ProfitFirstPrefersLowerCost(t *testing.T) {
	candidates := []channelRouteCandidate{
		{
			Channel:      &model.Channel{Id: 1, Name: "cheap"},
			SuccessRate:  86,
			SampleCount:  20,
			CostScore:    4.4,
			ResponseTime: 1200,
		},
		{
			Channel:      &model.Channel{Id: 2, Name: "expensive"},
			SuccessRate:  99,
			SampleCount:  40,
			CostScore:    6.6,
			ResponseTime: 300,
		},
	}

	selected := pickChannelByRoutePolicy(ratio_setting.GroupRoutePolicy{
		Mode:           ratio_setting.GroupRouteModeProfitFirst,
		MinSuccessRate: 85,
	}, candidates)

	require.NotNil(t, selected)
	require.Equal(t, 1, selected.Id)
}

func TestPickChannelByRoutePolicy_ExperienceFirstPrefersHigherSuccessRate(t *testing.T) {
	candidates := []channelRouteCandidate{
		{
			Channel:      &model.Channel{Id: 1, Name: "cheap"},
			SuccessRate:  86,
			SampleCount:  20,
			CostScore:    4.4,
			ResponseTime: 1200,
		},
		{
			Channel:      &model.Channel{Id: 2, Name: "stable"},
			SuccessRate:  99,
			SampleCount:  40,
			CostScore:    6.6,
			ResponseTime: 300,
		},
	}

	selected := pickChannelByRoutePolicy(ratio_setting.GroupRoutePolicy{
		Mode:           ratio_setting.GroupRouteModeExperienceFirst,
		MinSuccessRate: 95,
	}, candidates)

	require.NotNil(t, selected)
	require.Equal(t, 2, selected.Id)
}

func TestGetChannelRouteStats_ComputesSuccessRateFromLogs(t *testing.T) {
	truncate(t)
	now := time.Now()
	channelID := 101
	seedRouteTestChannel(t, &model.Channel{
		Id:     channelID,
		Name:   "route-stat",
		Group:  "default",
		Models: "gpt-4o-mini",
		Status: common.ChannelStatusEnabled,
	})

	for i := 0; i < 3; i++ {
		seedRouteTestLog(t, &model.Log{
			UserId:    1,
			CreatedAt: now.Unix(),
			Type:      model.LogTypeConsume,
			ChannelId: channelID,
			ModelName: "gpt-4o-mini",
			Group:     "default",
		})
	}
	for i := 0; i < 1; i++ {
		seedRouteTestLog(t, &model.Log{
			UserId:    1,
			CreatedAt: now.Unix(),
			Type:      model.LogTypeError,
			ChannelId: channelID,
			ModelName: "gpt-4o-mini",
			Group:     "default",
		})
	}

	channelRouteStatsCache = syncMapZero()
	stats, err := getChannelRouteStats([]*model.Channel{{Id: channelID}}, now)
	require.NoError(t, err)
	require.EqualValues(t, 3, stats[channelID].SuccessCount)
	require.EqualValues(t, 1, stats[channelID].ErrorCount)
}

func TestGetChannelRouteCosts_UsesSupplierMarkupCost(t *testing.T) {
	truncate(t)
	now := time.Now()
	supplier := &model.Supplier{Id: 201, Name: "supplier-a", Markup: 1.1, Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(supplier).Error)
	require.NoError(t, model.DB.Create(&model.SupplierGroup{
		Id:            1,
		SupplierID:    supplier.Id,
		UpstreamGroup: "claude-officially",
		LocalGroup:    "cc-platinum",
		GroupRatio:    6.0,
	}).Error)
	channel := &model.Channel{
		Id:         301,
		Name:       "packycode-claude-officially",
		Status:     common.ChannelStatusEnabled,
		Group:      "cc-platinum",
		Models:     "claude-sonnet-4",
		SupplierID: supplier.Id,
	}
	channel.SetOtherInfo(map[string]interface{}{
		"supplier_upstream_group": "claude-officially",
	})
	seedRouteTestChannel(t, channel)

	channelRouteCostCache = syncMapZero()
	costs, err := getChannelRouteCosts([]*model.Channel{channel}, now)
	require.NoError(t, err)
	require.InDelta(t, 6.6, costs[channel.Id], 0.000001)
}

func syncMapZero() sync.Map {
	return sync.Map{}
}
