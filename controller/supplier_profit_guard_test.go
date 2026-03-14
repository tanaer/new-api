package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIsSupplierTargetProfitable_DetectsLossRoute(t *testing.T) {
	supplier := &model.Supplier{
		Id:     1,
		Name:   "packycode",
		Markup: 1.1,
	}
	target := supplierChannelTarget{
		Name:       "packycode-claude-officially",
		LocalGroup: "cc-platinum",
		GroupRatio: 6.0,
	}
	currentGroupRatios := map[string]float64{
		"cc-platinum": 5.5,
	}

	profitable, currentRatio, requiredRatio := isSupplierTargetProfitable(target, supplier, currentGroupRatios)
	require.False(t, profitable)
	require.Equal(t, 5.5, currentRatio)
	require.Equal(t, 6.6, requiredRatio)
}

func TestIsSupplierTargetProfitable_AllowsSafeRoute(t *testing.T) {
	supplier := &model.Supplier{
		Id:     1,
		Name:   "packycode",
		Markup: 1.1,
	}
	target := supplierChannelTarget{
		Name:       "packycode-aws-officially",
		LocalGroup: "cc-platinum",
		GroupRatio: 5.0,
	}
	currentGroupRatios := map[string]float64{
		"cc-platinum": 5.5,
	}

	profitable, currentRatio, requiredRatio := isSupplierTargetProfitable(target, supplier, currentGroupRatios)
	require.True(t, profitable)
	require.Equal(t, 5.5, currentRatio)
	require.Equal(t, 5.5, requiredRatio)
}

func TestMergeProjectedGroupRatios_UsesHigherProjectedRatio(t *testing.T) {
	projected := mergeProjectedGroupRatios(
		map[string]float64{
			"cc-platinum": 5.5,
			"cc-basic":    4.2,
		},
		map[string]float64{
			"cc-platinum": 6.6,
			"cc-basic":    4.1,
			"cc-new":      3.3,
		},
	)

	require.Equal(t, 6.6, projected["cc-platinum"])
	require.Equal(t, 4.2, projected["cc-basic"])
	require.Equal(t, 3.3, projected["cc-new"])
}

func TestProtectSupplierChannelFromLoss_MarksChannelAutoDisabled(t *testing.T) {
	originalDB := model.DB
	originalLogDB := model.LOG_DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB = db
	model.LOG_DB = db
	t.Cleanup(func() {
		model.DB = originalDB
		model.LOG_DB = originalLogDB
	})

	channel := &model.Channel{
		Id:     1,
		Name:   "packycode-claude-officially",
		Status: common.ChannelStatusEnabled,
		Group:  "cc-platinum",
		Models: "claude-sonnet-4",
	}
	require.NoError(t, model.DB.Create(channel).Error)
	target := supplierChannelTarget{
		Name:       "packycode-claude-officially",
		LocalGroup: "cc-platinum",
	}

	changed, err := protectSupplierChannelFromLoss(channel, target, 5.5, 6.6)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, common.ChannelStatusAutoDisabled, channel.Status)

	otherInfo := channel.GetOtherInfo()
	require.Equal(t, true, otherInfo["profit_guard"])
	require.Equal(t, 5.5, otherInfo["profit_guard_current_ratio"])
	require.Equal(t, 6.6, otherInfo["profit_guard_required_ratio"])
}

func TestCollectUnderpricedLocalGroupWarnings_DetectsGap(t *testing.T) {
	groupRatios := ratio_setting.GetGroupRatioCopy()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateGroupRatioByJSONString(marshalMapForTest(t, groupRatios))
	})

	_ = ratio_setting.UpdateGroupRatioByJSONString(`{"cc-platinum":5.5}`)
	warnings := collectUnderpricedLocalGroupWarnings(
		map[string]float64{"cc-platinum": 6.6},
		ratio_setting.GetGroupRatioCopy(),
	)

	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "cc-platinum")
	require.Contains(t, warnings[0], "6.600")
}
