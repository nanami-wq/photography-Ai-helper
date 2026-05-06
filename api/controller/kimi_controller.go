package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"go-service-starter/config"
	"go-service-starter/core/libx"

	"github.com/gin-gonic/gin"
)

type KimiController struct {
	httpClient *http.Client
}

func NewKimiController() *KimiController {
	return &KimiController{
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

func (k *KimiController) RegisterPublic(r *gin.RouterGroup) {
	r.POST("/kimi/generate", k.Generate)
	r.POST("/kimi/recognize-image", k.RecognizeImageBinary)
	r.POST("/kimi/recognize/image", k.RecognizeImageBinary)
}

func (k *KimiController) RegisterProtected(r *gin.RouterGroup) {
	r.POST("/kimi/photography/analyze-image", k.PhotographyAnalyzeImage)
	r.POST("/kimi/analyze-photo", k.PhotographyAnalyzeImage)
	r.GET("/kimi/photography/analyze-image", k.photoAnalyzeMethodHint)
	r.GET("/kimi/analyze-photo", k.photoAnalyzeMethodHint)
}

func (k *KimiController) photoAnalyzeMethodHint(c *gin.Context) {
	libx.Err(c, http.StatusMethodNotAllowed,
		"请使用 POST（需先登录；Header：Authorization: Bearer <token>）；multipart 字段 image 或 file。路径：POST /api/kimi/photography/analyze-image 或 POST /api/kimi/analyze-photo",
		nil)
}

// 单张图片二进制上限（multipart 整请求或原始 body 都会受此上限约束）
const maxImageUploadBytes = 20 << 20

// kimiModelK26 多模态接口固定使用该模型（不使用配置文件里的默认模型占位）
const kimiModelK26 = "kimi-k2.6"

// photographySystemPrompt 摄影分析接口的系统提示（引导模型从摄影专业角度输出）
var photographySystemPrompt = strings.TrimSpace(`
你是一位经验丰富的摄影指导与影像评审。用户会上传一张照片，请你基于画面给出专业、可执行的分析与建议。
请尽量从以下维度展开（若画面信息不足可简要说明）：构图与画面平衡、曝光与明暗层次、色彩与白平衡、对焦与景深、光线方向与质感、主体表达与叙事意图。
语气友善、具体：先简要肯定亮点，再指出可改进之处，并给出拍摄参数、取景或后期方面的可操作建议。
`)

const photographyUserBase = "请根据上述要求，对附图从摄影角度进行分析并给出建议。"

type kimiGenerateBody struct {
	Text    string   `json:"text"`
	Prompt  string   `json:"prompt"`
	Model   string   `json:"model"`
	Images  []string `json:"images"`  // 可选：每条为完整 data URL、http(s) URL、ms:// 引用，或裸 base64（按 JPEG data URL 拼接）
	Videos  []string `json:"videos"`  // 可选：同上，视频多为 data:video/...;base64,...
}

func (b *kimiGenerateBody) userText() string {
	t := strings.TrimSpace(b.Text)
	if t != "" {
		return t
	}
	return strings.TrimSpace(b.Prompt)
}

func publicKimiNetworkErr(err error) error {
	if err == nil {
		return nil
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Timeout() {
		return fmt.Errorf("连接 Kimi（Moonshot）接口超时，请检查网络或 HTTPS_PROXY")
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fmt.Errorf("连接 Kimi（Moonshot）接口超时，请检查网络或 HTTPS_PROXY")
	}
	low := strings.ToLower(err.Error())
	if strings.Contains(low, "timeout") || strings.Contains(low, "i/o timeout") {
		return fmt.Errorf("连接 Kimi（Moonshot）接口超时，请检查网络或 HTTPS_PROXY")
	}
	if strings.Contains(low, "connection refused") {
		return fmt.Errorf("连接被拒绝，请检查本机或 HTTPS_PROXY 代理是否可用")
	}
	return fmt.Errorf("无法连接 Kimi 服务，请检查网络、防火墙或代理（勿在响应中暴露上游详情）")
}

func sanitizeKimiAPIKey(key string) string {
	key = strings.TrimSpace(key)
	key = strings.TrimPrefix(strings.TrimPrefix(key, "Bearer "), "bearer ")
	key = strings.Trim(key, `"'`)
	return strings.TrimSpace(key)
}

// normalizeMediaURL 将单条输入转为 Kimi image_url / video_url 可用的 url 字段。
func normalizeMediaURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "data:image/") ||
		strings.HasPrefix(s, "data:video/") ||
		strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "ms://") {
		return s
	}
	return "data:image/jpeg;base64," + s
}

func normalizeVideoURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "data:video/") ||
		strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "ms://") {
		return s
	}
	return "data:video/mp4;base64," + s
}

func buildKimiUserContent(text string, images, videos []string) any {
	hasMedia := len(images) > 0 || len(videos) > 0
	if !hasMedia {
		return text
	}
	parts := make([]map[string]any, 0, len(images)+len(videos)+1)
	for _, img := range images {
		u := normalizeMediaURL(img)
		if u == "" {
			continue
		}
		parts = append(parts, map[string]any{
			"type":       "image_url",
			"image_url":  map[string]string{"url": u},
		})
	}
	for _, v := range videos {
		u := normalizeVideoURL(v)
		if u == "" {
			continue
		}
		parts = append(parts, map[string]any{
			"type":       "video_url",
			"video_url": map[string]string{"url": u},
		})
	}
	t := strings.TrimSpace(text)
	if t == "" {
		t = "请结合以上内容进行描述或回答。"
	}
	parts = append(parts, map[string]any{"type": "text", "text": t})
	return parts
}

func (k *KimiController) postKimiChat(ctx context.Context, model, base, apiKey string, messages []map[string]any) (statusCode int, respBody []byte, reqErr error) {
	payload := map[string]any{
		"model":    model,
		"messages": messages,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	apiURL := strings.TrimRight(strings.TrimSpace(base), "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

func imageBinaryToDataURL(raw []byte, mimeHint string) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("图像数据为空")
	}
	mimeHint = strings.TrimSpace(strings.ToLower(mimeHint))
	if mimeHint != "" && mimeHint != "application/octet-stream" && strings.HasPrefix(mimeHint, "image/") {
		b64 := base64.StdEncoding.EncodeToString(raw)
		return fmt.Sprintf("data:%s;base64,%s", mimeHint, b64), nil
	}
	sample := raw
	if len(sample) > 512 {
		sample = raw[:512]
	}
	mt := http.DetectContentType(sample)
	switch mt {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		b64 := base64.StdEncoding.EncodeToString(raw)
		return fmt.Sprintf("data:%s;base64,%s", mt, b64), nil
	default:
		return "", fmt.Errorf("无法识别的图片格式 (%s)，请上传 jpeg/png/gif/webp", mt)
	}
}

// readSingleImageBinary 读取一张上传图；httpStatus 非 0 时表示失败，errMsg 为客户端提示。
func readSingleImageBinary(c *gin.Context) (raw []byte, mimeHint string, httpStatus int, errMsg string) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImageUploadBytes)
	ct := c.ContentType()
	if strings.Contains(ct, "multipart/form-data") {
		if err := c.Request.ParseMultipartForm(maxImageUploadBytes); err != nil {
			return nil, "", http.StatusBadRequest, "multipart 解析失败: " + err.Error()
		}
		fh, err := c.FormFile("image")
		if err != nil {
			fh, err = c.FormFile("file")
		}
		if err != nil {
			return nil, "", http.StatusBadRequest, "请使用 multipart 上传字段 image 或 file"
		}
		if fh.Size > maxImageUploadBytes {
			return nil, "", http.StatusRequestEntityTooLarge, "图片超过大小上限"
		}
		src, err := fh.Open()
		if err != nil {
			return nil, "", http.StatusBadRequest, "无法打开上传文件"
		}
		b, err := io.ReadAll(io.LimitReader(src, maxImageUploadBytes+1))
		src.Close()
		if err != nil {
			return nil, "", http.StatusBadRequest, "读取上传文件失败"
		}
		if len(b) > maxImageUploadBytes {
			return nil, "", http.StatusRequestEntityTooLarge, "图片超过大小上限"
		}
		return b, fh.Header.Get("Content-Type"), 0, ""
	}
	if strings.HasPrefix(ct, "image/") || ct == "application/octet-stream" {
		b, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return nil, "", http.StatusBadRequest, "读取请求正文失败"
		}
		if len(b) == 0 {
			return nil, "", http.StatusBadRequest, "请求正文为空"
		}
		return b, ct, 0, ""
	}
	return nil, "", http.StatusUnsupportedMediaType,
		"请使用 multipart/form-data 上传字段 image 或 file，或使用 Content-Type 为 image/* 或 application/octet-stream 传递原始图片二进制"
}

func (k *KimiController) finishKimiFromUpstream(c *gin.Context, status int, respBody []byte, model string) {
	if status != http.StatusOK {
		if status == http.StatusUnauthorized {
			libx.Err(c, http.StatusUnauthorized,
				"Kimi 鉴权失败：密钥与 base_url 需同属一国别开放平台——在中国大陆申请的密钥请设 kimi.base_url 为 https://api.moonshot.cn/v1；国际/ kimi.ai 侧密钥一般用 https://api.moonshot.ai/v1。并核对密钥未过期、整串粘贴且无多余字符",
				nil)
			return
		}
		libx.Err(c, status, "Kimi 返回错误", fmt.Errorf("%s", string(respBody)))
		return
	}
	out, err := parseKimiChatResponse(respBody)
	if err != nil {
		libx.Err(c, http.StatusBadGateway, "解析 Kimi 响应失败", err)
		return
	}
	libx.Ok(c, "ok", gin.H{
		"text":  out,
		"model": model,
	})
}

// RecognizeImageBinary 接收一张图片的二进制（无需登录）：
// - multipart/form-data：字段名 image 或 file；可选表单字段 prompt、可选查询参数 model
// - 原始正文：Content-Type 为 image/jpeg | image/png | image/webp | image/gif 或 application/octet-stream（按嗅探识别）；可选查询参数 prompt、model
func (k *KimiController) RecognizeImageBinary(c *gin.Context) {
	cfg := config.GetConfig()
	key := sanitizeKimiAPIKey(cfg.Kimi.APIKey)
	if key == "" {
		libx.Err(c, http.StatusInternalServerError, "未配置 kimi.api_key（或环境变量 KIMI_API_KEY / MOONSHOT_API_KEY）", nil)
		return
	}

	model := strings.TrimSpace(c.Query("model"))
	if model == "" {
		model = strings.TrimSpace(cfg.Kimi.Model)
	}
	if model == "" {
		model = kimiModelK26
	}

	base := strings.TrimSpace(cfg.Kimi.BaseURL)
	if base == "" {
		base = "https://api.moonshot.ai/v1"
	}

	prompt := strings.TrimSpace(c.Query("prompt"))
	if prompt == "" {
		prompt = strings.TrimSpace(c.PostForm("prompt"))
	}
	if prompt == "" {
		prompt = "请识别并简要描述这张图片的主要内容。"
	}

	raw, mimeHint, httpStatus, errMsg := readSingleImageBinary(c)
	if httpStatus != 0 {
		libx.Err(c, httpStatus, errMsg, nil)
		return
	}

	dataURL, err := imageBinaryToDataURL(raw, mimeHint)
	if err != nil {
		libx.Err(c, http.StatusBadRequest, err.Error(), nil)
		return
	}

	userContent := buildKimiUserContent(prompt, []string{dataURL}, nil)
	msgs := []map[string]any{{"role": "user", "content": userContent}}
	status, respBody, err := k.postKimiChat(c.Request.Context(), model, base, key, msgs)
	if err != nil {
		log.Printf("kimi upstream error: %v", err)
		libx.Err(c, http.StatusBadGateway, "调用 Kimi 失败", publicKimiNetworkErr(err))
		return
	}
	k.finishKimiFromUpstream(c, status, respBody, model)
}

// PhotographyAnalyzeImage 使用 kimi-k2.6 多模态，从摄影角度分析与建议（system + user 引导）；需在 JWT 保护路由下调用。
func (k *KimiController) PhotographyAnalyzeImage(c *gin.Context) {
	cfg := config.GetConfig()
	key := sanitizeKimiAPIKey(cfg.Kimi.APIKey)
	if key == "" {
		libx.Err(c, http.StatusInternalServerError, "未配置 kimi.api_key（或环境变量 KIMI_API_KEY / MOONSHOT_API_KEY）", nil)
		return
	}

	model := kimiModelK26

	base := strings.TrimSpace(cfg.Kimi.BaseURL)
	if base == "" {
		base = "https://api.moonshot.ai/v1"
	}

	extra := strings.TrimSpace(c.Query("prompt"))
	if extra == "" {
		extra = strings.TrimSpace(c.PostForm("prompt"))
	}
	userLine := photographyUserBase
	if extra != "" {
		userLine = photographyUserBase + "\n\n【用户补充关注点】" + extra
	}

	raw, mimeHint, httpStatus, errMsg := readSingleImageBinary(c)
	if httpStatus != 0 {
		libx.Err(c, httpStatus, errMsg, nil)
		return
	}

	dataURL, err := imageBinaryToDataURL(raw, mimeHint)
	if err != nil {
		libx.Err(c, http.StatusBadRequest, err.Error(), nil)
		return
	}

	userContent := buildKimiUserContent(userLine, []string{dataURL}, nil)
	msgs := []map[string]any{
		{"role": "system", "content": photographySystemPrompt},
		{"role": "user", "content": userContent},
	}

	status, respBody, err := k.postKimiChat(c.Request.Context(), model, base, key, msgs)
	if err != nil {
		log.Printf("kimi upstream error: %v", err)
		libx.Err(c, http.StatusBadGateway, "调用 Kimi 失败", publicKimiNetworkErr(err))
		return
	}
	k.finishKimiFromUpstream(c, status, respBody, model)
}

func (k *KimiController) Generate(c *gin.Context) {
	var body kimiGenerateBody
	if err := c.ShouldBindJSON(&body); err != nil {
		libx.Err(c, http.StatusBadRequest, "参数无效", err)
		return
	}
	text := body.userText()
	hasMedia := len(body.Images) > 0 || len(body.Videos) > 0
	if text == "" && !hasMedia {
		libx.Err(c, http.StatusBadRequest, "请提供 text/prompt，或与 images/videos 一并用于多模态", nil)
		return
	}

	cfg := config.GetConfig()
	key := sanitizeKimiAPIKey(cfg.Kimi.APIKey)
	if key == "" {
		libx.Err(c, http.StatusInternalServerError, "未配置 kimi.api_key（或环境变量 KIMI_API_KEY / MOONSHOT_API_KEY）", nil)
		return
	}

	model := strings.TrimSpace(body.Model)
	if model == "" {
		model = strings.TrimSpace(cfg.Kimi.Model)
	}
	if model == "" {
		model = kimiModelK26
	}

	base := strings.TrimSpace(cfg.Kimi.BaseURL)
	if base == "" {
		base = "https://api.moonshot.ai/v1"
	}
	base = strings.TrimRight(base, "/")

	userContent := buildKimiUserContent(text, body.Images, body.Videos)

	msgs := []map[string]any{{"role": "user", "content": userContent}}

	status, respBody, err := k.postKimiChat(c.Request.Context(), model, base, key, msgs)
	if err != nil {
		log.Printf("kimi upstream error: %v", err)
		libx.Err(c, http.StatusBadGateway, "调用 Kimi 失败", publicKimiNetworkErr(err))
		return
	}

	k.finishKimiFromUpstream(c, status, respBody, model)
}

func parseKimiChatResponse(b []byte) (string, error) {
	var root struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		return "", err
	}
	if root.Error != nil && root.Error.Message != "" {
		return "", fmt.Errorf("%s", root.Error.Message)
	}
	if len(root.Choices) == 0 {
		return "", fmt.Errorf("choices 为空")
	}
	s := strings.TrimSpace(root.Choices[0].Message.Content)
	if s == "" {
		return "", fmt.Errorf("模型未返回文本")
	}
	return s, nil
}
