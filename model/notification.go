package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// NotificationConfig 通知配置
type NotificationConfig struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Type        string `json:"type" gorm:"type:varchar(32);default:'pushplus'"` // pushplus
	Token       string `json:"token" gorm:"type:varchar(255)"`
	Enabled     bool   `json:"enabled" gorm:"default:false"`
	CreatedTime int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime int64  `json:"updated_time" gorm:"bigint"`
}

func GetNotificationConfig() (*NotificationConfig, error) {
	var config NotificationConfig
	err := DB.First(&config).Error
	if err != nil {
		return nil, err
	}
	return &config, nil
}

func SaveNotificationConfig(config *NotificationConfig) error {
	config.UpdatedTime = common.GetTimestamp()
	if config.Id == 0 {
		config.CreatedTime = common.GetTimestamp()
		return DB.Create(config).Error
	}
	return DB.Save(config).Error
}

// SendNotification 通过 PushPlus 发送通知
func SendNotification(title, content string) error {
	config, err := GetNotificationConfig()
	if err != nil {
		return fmt.Errorf("failed to get notification config: %v", err)
	}
	if !config.Enabled || config.Token == "" {
		return nil // 未启用或未配置，静默返回
	}

	return SendPushPlusNotification(config.Token, title, content)
}

// SendPushPlusNotification 发送 PushPlus 通知
func SendPushPlusNotification(token, title, content string) error {
	payload := map[string]string{
		"token":    token,
		"title":    title,
		"channel":  "app",
		"content":  content,
		"template": "markdown",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal notification payload: %v", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post("http://www.pushplus.plus/send", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to send pushplus notification: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pushplus returned status %d: %s", resp.StatusCode, string(respBody))
	}

	common.SysLog(fmt.Sprintf("PushPlus notification sent: title=%s, response=%s", title, string(respBody)))
	return nil
}
