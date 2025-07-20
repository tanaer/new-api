package claudecode

import (
	"fmt"
	"io"
	"net/http"
	"one-api/dto"
	"one-api/relay/channel"
	"one-api/relay/channel/claude"
	relaycommon "one-api/relay/common"
	relayconstant "one-api/relay/constant"
	"strings"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
	ChannelType int
	RequestMode int
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.RequestMode = claude.RequestModeMessage
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	// Claude Code API 使用标准的 Anthropic API endpoint
	return fmt.Sprintf("%s/v1/messages", info.BaseUrl), nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	req.Set("x-api-key", info.ApiKey)
	req.Set("anthropic-version", "2023-06-01")
	req.Set("content-type", "application/json")
	
	// Claude Code 特定的 headers
	if userAgent := c.Request.Header.Get("User-Agent"); strings.Contains(userAgent, "Claude-Code") {
		req.Set("x-claude-code-client", "true")
	}
	
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	// 转换 OpenAI 请求到 Claude 格式
	claudeRequest, err := claude.RequestOpenAI2ClaudeMessage(*request)
	if err != nil {
		return nil, err
	}
	
	// 设置 Claude Code 特定的参数
	if claudeRequest.MaxTokens == 0 {
		claudeRequest.MaxTokens = 4096
	}
	
	return claudeRequest, nil
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	// Claude Code 直接使用 Claude 格式，不需要转换
	if request.MaxTokens == 0 {
		request.MaxTokens = 4096
	}
	return request, nil
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, fmt.Errorf("rerank request not supported for Claude Code")
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return nil, fmt.Errorf("embedding request not supported for Claude Code")
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return nil, fmt.Errorf("audio request not supported for Claude Code")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	return nil, fmt.Errorf("image request not supported for Claude Code")
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return nil, fmt.Errorf("responses request not supported for Claude Code")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if info.RelayMode == relayconstant.RelayModeResponses {
		return nil, fmt.Errorf("responses mode not supported for Claude Code")
	}
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *dto.OpenAIErrorWithStatusCode) {
	if info.RelayMode == relayconstant.RelayModeResponses {
		return nil, &dto.OpenAIErrorWithStatusCode{
			StatusCode: http.StatusBadRequest,
			Error: dto.OpenAIError{
				Type:    "invalid_request_error",
				Message: "responses mode not supported for Claude Code",
			},
		}
	}
	
	if info.IsStream {
		err, usage = claude.ClaudeStreamHandler(c, resp, info, a.RequestMode)
	} else {
		err, usage = claude.ClaudeHandler(c, resp, a.RequestMode, info)
	}
	return
}

func (a *Adaptor) GetModelList() []string {
	// Claude Code 支持的模型列表
	return []string{
		"claude-3-opus-20240229",
		"claude-3-sonnet-20240229",
		"claude-3-haiku-20240307",
		"claude-3.5-sonnet-20240620",
		"claude-3.5-sonnet-20241022",
		"claude-3.5-haiku-20241022",
	}
}

func (a *Adaptor) GetChannelName() string {
	return "Claude Code"
}