package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetNotificationConfig(c *gin.Context) {
	config, err := model.GetNotificationConfig()
	if err != nil {
		// 未配置时返回默认结构
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"type":    "pushplus",
				"token":   "",
				"enabled": false,
			},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    config,
	})
}

func UpdateNotificationConfig(c *gin.Context) {
	var config model.NotificationConfig
	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的请求参数"})
		return
	}

	// 检查是否已存在配置
	existing, err := model.GetNotificationConfig()
	if err == nil && existing != nil {
		config.Id = existing.Id
	}

	if err := model.SaveNotificationConfig(&config); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "保存成功"})
}

type TestNotificationRequest struct {
	Token string `json:"token"`
}

func TestNotification(c *gin.Context) {
	var req TestNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的请求参数"})
		return
	}

	token := req.Token
	if token == "" {
		// 尝试从已保存的配置中获取
		config, err := model.GetNotificationConfig()
		if err != nil || config.Token == "" {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "未配置 PushPlus Token"})
			return
		}
		token = config.Token
	}

	err := model.SendPushPlusNotification(token, "测试通知", "# 通知测试\n\n这是一条来自 New API 的测试通知。\n\n如果您看到此消息，说明通知配置正确。")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "通知已发送"})
}
