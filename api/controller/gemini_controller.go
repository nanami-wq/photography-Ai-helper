package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go-service-starter/config"
	"go-service-starter/core/libx"

	"github.com/gin-gonic/gin"
)

type GeminiController struct {
	httpClient *http.Client
}

func NewGeminiController() *GeminiController {
	return &GeminiController{
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

func (g *GeminiController) RegisterPublic(r *gin.RouterGroup) {
	r.POST("/gemini/generate", g.Generate)
}

type geminiGenerateBody struct {
	Text   string `json:"text"`
	Prompt string `json:"prompt"`
	Model  string `json:"model"`
}

func (b *geminiGenerateBody) userText() string {
	t := strings.TrimSpace(b.Text)
	if t != "" {
		return t
	}
	return strings.TrimSpace(b.Prompt)
}

func publicGeminiNetworkErr(err error) error {
	if err == nil {
		return nil
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Timeout() {
		return fmt.Errorf("连接 Google 接口超时，请检查网络或使用 VPN；若在受限网络可设置环境变量 HTTPS_PROXY 后重启服务")
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fmt.Errorf("连接 Google 接口超时，请检查网络或使用 VPN；若在受限网络可设置环境变量 HTTPS_PROXY 后重启服务")
	}
	low := strings.ToLower(err.Error())
	if strings.Contains(low, "timeout") || strings.Contains(low, "i/o timeout") {
		return fmt.Errorf("连接 Google 接口超时，请检查网络或使用 VPN；若在受限网络可设置环境变量 HTTPS_PROXY 后重启服务")
	}
	if strings.Contains(low, "connection refused") {
		return fmt.Errorf("连接被拒绝，请检查本机或 HTTPS_PROXY 代理是否可用")
	}
	return fmt.Errorf("无法连接 Gemini 服务，请检查网络、防火墙或代理（勿在响应中暴露上游详情）")
}

func (g *GeminiController) Generate(c *gin.Context) {
	var body geminiGenerateBody
	if err := c.ShouldBindJSON(&body); err != nil {
		libx.Err(c, http.StatusBadRequest, "参数无效", err)
		return
	}
	text := body.userText()
	if text == "" {
		libx.Err(c, http.StatusBadRequest, "请提供 text 或 prompt 字段作为输入内容", nil)
		return
	}

	cfg := config.GetConfig()
	key := strings.TrimSpace(cfg.Gemini.APIKey)
	if key == "" {
		libx.Err(c, http.StatusInternalServerError, "未配置 gemini.api_key（或环境变量 GEMINI_API_KEY）", nil)
		return
	}

	model := strings.TrimSpace(body.Model)
	if model == "" {
		model = strings.TrimSpace(cfg.Gemini.Model)
	}
	if model == "" {
		model = "gemini-2.0-flash"
	}

	payload := map[string]any{
		"contents": []map[string]any{
			{
				"parts": []map[string]string{{"text": text}},
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		libx.Err(c, http.StatusInternalServerError, "请求体编码失败", err)
		return
	}

	apiURL := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent",
		url.PathEscape(model),
	)
	u, err := url.Parse(apiURL)
	if err != nil {
		libx.Err(c, http.StatusInternalServerError, "URL 无效", err)
		return
	}
	q := u.Query()
	q.Set("key", key)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		libx.Err(c, http.StatusInternalServerError, "构建请求失败", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		log.Printf("gemini upstream error: %v", err)
		libx.Err(c, http.StatusBadGateway, "调用 Gemini 失败", publicGeminiNetworkErr(err))
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		libx.Err(c, resp.StatusCode, "Gemini 返回错误", fmt.Errorf("%s", string(respBody)))
		return
	}

	out, err := parseGeminiGenerateResponse(respBody)
	if err != nil {
		libx.Err(c, http.StatusBadGateway, "解析 Gemini 响应失败", err)
		return
	}

	libx.Ok(c, "ok", gin.H{
		"text":  out,
		"model": model,
	})
}

func parseGeminiGenerateResponse(b []byte) (string, error) {
	var root struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		return "", err
	}
	if root.Error != nil && root.Error.Message != "" {
		return "", fmt.Errorf("%s", root.Error.Message)
	}
	if len(root.Candidates) == 0 {
		return "", fmt.Errorf("candidates 为空")
	}
	var sb strings.Builder
	for _, p := range root.Candidates[0].Content.Parts {
		sb.WriteString(p.Text)
	}
	s := strings.TrimSpace(sb.String())
	if s == "" {
		return "", fmt.Errorf("模型未返回文本")
	}
	return s, nil
}
