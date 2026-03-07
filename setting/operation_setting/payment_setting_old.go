package operation_setting

import (
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

type PaymentChannel struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Color    string `json:"color,omitempty"`
	MinTopup string `json:"min_topup,omitempty"`
	Scope    string `json:"scope,omitempty"`
}

const (
	PaymentScopeTopUp        = "topup"
	PaymentScopeSubscription = "subscription"
	PaymentScopeAll          = "all"
)

var PayAddress = ""
var CustomCallbackAddress = ""
var EpayId = ""
var EpayKey = ""
var Price = 7.3
var MinTopUp = 1
var USDExchangeRate = 7.3

var defaultPayMethods = []PaymentChannel{
	{
		Type:    "alipay",
		Name:    "支付宝",
		Enabled: true,
		Color:   "rgba(var(--semi-blue-5), 1)",
		Scope:   PaymentScopeAll,
	},
	{
		Type:    "wxpay",
		Name:    "微信",
		Enabled: true,
		Color:   "rgba(var(--semi-green-5), 1)",
		Scope:   PaymentScopeAll,
	},
	{
		Type:    "stripe",
		Name:    "Stripe",
		Enabled: true,
		Color:   "rgba(var(--semi-purple-5), 1)",
		Scope:   PaymentScopeAll,
	},
	{
		Type:    "creem",
		Name:    "Creem",
		Enabled: true,
		Color:   "rgba(var(--semi-orange-5), 1)",
		Scope:   PaymentScopeAll,
	},
	{
		Type:    "bepusdt",
		Name:    "USDT支付",
		Enabled: true,
		Color:   "rgba(38, 161, 123, 1)",
		Scope:   PaymentScopeTopUp,
	},
	{
		Type:    "futoon_alipay",
		Name:    "富通支付宝",
		Enabled: false,
		Color:   "rgba(var(--semi-blue-5), 1)",
		Scope:   PaymentScopeAll,
	},
	{
		Type:    "futoon_wxpay",
		Name:    "富通微信",
		Enabled: false,
		Color:   "rgba(var(--semi-green-5), 1)",
		Scope:   PaymentScopeAll,
	},
	{
		Type:     "custom1",
		Name:     "自定义1",
		Enabled:  false,
		Color:    "black",
		MinTopup: "50",
		Scope:    PaymentScopeAll,
	},
}

func normalizePaymentChannels(channels []PaymentChannel) []PaymentChannel {
	defaults := map[string]PaymentChannel{}
	for _, channel := range defaultPaymentChannels() {
		defaults[channel.Type] = channel
	}
	if len(channels) == 0 {
		return defaultPaymentChannels()
	}

	normalized := make([]PaymentChannel, 0, len(channels))
	seen := make(map[string]struct{}, len(channels))
	for _, channel := range channels {
		channel.Type = strings.TrimSpace(channel.Type)
		if channel.Type == "" {
			continue
		}
		if _, ok := seen[channel.Type]; ok {
			continue
		}
		seen[channel.Type] = struct{}{}
		if def, ok := defaults[channel.Type]; ok {
			if strings.TrimSpace(channel.Name) == "" {
				channel.Name = def.Name
			}
			if strings.TrimSpace(channel.Color) == "" {
				channel.Color = def.Color
			}
			if strings.TrimSpace(channel.Scope) == "" {
				channel.Scope = def.Scope
			}
			if strings.TrimSpace(channel.MinTopup) == "" {
				channel.MinTopup = def.MinTopup
			}
		} else {
			if strings.TrimSpace(channel.Name) == "" {
				channel.Name = channel.Type
			}
			if strings.TrimSpace(channel.Scope) == "" {
				channel.Scope = PaymentScopeAll
			}
		}
		channel.Scope = normalizePaymentScope(channel.Scope)
		normalized = append(normalized, channel)
	}
	return normalized
}

func defaultPaymentChannels() []PaymentChannel {
	channels := make([]PaymentChannel, len(defaultPayMethods))
	copy(channels, defaultPayMethods)
	return channels
}

var PayMethods = defaultPaymentChannels()

func normalizePaymentScope(scope string) string {
	switch strings.TrimSpace(scope) {
	case PaymentScopeTopUp, PaymentScopeSubscription, PaymentScopeAll:
		return strings.TrimSpace(scope)
	default:
		return PaymentScopeAll
	}
}

func UpdatePayMethodsByJsonString(jsonString string) error {
	if strings.TrimSpace(jsonString) == "" {
		PayMethods = defaultPaymentChannels()
		return nil
	}

	rawChannels := make([]map[string]any, 0)
	if err := common.UnmarshalJsonStr(jsonString, &rawChannels); err != nil {
		return err
	}

	defaults := map[string]PaymentChannel{}
	for _, channel := range defaultPaymentChannels() {
		defaults[channel.Type] = channel
	}

	channels := make([]PaymentChannel, 0, len(rawChannels))
	for _, item := range rawChannels {
		if item == nil {
			continue
		}
		channelType, _ := item["type"].(string)
		channelType = strings.TrimSpace(channelType)
		if channelType == "" {
			continue
		}
		enabled := true
		if def, ok := defaults[channelType]; ok {
			enabled = def.Enabled
		}
		if rawEnabled, ok := item["enabled"]; ok {
			switch value := rawEnabled.(type) {
			case bool:
				enabled = value
			case string:
				parsed, err := strconv.ParseBool(strings.TrimSpace(value))
				if err == nil {
					enabled = parsed
				}
			case float64:
				enabled = value != 0
			}
		}
		name, _ := item["name"].(string)
		color, _ := item["color"].(string)
		minTopup := ""
		if rawMinTopup, ok := item["min_topup"]; ok {
			switch value := rawMinTopup.(type) {
			case string:
				minTopup = strings.TrimSpace(value)
			case float64:
				minTopup = strconv.FormatFloat(value, 'f', -1, 64)
			case int:
				minTopup = strconv.Itoa(value)
			case int64:
				minTopup = strconv.FormatInt(value, 10)
			}
		}
		scope, _ := item["scope"].(string)
		channels = append(channels, PaymentChannel{
			Type:     channelType,
			Name:     strings.TrimSpace(name),
			Enabled:  enabled,
			Color:    strings.TrimSpace(color),
			MinTopup: strings.TrimSpace(minTopup),
			Scope:    normalizePaymentScope(scope),
		})
	}
	PayMethods = normalizePaymentChannels(channels)
	return nil
}

func PayMethods2JsonString() string {
	jsonBytes, err := common.Marshal(normalizePaymentChannels(PayMethods))
	if err != nil {
		return "[]"
	}
	return string(jsonBytes)
}

func GetPayMethod(methodType string) (PaymentChannel, bool) {
	for _, payMethod := range normalizePaymentChannels(PayMethods) {
		if payMethod.Type == methodType {
			return payMethod, true
		}
	}
	return PaymentChannel{}, false
}

func IsPayMethodEnabled(methodType string) bool {
	payMethod, ok := GetPayMethod(methodType)
	return ok && payMethod.Enabled
}

func ContainsPayMethod(method string) bool {
	_, ok := GetPayMethod(method)
	return ok
}

func IsPaymentMethodAvailable(methodType string, scene string) bool {
	channel, ok := GetPayMethod(methodType)
	if !ok || !channel.Enabled {
		return false
	}
	return IsPaymentChannelInScope(channel, scene)
}

func IsPaymentChannelInScope(channel PaymentChannel, scene string) bool {
	scope := normalizePaymentScope(channel.Scope)
	scene = strings.TrimSpace(scene)
	if scope == PaymentScopeAll || scene == "" {
		return true
	}
	return scope == scene
}

func GetPaymentChannelsByScene(scene string) []PaymentChannel {
	channels := normalizePaymentChannels(PayMethods)
	filtered := make([]PaymentChannel, 0, len(channels))
	for _, channel := range channels {
		if !channel.Enabled {
			continue
		}
		if !IsPaymentChannelInScope(channel, scene) {
			continue
		}
		filtered = append(filtered, channel)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].Type < filtered[j].Type
	})
	return filtered
}
