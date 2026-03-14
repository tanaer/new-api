package ratio_setting

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/types"
)

var defaultGroupRatio = map[string]float64{
	"default": 1,
	"vip":     1,
	"svip":    1,
}

var groupRatioMap = types.NewRWMap[string, float64]()

var defaultGroupGroupRatio = map[string]map[string]float64{
	"vip": {
		"edit_this": 0.9,
	},
}

var groupGroupRatioMap = types.NewRWMap[string, map[string]float64]()

var defaultGroupSpecialUsableGroup = map[string]map[string]string{
	"vip": {
		"append_1":   "vip_special_group_1",
		"-:remove_1": "vip_removed_group_1",
	},
}

type GroupRoutePolicy struct {
	Mode           string  `json:"mode"`
	MinSuccessRate float64 `json:"min_success_rate"`
}

const (
	GroupRouteModeProfitFirst     = "profit_first"
	GroupRouteModeExperienceFirst = "experience_first"
)

type GroupRatioSetting struct {
	GroupRatio              *types.RWMap[string, float64]            `json:"group_ratio"`
	GroupGroupRatio         *types.RWMap[string, map[string]float64] `json:"group_group_ratio"`
	GroupSpecialUsableGroup *types.RWMap[string, map[string]string]  `json:"group_special_usable_group"`
	GroupRoutePolicy        *types.RWMap[string, GroupRoutePolicy]   `json:"group_route_policy"`
}

var groupRatioSetting GroupRatioSetting

func init() {
	groupSpecialUsableGroup := types.NewRWMap[string, map[string]string]()
	groupSpecialUsableGroup.AddAll(defaultGroupSpecialUsableGroup)
	groupRoutePolicy := types.NewRWMap[string, GroupRoutePolicy]()

	groupRatioMap.AddAll(defaultGroupRatio)
	groupGroupRatioMap.AddAll(defaultGroupGroupRatio)

	groupRatioSetting = GroupRatioSetting{
		GroupSpecialUsableGroup: groupSpecialUsableGroup,
		GroupRoutePolicy:        groupRoutePolicy,
		GroupRatio:              groupRatioMap,
		GroupGroupRatio:         groupGroupRatioMap,
	}

	config.GlobalConfig.Register("group_ratio_setting", &groupRatioSetting)
}

func GetGroupRatioSetting() *GroupRatioSetting {
	if groupRatioSetting.GroupSpecialUsableGroup == nil {
		groupRatioSetting.GroupSpecialUsableGroup = types.NewRWMap[string, map[string]string]()
		groupRatioSetting.GroupSpecialUsableGroup.AddAll(defaultGroupSpecialUsableGroup)
	}
	if groupRatioSetting.GroupRoutePolicy == nil {
		groupRatioSetting.GroupRoutePolicy = types.NewRWMap[string, GroupRoutePolicy]()
	}
	return &groupRatioSetting
}

func GetGroupRatioCopy() map[string]float64 {
	return groupRatioMap.ReadAll()
}

func ContainsGroupRatio(name string) bool {
	_, ok := groupRatioMap.Get(name)
	return ok
}

func GroupRatio2JSONString() string {
	return groupRatioMap.MarshalJSONString()
}

func UpdateGroupRatioByJSONString(jsonStr string) error {
	return types.LoadFromJsonString(groupRatioMap, jsonStr)
}

func GetGroupRatio(name string) float64 {
	ratio, ok := groupRatioMap.Get(name)
	if !ok {
		common.SysLog("group ratio not found: " + name)
		return 1
	}
	return ratio
}

func GetGroupGroupRatio(userGroup, usingGroup string) (float64, bool) {
	gp, ok := groupGroupRatioMap.Get(userGroup)
	if !ok {
		return -1, false
	}
	ratio, ok := gp[usingGroup]
	if !ok {
		return -1, false
	}
	return ratio, true
}

func normalizeGroupRoutePolicy(policy GroupRoutePolicy) (GroupRoutePolicy, bool) {
	policy.Mode = strings.TrimSpace(policy.Mode)
	switch policy.Mode {
	case GroupRouteModeProfitFirst, GroupRouteModeExperienceFirst:
	default:
		return GroupRoutePolicy{}, false
	}
	if policy.MinSuccessRate < 0 || policy.MinSuccessRate > 100 {
		return GroupRoutePolicy{}, false
	}
	return policy, true
}

func GetUserGroupRoutePolicy(userGroup string) (GroupRoutePolicy, bool) {
	setting := GetGroupRatioSetting()
	if setting.GroupRoutePolicy == nil {
		return GroupRoutePolicy{}, false
	}
	if userGroup != "" {
		if policy, ok := setting.GroupRoutePolicy.Get(userGroup); ok {
			return normalizeGroupRoutePolicy(policy)
		}
	}
	if policy, ok := setting.GroupRoutePolicy.Get("default"); ok {
		return normalizeGroupRoutePolicy(policy)
	}
	return GroupRoutePolicy{}, false
}

func ValidateGroupRoutePolicyJSON(jsonStr string) error {
	policies := make(map[string]GroupRoutePolicy)
	if err := common.UnmarshalJsonStr(jsonStr, &policies); err != nil {
		return err
	}
	for userGroup, policy := range policies {
		if _, ok := normalizeGroupRoutePolicy(policy); !ok {
			return fmt.Errorf("用户分组 %s 的路由策略无效，mode 仅支持 %s/%s，min_success_rate 需在 0-100 之间",
				userGroup,
				GroupRouteModeProfitFirst,
				GroupRouteModeExperienceFirst,
			)
		}
	}
	return nil
}

func GroupGroupRatio2JSONString() string {
	return groupGroupRatioMap.MarshalJSONString()
}

func UpdateGroupGroupRatioByJSONString(jsonStr string) error {
	return types.LoadFromJsonString(groupGroupRatioMap, jsonStr)
}

func CheckGroupRatio(jsonStr string) error {
	checkGroupRatio := make(map[string]float64)
	err := json.Unmarshal([]byte(jsonStr), &checkGroupRatio)
	if err != nil {
		return err
	}
	for name, ratio := range checkGroupRatio {
		if ratio < 0 {
			return errors.New("group ratio must be not less than 0: " + name)
		}
	}
	return nil
}

// SetGroupRatio 设置单个分组倍率（内存中）
func SetGroupRatio(name string, ratio float64) {
	groupRatioMap.Set(name, ratio)
}

// BatchUpdateGroupRatios 批量更新分组倍率（合并到现有配置，不删除未指定的分组）
func BatchUpdateGroupRatios(ratios map[string]float64) {
	groupRatioMap.AddAll(ratios)
}

// GetGroupRatioMap 获取分组倍率映射表的引用（用于直接操作）
func GetGroupRatioMap() *types.RWMap[string, float64] {
	return groupRatioMap
}
