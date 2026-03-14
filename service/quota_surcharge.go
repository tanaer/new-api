package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// CalculateClaudeWebSearchQuota converts Claude web search calls into quota units.
func CalculateClaudeWebSearchQuota(ctx *gin.Context, groupRatio float64) (decimal.Decimal, int) {
	if ctx == nil {
		return decimal.Zero, 0
	}
	callCount := ctx.GetInt("claude_web_search_requests")
	if callCount <= 0 {
		return decimal.Zero, 0
	}

	quota := decimal.NewFromFloat(operation_setting.GetClaudeWebSearchPricePerThousand()).
		Div(decimal.NewFromInt(1000)).
		Mul(decimal.NewFromFloat(groupRatio)).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit)).
		Mul(decimal.NewFromInt(int64(callCount)))

	return quota, callCount
}
