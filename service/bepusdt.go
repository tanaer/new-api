package service

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// Bepusdt 创建交易请求
type BepusdtCreateTransactionRequest struct {
	OrderId     string  `json:"order_id"`
	Amount      float64 `json:"amount"`
	NotifyUrl   string  `json:"notify_url"`
	RedirectUrl string  `json:"redirect_url"`
	Signature   string  `json:"signature"`
	TradeType   string  `json:"trade_type,omitempty"`
	Fiat        string  `json:"fiat,omitempty"`
	Name        string  `json:"name,omitempty"`
	Timeout     int     `json:"timeout,omitempty"`
}

// Bepusdt 创建交易响应
type BepusdtCreateTransactionResponse struct {
	StatusCode int    `json:"status_code"`
	Message    string `json:"message"`
	Data       struct {
		Fiat            string  `json:"fiat"`
		TradeId         string  `json:"trade_id"`
		OrderId         string  `json:"order_id"`
		Amount          string  `json:"amount"`
		ActualAmount    string  `json:"actual_amount"`
		Status          int     `json:"status"`
		Token           string  `json:"token"`
		ExpirationTime  int     `json:"expiration_time"`
		PaymentUrl      string  `json:"payment_url"`
	} `json:"data"`
	RequestId string `json:"request_id"`
}

// Bepusdt 回调通知
type BepusdtNotifyRequest struct {
	TradeId            string  `json:"trade_id"`
	OrderId            string  `json:"order_id"`
	Amount             float64 `json:"amount"`
	ActualAmount       float64 `json:"actual_amount"`
	Token              string  `json:"token"`
	BlockTransactionId string  `json:"block_transaction_id"`
	Signature          string  `json:"signature"`
	Status             int     `json:"status"` // 1=等待支付, 2=支付成功, 3=支付超时
}

// BepusdtClient Bepusdt 客户端
type BepusdtClient struct {
	httpClient *http.Client
}

// NewBepusdtClient 创建 Bepusdt 客户端
func NewBepusdtClient() *BepusdtClient {
	return &BepusdtClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// CreateTransaction 创建交易订单
func (c *BepusdtClient) CreateTransaction(req *BepusdtCreateTransactionRequest) (*BepusdtCreateTransactionResponse, error) {
	// 生成签名
	params := map[string]interface{}{
		"order_id":     req.OrderId,
		"amount":       req.Amount,
		"notify_url":   req.NotifyUrl,
		"redirect_url": req.RedirectUrl,
	}
	if req.TradeType != "" {
		params["trade_type"] = req.TradeType
	}
	if req.Fiat != "" {
		params["fiat"] = req.Fiat
	}
	if req.Name != "" {
		params["name"] = req.Name
	}
	if req.Timeout > 0 {
		params["timeout"] = req.Timeout
	}
	req.Signature = operation_setting.BepusdtGenerateSignature(params)

	// 序列化请求体
	body, err := common.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}

	// 发送请求
	url := operation_setting.BepusdtApiUrl + "/api/v1/order/create-transaction"
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response failed: %w", err)
	}

	var result BepusdtCreateTransactionResponse
	if err := common.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response failed: %w", err)
	}

	return &result, nil
}

// IsBepusdtEnabled 检查 Bepusdt 是否已配置
func IsBepusdtEnabled() bool {
	return operation_setting.IsBepusdtEnabled()
}
