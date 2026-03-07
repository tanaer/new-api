package controller

import (
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type BepusdtPayRequest struct {
	Amount        int64  `json:"amount"`
	PaymentMethod string `json:"payment_method"` // 用于兼容，实际使用 trade_type
}

func expireBepusdtTopUp(topUp *model.TopUp) {
	if topUp == nil || topUp.Status != common.TopUpStatusPending {
		return
	}
	topUp.Status = common.TopUpStatusExpired
	topUp.CompleteTime = common.GetTimestamp()
	if err := topUp.Update(); err != nil {
		log.Printf("Bepusdt 订单过期更新失败: %v", err)
	}
}

// RequestBepusdt 发起 Bepusdt 支付
func RequestBepusdt(c *gin.Context) {
	var req BepusdtPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	if req.Amount < getMinTopup() {
		c.JSON(200, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", getMinTopup())})
		return
	}

	userId := c.GetInt("id")
	group, err := model.GetUserGroup(userId, true)
	if err != nil {
		c.JSON(200, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}

	payMoney := getPayMoney(req.Amount, group)
	minPayAmount := operation_setting.BepusdtMinPaymentAmount
	if minPayAmount > 0 && payMoney < minPayAmount {
		c.JSON(200, gin.H{"message": "error", "data": fmt.Sprintf("USDT 支付金额不能低于 %.2f 元", minPayAmount)})
		return
	}
	if payMoney < 0.01 {
		c.JSON(200, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	if !operation_setting.IsPaymentMethodAvailable("bepusdt", operation_setting.PaymentSceneTopup) || !service.IsBepusdtEnabled() {
		c.JSON(200, gin.H{"message": "error", "data": "当前管理员未配置或未启用 Bepusdt 支付信息"})
		return
	}

	callBackAddress := service.GetCallbackAddress()
	returnUrl, _ := url.Parse(system_setting.ServerAddress + "/console/log")
	notifyUrl, _ := url.Parse(callBackAddress + "/api/user/bepusdt/notify")

	tradeNo := fmt.Sprintf("%s%d", common.GetRandomString(6), time.Now().Unix())
	tradeNo = fmt.Sprintf("BPU%dNO%s", userId, tradeNo)

	// 创建订单
	amount := req.Amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		dAmount := decimal.NewFromInt(int64(amount))
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		amount = dAmount.Div(dQuotaPerUnit).IntPart()
	}

	topUp := &model.TopUp{
		UserId:        userId,
		Amount:        amount,
		Money:         payMoney,
		TradeNo:       tradeNo,
		PaymentMethod: "bepusdt",
		CreateTime:    time.Now().Unix(),
		Status:        common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		c.JSON(200, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	// 调用 Bepusdt API 创建交易
	client := service.NewBepusdtClient()
	resp, err := client.CreateTransaction(&service.BepusdtCreateTransactionRequest{
		OrderId:     tradeNo,
		Amount:      payMoney,
		NotifyUrl:   notifyUrl.String(),
		RedirectUrl: returnUrl.String(),
		TradeType:   operation_setting.BepusdtTradeType,
		Fiat:        operation_setting.BepusdtFiat,
		Name:        fmt.Sprintf("TOPUP%d", req.Amount),
		Timeout:     operation_setting.BepusdtTimeout,
	})
	if err != nil {
		log.Printf("Bepusdt 创建交易失败: %v", err)
		expireBepusdtTopUp(topUp)
		c.JSON(200, gin.H{"message": "error", "data": "创建支付订单失败"})
		return
	}

	if resp.StatusCode != 200 {
		log.Printf("Bepusdt 创建交易失败: %s", resp.Message)
		expireBepusdtTopUp(topUp)
		c.JSON(200, gin.H{"message": "error", "data": resp.Message})
		return
	}

	paymentURL := strings.TrimSpace(resp.Data.PaymentUrl)
	if paymentURL == "" || strings.EqualFold(paymentURL, "about:blank") {
		log.Printf("Bepusdt 创建交易返回空支付链接: order_id=%s", tradeNo)
		expireBepusdtTopUp(topUp)
		c.JSON(200, gin.H{"message": "error", "data": "支付链接获取失败"})
		return
	}

	c.JSON(200, gin.H{
		"message": "success",
		"data": gin.H{
			"trade_id":        resp.Data.TradeId,
			"order_id":        resp.Data.OrderId,
			"amount":          resp.Data.Amount,
			"actual_amount":   resp.Data.ActualAmount,
			"token":           resp.Data.Token,
			"expiration_time": resp.Data.ExpirationTime,
			"payment_url":     paymentURL,
		},
		"url": paymentURL,
	})
}

// BepusdtNotify 处理 Bepusdt 回调
func BepusdtNotify(c *gin.Context) {
	var req service.BepusdtNotifyRequest
	body, err := c.GetRawData()
	if err != nil {
		log.Printf("Bepusdt 回调读取失败: %v", err)
		c.String(200, "fail")
		return
	}
	if err := common.Unmarshal(body, &req); err != nil {
		log.Printf("Bepusdt 回调解析失败: %v", err)
		c.String(200, "fail")
		return
	}

	log.Printf("Bepusdt 回调: trade_id=%s, order_id=%s, status=%d, amount=%f, actual_amount=%s",
		req.TradeId, req.OrderId, req.Status, req.Amount, req.ActualAmount)

	actualAmount := strings.TrimSpace(req.ActualAmount)

	// 验证签名
	params := map[string]interface{}{
		"trade_id":             req.TradeId,
		"order_id":             req.OrderId,
		"amount":               req.Amount,
		"actual_amount":        actualAmount,
		"token":                req.Token,
		"block_transaction_id": req.BlockTransactionId,
		"status":               req.Status,
	}

	if !operation_setting.BepusdtVerifySignature(params, req.Signature) {
		log.Printf("Bepusdt 回调签名验证失败")
		c.String(200, "fail")
		return
	}

	// 只有支付成功才处理
	if req.Status != 2 {
		log.Printf("Bepusdt 回调状态非成功: %d", req.Status)
		// 对于等待支付和超时，返回 ok 即可
		if req.Status == 1 || req.Status == 3 {
			c.String(200, "ok")
			return
		}
		c.String(200, "fail")
		return
	}

	// 锁定订单
	LockOrder(req.OrderId)
	defer UnlockOrder(req.OrderId)

	// 查询订单
	topUp := model.GetTopUpByTradeNo(req.OrderId)
	if topUp == nil {
		log.Printf("Bepusdt 回调未找到订单: %s", req.OrderId)
		c.String(200, "ok") // 返回 ok 避免重试
		return
	}

	if topUp.Status != common.TopUpStatusPending {
		log.Printf("Bepusdt 回调订单已处理: %s, status=%s", req.OrderId, topUp.Status)
		c.String(200, "ok")
		return
	}

	// 更新订单状态
	topUp.Status = common.TopUpStatusSuccess
	if err := topUp.Update(); err != nil {
		log.Printf("Bepusdt 回调更新订单失败: %v", err)
		c.String(200, "fail")
		return
	}

	// 增加用户额度
	dAmount := decimal.NewFromInt(int64(topUp.Amount))
	dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
	quotaToAdd := int(dAmount.Mul(dQuotaPerUnit).IntPart())

	if err := model.IncreaseUserQuota(topUp.UserId, quotaToAdd, true); err != nil {
		log.Printf("Bepusdt 回调更新用户额度失败: %v", err)
		c.String(200, "fail")
		return
	}

	log.Printf("Bepusdt 回调成功: user_id=%d, quota=%d", topUp.UserId, quotaToAdd)
	model.RecordLog(topUp.UserId, model.LogTypeTopup,
		fmt.Sprintf("使用 Bepusdt 充值成功，充值金额: %v，支付金额: %.2f，交易ID: %s",
			logger.LogQuota(quotaToAdd), topUp.Money, req.TradeId))

	c.String(200, "ok")
}

// GetBepusdtPayInfo 获取 Bepusdt 支付信息（用于前端判断是否显示）
func GetBepusdtPayInfo() gin.H {
	return gin.H{
		"enabled":            service.IsBepusdtEnabled(),
		"trade_type":         operation_setting.BepusdtTradeType,
		"min_payment_amount": operation_setting.BepusdtMinPaymentAmount,
	}
}
