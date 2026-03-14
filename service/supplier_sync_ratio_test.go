package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestCollectRequiredLocalGroupRatios_UsesHighestPerLocalGroup(t *testing.T) {
	suppliers := []*model.Supplier{
		{Id: 1, Name: "supplier-a", Markup: 1.1},
		{Id: 2, Name: "supplier-b", Markup: 1.2},
	}
	groupsBySupplier := map[int][]*model.SupplierGroup{
		1: {
			{LocalGroup: "cc-platinum", GroupRatio: 6.0},
			{LocalGroup: "codex-pro", GroupRatio: 3.0},
		},
		2: {
			{LocalGroup: "cc-platinum", GroupRatio: 5.0},
			{LocalGroup: "codex-pro", GroupRatio: 2.0},
			{LocalGroup: "", GroupRatio: 99.0},
		},
	}

	requiredRatios := collectRequiredLocalGroupRatios(suppliers, groupsBySupplier)

	require.Equal(t, 6.6, requiredRatios["cc-platinum"])
	require.Equal(t, 3.3, requiredRatios["codex-pro"])
}

func TestProtectSupplierLocalRatios_PreservesHigherExistingRatio(t *testing.T) {
	currentRatios := map[string]float64{
		"cc-platinum": 6.8,
		"codex-pro":   2.9,
	}
	requiredRatios := map[string]float64{
		"cc-platinum": 6.6,
		"codex-pro":   3.3,
	}

	protectedRatios := protectSupplierLocalRatios(currentRatios, requiredRatios)

	require.Equal(t, 6.8, protectedRatios["cc-platinum"])
	require.Equal(t, 3.3, protectedRatios["codex-pro"])
}

func TestHasSupplierLocalRatioDiff_DetectsChanges(t *testing.T) {
	currentRatios := map[string]float64{
		"cc-platinum": 5.5,
	}
	targetRatios := map[string]float64{
		"cc-platinum": 6.6,
	}

	require.True(t, hasSupplierLocalRatioDiff(currentRatios, targetRatios))
	require.False(t, hasSupplierLocalRatioDiff(targetRatios, targetRatios))
}
