package operation_setting

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/setting"
)

const (
	PaymentSceneTopup        = "topup"
	PaymentSceneSubscription = "subscription"
)

func IsOnlineTopupEnabled() bool {
	for _, channel := range GetPaymentChannelsByScene(PaymentSceneTopup) {
		if IsEpayMethodType(channel.Type) {
			return true
		}
	}
	return false
}

func IsStripeTopupEnabled() bool {
	channel, ok := GetPayMethod("stripe")
	if !ok || !channel.Enabled || !IsPaymentChannelInScope(channel, PaymentSceneTopup) {
		return false
	}
	return setting.StripeApiSecret != "" && setting.StripeWebhookSecret != "" && setting.StripePriceId != ""
}

func IsCreemTopupEnabled() bool {
	channel, ok := GetPayMethod("creem")
	if !ok || !channel.Enabled || !IsPaymentChannelInScope(channel, PaymentSceneTopup) {
		return false
	}
	return setting.CreemApiKey != "" && setting.CreemProducts != "[]"
}

func IsBepusdtTopupEnabled() bool {
	channel, ok := GetPayMethod("bepusdt")
	if !ok || !channel.Enabled || !IsPaymentChannelInScope(channel, PaymentSceneTopup) {
		return false
	}
	return IsBepusdtEnabled()
}

func IsFutoonEnabled() bool {
	return setting.FutoonApiUrl != "" && setting.FutoonPid != "" && setting.FutoonKey != ""
}

func IsFutoonTopupEnabled() bool {
	if !IsFutoonEnabled() {
		return false
	}
	for _, methodType := range []string{"futoon_alipay", "futoon_wxpay"} {
		channel, ok := GetPayMethod(methodType)
		if !ok || !channel.Enabled || !IsPaymentChannelInScope(channel, PaymentSceneTopup) {
			continue
		}
		return true
	}
	return false
}

func IsFutoonMethodType(methodType string) bool {
	switch methodType {
	case "futoon_alipay", "futoon_wxpay":
		return true
	default:
		return false
	}
}

func IsEpayMethodType(methodType string) bool {
	if methodType == "" {
		return false
	}
	if methodType == "stripe" || methodType == "creem" || methodType == "bepusdt" {
		return false
	}
	if IsFutoonMethodType(methodType) {
		return false
	}
	return true
}

func IsPaymentMethodConfigured(methodType string) bool {
	switch methodType {
	case "stripe":
		return setting.StripeApiSecret != "" && setting.StripeWebhookSecret != "" && setting.StripePriceId != ""
	case "creem":
		return setting.CreemApiKey != "" && setting.CreemProducts != "[]"
	case "bepusdt":
		return IsBepusdtEnabled()
	case "futoon_alipay", "futoon_wxpay":
		return IsFutoonEnabled()
	default:
		return PayAddress != "" && EpayId != "" && EpayKey != ""
	}
}

func GetEnabledPaymentChannels(scene string) []PaymentChannel {
	channels := GetPaymentChannelsByScene(scene)
	filtered := make([]PaymentChannel, 0, len(channels))
	for _, channel := range channels {
		if !IsPaymentMethodConfigured(channel.Type) {
			continue
		}
		filtered = append(filtered, ApplyPaymentChannelDefaults(channel))
	}
	return filtered
}

func ApplyPaymentChannelDefaults(channel PaymentChannel) PaymentChannel {
	if strings.TrimSpace(channel.Name) == "" {
		switch channel.Type {
		case "stripe":
			channel.Name = "Stripe"
		case "creem":
			channel.Name = "Creem"
		case "bepusdt":
			channel.Name = "USDT支付"
		case "futoon_alipay":
			channel.Name = "富通支付宝"
		case "futoon_wxpay":
			channel.Name = "富通微信"
		case "alipay":
			channel.Name = "支付宝"
		case "wxpay":
			channel.Name = "微信"
		default:
			channel.Name = channel.Type
		}
	}
	if strings.TrimSpace(channel.Color) == "" {
		switch channel.Type {
		case "stripe":
			channel.Color = "rgba(var(--semi-purple-5), 1)"
		case "creem":
			channel.Color = "rgba(var(--semi-orange-5), 1)"
		case "bepusdt":
			channel.Color = "rgba(38, 161, 123, 1)"
		case "futoon_alipay", "alipay":
			channel.Color = "rgba(var(--semi-blue-5), 1)"
		case "futoon_wxpay", "wxpay":
			channel.Color = "rgba(var(--semi-green-5), 1)"
		default:
			channel.Color = "rgba(var(--semi-primary-5), 1)"
		}
	}
	if strings.TrimSpace(channel.MinTopup) == "" {
		switch channel.Type {
		case "stripe":
			channel.MinTopup = strconv.Itoa(setting.StripeMinTopUp)
		case "bepusdt":
			channel.MinTopup = strconv.Itoa(MinTopUp)
		default:
			channel.MinTopup = strconv.Itoa(MinTopUp)
		}
	}
	channel.Scope = normalizePaymentScope(channel.Scope)
	return channel
}

func GetPaymentMethodDisplayName(methodType string) string {
	channel, ok := GetPayMethod(methodType)
	if ok {
		channel = ApplyPaymentChannelDefaults(channel)
		if strings.TrimSpace(channel.Name) != "" {
			return channel.Name
		}
	}
	switch methodType {
	case "stripe":
		return "Stripe"
	case "creem":
		return "Creem"
	case "bepusdt":
		return "USDT支付"
	case "futoon_alipay":
		return "富通支付宝"
	case "futoon_wxpay":
		return "富通微信"
	case "alipay":
		return "支付宝"
	case "wxpay":
		return "微信"
	default:
		return methodType
	}
}
