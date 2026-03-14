package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalculateClaudeWebSearchQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("claude_web_search_requests", 2)

	quota, calls := CalculateClaudeWebSearchQuota(ctx, 1.1)
	expected := decimal.NewFromInt(11000)

	assert.Equal(t, 2, calls)
	assert.True(t, quota.Equal(expected), "quota=%s expected=%s", quota.String(), expected.String())
}

func TestPostClaudeConsumeQuota_IncludesClaudeWebSearchQuota(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)

	const userID = 11
	const tokenID = 12
	const channelID = 13
	const initQuota = 50000
	const initTokenQuota = 50000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-test-key", initTokenQuota)
	seedChannel(t, channelID)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("token_name", "test_token")
	ctx.Set("claude_web_search_requests", 2)

	relayInfo := &relaycommon.RelayInfo{
		UserId:            userID,
		TokenId:           tokenID,
		TokenKey:          "sk-test-key",
		UsingGroup:        "default",
		StartTime:         time.Now().Add(-2 * time.Second),
		FirstResponseTime: time.Now().Add(-1 * time.Second),
		OriginModelName:   "claude-sonnet-4-6",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId: channelID,
		},
		PriceData: types.PriceData{
			ModelRatio:      1.5,
			CompletionRatio: 5,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio: 1.0,
			},
		},
	}

	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
	}

	PostClaudeConsumeQuota(ctx, relayInfo, usage)

	// Base Claude quota: (1000 + 500*5) * 1.5 = 5250
	// Claude web search surcharge: 10 / 1000 * 500000 * 1.0 * 2 = 10000
	// Total expected quota: 15250
	assert.Equal(t, initQuota-15250, getUserQuota(t, userID))
	assert.Equal(t, initTokenQuota-15250, getTokenRemainQuota(t, tokenID))

	logEntry := getLastLog(t)
	require.NotNil(t, logEntry)
	assert.Equal(t, 15250, logEntry.Quota)
	assert.Contains(t, logEntry.Content, "Claude Web Search 调用 2 次")
	other, err := common.StrToMap(logEntry.Other)
	require.NoError(t, err)
	assert.Equal(t, true, other["web_search"])
	assert.Equal(t, float64(2), other["web_search_call_count"])
	assert.Equal(t, float64(10), other["web_search_price"])

	var channel model.Channel
	require.NoError(t, model.DB.Select("used_quota").Where("id = ?", channelID).First(&channel).Error)
	assert.EqualValues(t, 15250, channel.UsedQuota)
}

func TestPostClaudeConsumeQuota_WithoutClaudeWebSearchKeepsBaseQuota(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)

	const userID = 21
	const tokenID = 22
	const channelID = 23
	const initQuota = 50000
	const initTokenQuota = 50000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-test-key-2", initTokenQuota)
	seedChannel(t, channelID)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("token_name", "test_token")

	relayInfo := &relaycommon.RelayInfo{
		UserId:            userID,
		TokenId:           tokenID,
		TokenKey:          "sk-test-key-2",
		UsingGroup:        "default",
		StartTime:         time.Now().Add(-2 * time.Second),
		FirstResponseTime: time.Now().Add(-1 * time.Second),
		OriginModelName:   "claude-sonnet-4-6",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId: channelID,
		},
		PriceData: types.PriceData{
			ModelRatio:      1.5,
			CompletionRatio: 5,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio: 1.0,
			},
		},
	}

	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
	}

	PostClaudeConsumeQuota(ctx, relayInfo, usage)

	assert.Equal(t, initQuota-5250, getUserQuota(t, userID))
	assert.Equal(t, initTokenQuota-5250, getTokenRemainQuota(t, tokenID))
}

var _ = common.QuotaPerUnit
