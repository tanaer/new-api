package operation_setting

import (
	"crypto/md5"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// Bepusdt 支付配置
var BepusdtApiUrl = ""            // API 地址，如 https://pay.example.com
var BepusdtApiToken = ""          // API Token，用于签名
var BepusdtTradeType = "usdt.trc20" // 默认交易类型
var BepusdtFiat = "CNY"           // 法币类型
var BepusdtTimeout = 600          // 订单超时时间（秒）

// IsBepusdtEnabled 检查 Bepusdt 支付是否已配置
func IsBepusdtEnabled() bool {
	return BepusdtApiUrl != "" && BepusdtApiToken != ""
}

// BepusdtGenerateSignature 生成签名
// 签名算法：
// 1. 筛选所有非空且非 signature 的参数
// 2. 按参数名 ASCII 码从小到大排序（字典序）
// 3. 按 key=value 格式拼接，使用 & 连接
// 4. 在拼接字符串末尾追加 API Token
// 5. 对完整字符串进行 MD5 加密
// 6. 将结果转为小写
func BepusdtGenerateSignature(params map[string]interface{}) string {
	// 筛选非空参数
	var keys []string
	for k, v := range params {
		if k == "signature" {
			continue
		}
		switch val := v.(type) {
		case string:
			if val != "" {
				keys = append(keys, k)
			}
		case int, int64, float64:
			keys = append(keys, k)
		}
	}

	// 按字典序排序
	sort.Strings(keys)

	// 拼接
	var parts []string
	for _, k := range keys {
		switch val := params[k].(type) {
		case string:
			parts = append(parts, k+"="+val)
		case int:
			parts = append(parts, k+"="+strconv.Itoa(val))
		case int64:
			parts = append(parts, k+"="+strconv.FormatInt(val, 10))
		case float64:
			parts = append(parts, k+"="+strconv.FormatFloat(val, 'f', -1, 64))
		}
	}
	signStr := strings.Join(parts, "&") + BepusdtApiToken

	// MD5 加密
	hash := md5.Sum([]byte(signStr))
	return hex.EncodeToString(hash[:])
}

// BepusdtVerifySignature 验证签名
func BepusdtVerifySignature(params map[string]interface{}, signature string) bool {
	expected := BepusdtGenerateSignature(params)
	return strings.EqualFold(expected, signature)
}
