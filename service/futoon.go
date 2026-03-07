package service

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
)

type FutoonCreatePaymentRequest struct {
	Pid        string
	Type       string
	OutTradeNo string
	NotifyURL  string
	ReturnURL  string
	Name       string
	Money      string
	ClientIP   string
	Device     string
}

type FutoonCreatePaymentResponse struct {
	Code       int    `json:"code"`
	Msg        string `json:"msg"`
	TradeNo    string `json:"trade_no"`
	PayURL     string `json:"payurl"`
	PayURLAlt  string `json:"pay_url"`
	PaymentURL string `json:"payment_url"`
	Qrcode     string `json:"qrcode"`
	QRCode     string `json:"qr_code"`
	URL        string `json:"url"`
}

type FutoonNotify struct {
	Pid         string `form:"pid" json:"pid"`
	TradeNo     string `form:"trade_no" json:"trade_no"`
	OutTradeNo  string `form:"out_trade_no" json:"out_trade_no"`
	Type        string `form:"type" json:"type"`
	Name        string `form:"name" json:"name"`
	Money       string `form:"money" json:"money"`
	TradeStatus string `form:"trade_status" json:"trade_status"`
	Sign        string `form:"sign" json:"sign"`
	SignType    string `form:"sign_type" json:"sign_type"`
}

type FutoonClient struct {
	httpClient *http.Client
}

func NewFutoonClient() *FutoonClient {
	return &FutoonClient{httpClient: &http.Client{Timeout: 30 * time.Second}}
}

func IsFutoonEnabled() bool {
	return setting.FutoonApiUrl != "" && setting.FutoonPid != "" && setting.FutoonKey != ""
}

func BuildFutoonSign(params map[string]string, key string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if v == "" || k == "sign" || k == "sign_type" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params[k])
	}
	plain := strings.Join(parts, "&") + key
	sum := md5.Sum([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func VerifyFutoonSign(params map[string]string) bool {
	sign := strings.TrimSpace(params["sign"])
	if sign == "" {
		return false
	}
	return strings.EqualFold(BuildFutoonSign(params, setting.FutoonKey), sign)
}

func normalizeFutoonResponseString(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		switch strings.ToLower(trimmed) {
		case "", "null", "undefined", "about:blank":
			continue
		}
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func getFutoonString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := data[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return strings.TrimSpace(typed)
			}
		case fmt.Stringer:
			converted := strings.TrimSpace(typed.String())
			if converted != "" {
				return converted
			}
		}
	}
	return ""
}

func applyFutoonResponseMap(result *FutoonCreatePaymentResponse, data map[string]any) {
	if result == nil || len(data) == 0 {
		return
	}
	result.URL = normalizeFutoonResponseString(result.URL, getFutoonString(data, "url"))
	result.PaymentURL = normalizeFutoonResponseString(result.PaymentURL, getFutoonString(data, "payment_url", "paymentUrl"))
	result.PayURL = normalizeFutoonResponseString(result.PayURL, getFutoonString(data, "payurl", "payUrl"))
	result.PayURLAlt = normalizeFutoonResponseString(result.PayURLAlt, getFutoonString(data, "pay_url"))
	result.Qrcode = normalizeFutoonResponseString(result.Qrcode, getFutoonString(data, "qrcode"))
	result.QRCode = normalizeFutoonResponseString(result.QRCode, getFutoonString(data, "qr_code", "qrCode"))
	result.TradeNo = normalizeFutoonResponseString(result.TradeNo, getFutoonString(data, "trade_no", "tradeNo"))
	result.Msg = normalizeFutoonResponseString(result.Msg, getFutoonString(data, "msg", "message"))
}

func enrichFutoonPaymentResponse(result *FutoonCreatePaymentResponse, body []byte) {
	if result == nil {
		return
	}

	generic := make(map[string]any)
	if err := common.Unmarshal(body, &generic); err != nil {
		return
	}
	applyFutoonResponseMap(result, generic)

	switch nested := generic["data"].(type) {
	case map[string]any:
		applyFutoonResponseMap(result, nested)
	case string:
		nestedStr := strings.TrimSpace(nested)
		if strings.HasPrefix(nestedStr, "{") {
			nestedMap := make(map[string]any)
			if err := common.Unmarshal([]byte(nestedStr), &nestedMap); err == nil {
				applyFutoonResponseMap(result, nestedMap)
			}
		} else {
			result.URL = normalizeFutoonResponseString(result.URL, nestedStr)
		}
	}
}

func ResolveFutoonPaymentURL(result *FutoonCreatePaymentResponse) string {
	if result == nil {
		return ""
	}
	return normalizeFutoonResponseString(
		result.URL,
		result.PaymentURL,
		result.PayURL,
		result.PayURLAlt,
		result.Qrcode,
		result.QRCode,
	)
}

func (c *FutoonClient) CreatePayment(req *FutoonCreatePaymentRequest) (*FutoonCreatePaymentResponse, url.Values, error) {
	params := url.Values{}
	params.Set("pid", req.Pid)
	params.Set("type", req.Type)
	params.Set("out_trade_no", req.OutTradeNo)
	params.Set("notify_url", req.NotifyURL)
	params.Set("return_url", req.ReturnURL)
	params.Set("name", req.Name)
	params.Set("money", req.Money)
	params.Set("clientip", req.ClientIP)
	params.Set("device", req.Device)
	params.Set("sign_type", "MD5")

	flat := map[string]string{}
	for key := range params {
		flat[key] = params.Get(key)
	}
	params.Set("sign", BuildFutoonSign(flat, setting.FutoonKey))

	httpReq, err := http.NewRequest(http.MethodPost, strings.TrimRight(setting.FutoonApiUrl, "/")+"/mapi.php", bytes.NewBufferString(params.Encode()))
	if err != nil {
		return nil, nil, err
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, params, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, params, err
	}

	var result FutoonCreatePaymentResponse
	if err := common.Unmarshal(body, &result); err != nil {
		return nil, params, fmt.Errorf("unmarshal futoon response failed: %w", err)
	}
	enrichFutoonPaymentResponse(&result, body)
	return &result, params, nil
}
