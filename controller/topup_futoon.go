package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type FutoonPayRequest struct {
	Amount        int64  `json:"amount"`
	PaymentMethod string `json:"payment_method"`
}

type SubscriptionFutoonPayRequest struct {
	PlanId        int    `json:"plan_id"`
	PaymentMethod string `json:"payment_method"`
}

func mapFutoonMethod(method string) string {
	switch method {
	case "futoon_alipay":
		return "alipay"
	case "futoon_wxpay":
		return "wxpay"
	default:
		return ""
	}
}

func resolveFutoonReturnURL() string {
	if strings.TrimSpace(setting.FutoonReturnURL) != "" {
		return strings.TrimSpace(setting.FutoonReturnURL)
	}
	return system_setting.ServerAddress + "/console/topup"
}

func resolveFutoonNotifyURL(path string) string {
	configured := strings.TrimSpace(setting.FutoonNotifyURL)
	if configured != "" {
		lowerConfigured := strings.ToLower(configured)
		if strings.HasPrefix(lowerConfigured, "http://") || strings.HasPrefix(lowerConfigured, "https://") {
			return configured
		}
		return strings.TrimRight(configured, "/") + path
	}
	return strings.TrimRight(service.GetCallbackAddress(), "/") + path
}

func resolveFutoonClientIP(c *gin.Context) string {
	clientIP := strings.TrimSpace(c.ClientIP())
	if clientIP == "" || clientIP == "::1" {
		return "127.0.0.1"
	}
	return clientIP
}

func resolveFutoonDevice() string {
	device := strings.TrimSpace(setting.FutoonDefaultDevice)
	if device == "" {
		return "pc"
	}
	return strings.ToLower(device)
}

func RequestFutoonPay(c *gin.Context) {
	var req FutoonPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	if req.Amount < getMinTopup() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", getMinTopup())})
		return
	}
	if !operation_setting.IsPaymentMethodAvailable(req.PaymentMethod, operation_setting.PaymentSceneTopup) || !operation_setting.IsFutoonMethodType(req.PaymentMethod) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付方式未启用"})
		return
	}
	tradeType := mapFutoonMethod(req.PaymentMethod)
	if tradeType == "" {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的支付类型"})
		return
	}
	if !operation_setting.IsFutoonEnabled() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "当前管理员未配置富通支付信息"})
		return
	}

	userId := c.GetInt("id")
	group, err := model.GetUserGroup(userId, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	payMoney := getPayMoney(req.Amount, group)
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	tradeNo := fmt.Sprintf("FTOUSR%dNO%s%d", userId, common.GetRandomString(6), time.Now().Unix())
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
		PaymentMethod: req.PaymentMethod,
		CreateTime:    time.Now().Unix(),
		Status:        common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	client := service.NewFutoonClient()
	resp, _, err := client.CreatePayment(&service.FutoonCreatePaymentRequest{
		Pid:        setting.FutoonPid,
		Type:       tradeType,
		OutTradeNo: tradeNo,
		NotifyURL:  resolveFutoonNotifyURL("/api/user/futoon/notify"),
		ReturnURL:  resolveFutoonReturnURL(),
		Name:       fmt.Sprintf("TOPUP%d", req.Amount),
		Money:      strconv.FormatFloat(payMoney, 'f', 2, 64),
		ClientIP:   resolveFutoonClientIP(c),
		Device:     resolveFutoonDevice(),
	})
	if err != nil {
		topUp.Status = common.TopUpStatusExpired
		topUp.CompleteTime = common.GetTimestamp()
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}
	if resp.Code != 1 {
		topUp.Status = common.TopUpStatusExpired
		topUp.CompleteTime = common.GetTimestamp()
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": resp.Msg})
		return
	}

	paymentURL := service.ResolveFutoonPaymentURL(resp)
	qrcode := strings.TrimSpace(resp.Qrcode)
	if qrcode == "" {
		qrcode = strings.TrimSpace(resp.QRCode)
	}
	data := gin.H{
		"payment_url": paymentURL,
		"pay_url":     paymentURL,
		"payurl":      paymentURL,
		"qrcode":      qrcode,
		"qr_code":     qrcode,
		"order_id":    tradeNo,
		"trade_no":    resp.TradeNo,
	}
	if paymentURL != "" {
		data["url"] = paymentURL
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": data, "url": paymentURL})
}

func FutoonNotify(c *gin.Context) {
	if !operation_setting.IsFutoonEnabled() {
		c.String(http.StatusOK, "fail")
		return
	}
	params := make(map[string]string)
	if c.Request.Method == http.MethodPost {
		if err := c.Request.ParseForm(); err != nil {
			c.String(http.StatusOK, "fail")
			return
		}
		for key := range c.Request.PostForm {
			params[key] = c.Request.PostForm.Get(key)
		}
	} else {
		for key := range c.Request.URL.Query() {
			params[key] = c.Request.URL.Query().Get(key)
		}
	}
	if len(params) == 0 || !service.VerifyFutoonSign(params) {
		c.String(http.StatusOK, "fail")
		return
	}
	if params["trade_status"] != "TRADE_SUCCESS" {
		c.String(http.StatusOK, "fail")
		return
	}
	tradeNo := params["out_trade_no"]
	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)
	topUp := model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil {
		c.String(http.StatusOK, "success")
		return
	}
	if topUp.Status != common.TopUpStatusPending {
		c.String(http.StatusOK, "success")
		return
	}
	if topUp.Status == common.TopUpStatusPending {
		topUp.Status = common.TopUpStatusSuccess
		topUp.CompleteTime = common.GetTimestamp()
		if err := topUp.Update(); err != nil {
			c.String(http.StatusOK, "fail")
			return
		}
		dAmount := decimal.NewFromInt(topUp.Amount)
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		quotaToAdd := int(dAmount.Mul(dQuotaPerUnit).IntPart())
		if err := model.IncreaseUserQuota(topUp.UserId, quotaToAdd, true); err != nil {
			c.String(http.StatusOK, "fail")
			return
		}
		model.RecordLog(topUp.UserId, model.LogTypeTopup, fmt.Sprintf("使用%s充值成功，充值金额: %v，支付金额：%f", operation_setting.GetPaymentMethodDisplayName(topUp.PaymentMethod), logger.LogQuota(quotaToAdd), topUp.Money))
	}
	c.String(http.StatusOK, "success")
}

func SubscriptionRequestFutoonPay(c *gin.Context) {
	var req SubscriptionFutoonPayRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	if !operation_setting.IsPaymentMethodAvailable(req.PaymentMethod, operation_setting.PaymentSceneSubscription) || !operation_setting.IsFutoonMethodType(req.PaymentMethod) {
		common.ApiErrorMsg(c, "支付方式未启用")
		return
	}
	tradeType := mapFutoonMethod(req.PaymentMethod)
	if tradeType == "" {
		common.ApiErrorMsg(c, "不支持的支付类型")
		return
	}
	if !operation_setting.IsFutoonEnabled() {
		common.ApiErrorMsg(c, "当前管理员未配置富通支付信息")
		return
	}

	plan, err := model.GetSubscriptionPlanById(req.PlanId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !plan.Enabled {
		common.ApiErrorMsg(c, "套餐未启用")
		return
	}
	if plan.PriceAmount < 0.01 {
		common.ApiErrorMsg(c, "套餐金额过低")
		return
	}
	userId := c.GetInt("id")
	if plan.MaxPurchasePerUser > 0 {
		count, err := model.CountUserSubscriptionsByPlan(userId, plan.Id)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if count >= int64(plan.MaxPurchasePerUser) {
			common.ApiErrorMsg(c, "已达到该套餐购买上限")
			return
		}
	}
	tradeNo := fmt.Sprintf("FTOSUB%dNO%s%d", userId, common.GetRandomString(6), time.Now().Unix())
	order := &model.SubscriptionOrder{
		UserId:        userId,
		PlanId:        plan.Id,
		Money:         plan.PriceAmount,
		TradeNo:       tradeNo,
		PaymentMethod: req.PaymentMethod,
		CreateTime:    time.Now().Unix(),
		Status:        common.TopUpStatusPending,
	}
	if err := order.Insert(); err != nil {
		common.ApiErrorMsg(c, "创建订单失败")
		return
	}
	client := service.NewFutoonClient()
	resp, _, err := client.CreatePayment(&service.FutoonCreatePaymentRequest{
		Pid:        setting.FutoonPid,
		Type:       tradeType,
		OutTradeNo: tradeNo,
		NotifyURL:  resolveFutoonNotifyURL("/api/subscription/futoon/notify"),
		ReturnURL:  resolveFutoonReturnURL(),
		Name:       fmt.Sprintf("SUB:%s", plan.Title),
		Money:      strconv.FormatFloat(plan.PriceAmount, 'f', 2, 64),
		ClientIP:   resolveFutoonClientIP(c),
		Device:     resolveFutoonDevice(),
	})
	if err != nil {
		_ = model.ExpireSubscriptionOrder(tradeNo)
		common.ApiErrorMsg(c, "拉起支付失败")
		return
	}
	if resp.Code != 1 {
		_ = model.ExpireSubscriptionOrder(tradeNo)
		common.ApiErrorMsg(c, resp.Msg)
		return
	}
	paymentURL := service.ResolveFutoonPaymentURL(resp)
	qrcode := strings.TrimSpace(resp.Qrcode)
	if qrcode == "" {
		qrcode = strings.TrimSpace(resp.QRCode)
	}
	data := gin.H{
		"payment_url": paymentURL,
		"pay_url":     paymentURL,
		"payurl":      paymentURL,
		"qrcode":      qrcode,
		"qr_code":     qrcode,
		"order_id":    tradeNo,
		"trade_no":    resp.TradeNo,
	}
	if paymentURL != "" {
		data["url"] = paymentURL
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": data, "url": paymentURL})
}

func SubscriptionFutoonNotify(c *gin.Context) {
	if !operation_setting.IsFutoonEnabled() {
		c.String(http.StatusOK, "fail")
		return
	}
	params := make(map[string]string)
	if c.Request.Method == http.MethodPost {
		if err := c.Request.ParseForm(); err != nil {
			c.String(http.StatusOK, "fail")
			return
		}
		for key := range c.Request.PostForm {
			params[key] = c.Request.PostForm.Get(key)
		}
	} else {
		for key := range c.Request.URL.Query() {
			params[key] = c.Request.URL.Query().Get(key)
		}
	}
	if len(params) == 0 || !service.VerifyFutoonSign(params) {
		c.String(http.StatusOK, "fail")
		return
	}
	if params["trade_status"] != "TRADE_SUCCESS" {
		c.String(http.StatusOK, "fail")
		return
	}
	tradeNo := params["out_trade_no"]
	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)
	if err := model.CompleteSubscriptionOrder(tradeNo, common.GetJsonString(params)); err != nil {
		c.String(http.StatusOK, "fail")
		return
	}
	c.String(http.StatusOK, "success")
}
