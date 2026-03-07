package controller

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func getEnabledPayMethodMaps(scene string) []map[string]string {
	channels := operation_setting.GetEnabledPaymentChannels(scene)
	result := make([]map[string]string, 0, len(channels))
	for _, channel := range channels {
		channel = operation_setting.ApplyPaymentChannelDefaults(channel)
		if !operation_setting.IsPaymentMethodConfigured(channel.Type) {
			continue
		}
		result = append(result, map[string]string{
			"type":      channel.Type,
			"name":      channel.Name,
			"color":     channel.Color,
			"min_topup": normalizeChannelMinTopup(channel),
			"scope":     channel.Scope,
			"enabled":   strconv.FormatBool(channel.Enabled),
		})
	}
	return result
}

func normalizeChannelMinTopup(channel operation_setting.PaymentChannel) string {
	channel = operation_setting.ApplyPaymentChannelDefaults(channel)
	if strings.TrimSpace(channel.MinTopup) != "" {
		return strings.TrimSpace(channel.MinTopup)
	}
	return strconv.Itoa(operation_setting.MinTopUp)
}
