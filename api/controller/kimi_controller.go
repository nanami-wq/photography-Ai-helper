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
	"math"
	"net"
	"net/http"
	"strings"
	"time"

	"go-service-starter/config"
	"go-service-starter/core/libx"
	"go-service-starter/core/photoscorer"
	"go-service-starter/core/rag"

	"github.com/gin-gonic/gin"
)

// ctxKeyPhotographyMetrics 单次请求内复用 CV 指标，避免重复解码；供 RAG 与融合共用。
const ctxKeyPhotographyMetrics = "photography_score_metrics"

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
	r.POST("/kimi/photography/score-image", k.PhotographyScoreImage)
	r.POST("/kimi/score-photo", k.PhotographyScoreImage)
	r.GET("/kimi/photography/score-image", k.photoScoreMethodHint)
	r.GET("/kimi/score-photo", k.photoScoreMethodHint)
}

func (k *KimiController) photoAnalyzeMethodHint(c *gin.Context) {
	libx.Err(c, http.StatusMethodNotAllowed,
		"请使用 POST（需先登录；Header：Authorization: Bearer <token>）；multipart 字段 image 或 file。路径：POST /api/kimi/photography/analyze-image 或 POST /api/kimi/analyze-photo",
		nil)
}

func (k *KimiController) photoScoreMethodHint(c *gin.Context) {
	libx.Err(c, http.StatusMethodNotAllowed,
		"请使用 POST（需先登录；Header：Authorization: Bearer <token>）；multipart 字段 image 或 file。路径：POST /api/kimi/photography/score-image 或 POST /api/kimi/score-photo；返回各维度 0–100 分与文字分析 JSON",
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

// photographyScoreSystemPrompt 固定量表 + 仅 JSON 输出，便于多图可比与复现（该模型 temperature 由接口固定为 1）。
var photographyScoreSystemPrompt = strings.TrimSpace(`
你是摄影赛事初审评委：同一量表、跨所有图片可比；禁止讨好用户、禁止「安全牌」中间分。
所有分数为 0–100 的整数；只依据画面可见证据；看不清则在对应 dimension_notes 写明「信息不足」并该维度倾向给 35–45（不要默认 50）。

【分数—评语一致性（必须满足）】
dimension_notes 对该维度的褒贬，必须与该维度分数同向：评语以批评为主、且未写实质亮点时，该维度分通常不得高于 48；若写明「严重/显著/尽失/严重模糊/伪影严重」等，该维度须落在 15–40。
若某维度评语同时含明显优缺点，分数应落在 40–65 并体现主次，不得三台一律塞 48–52。
禁止在 technique_notes 指出严重技术缺陷时，仍将 technique_score 拉到 45 以上除非明确解释创意性模糊/风格化且可辨认是刻意为之。

【锚点刻度——每维打分前先选档再微调个位数，避免扎堆整十】
每维优先用 17、23、31、38、44、52、58、63、71、78、86 等非克隆数，除非该维确实恰为典型「典型中段」才可使用 50 附近。

1) color_score：白平衡、色调与题材关系；和谐度与饱和度控制。
   - 15–25：明显偏色/脏灰/过饱和干扰主题。
   - 35–45：尚可但单调、层次弱或轻微偏色。
   - 55–65：整体舒服、略有平庸或轻微遗憾。
   - 72–82：色彩推动情绪与层次，克制有力。
   - 88+：极少给出；需画面与题材高度匹配且具辨识度。
2) composition_score：主体、线条、留白、平衡、裁切与视线引导。
   - 15–25：严重失衡、致命裁切、主体淹没。
   - 35–45：完整但不讲究；缺少引导与节奏。
   - 55–65：稳妥可用，有一点设计感或一笔败笔。
   - 72–82：结构清晰，引导明确，有章法。
3) technique_score：曝光层次、对焦与解析度、噪点与压缩伪影、景深合理性。
   - 15–25：明显过曝/死黑/严重虚焦或解析度崩溃致主体难辨。
   - 35–55：能看但有明显软焦、重压缩或层次不足；依程度细分勿雷同。
   - 62+：技术扎实；85+ 极少。

【输出】仅输出一个 JSON 对象，勿 Markdown、勿 JSON 外文字。
键必须完全一致：
{
  "color_score": <int>,
  "composition_score": <int>,
  "technique_score": <int>,
  "text_analysis": "<string，中文：总体评价 + 可执行改进建议，分段可用 \\n>",
  "dimension_notes": {
    "color": "<一句，须与 color_score 同向>",
    "composition": "<一句，须与 composition_score 同向>",
    "technique": "<一句，须与 technique_score 同向>"
  }
}
不要输出 overall_score，由服务端按固定权重计算。
`)

const photographyScoreUserBase = "请仅依据附图，按系统量表输出 JSON；dimension_notes 每项一句话；text_analysis 200–500 字为宜。"

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

func (k *KimiController) postKimiChat(ctx context.Context, model, base, apiKey string, messages []map[string]any, payloadExtras map[string]any) (statusCode int, respBody []byte, reqErr error) {
	payload := map[string]any{
		"model":    model,
		"messages": messages,
	}
	for ek, ev := range payloadExtras {
		payload[ek] = ev
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
	status, respBody, err := k.postKimiChat(c.Request.Context(), model, base, key, msgs, nil)
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

	status, respBody, err := k.postKimiChat(c.Request.Context(), model, base, key, msgs, nil)
	if err != nil {
		log.Printf("kimi upstream error: %v", err)
		libx.Err(c, http.StatusBadGateway, "调用 Kimi 失败", publicKimiNetworkErr(err))
		return
	}
	k.finishKimiFromUpstream(c, status, respBody, model)
}

const photographyScoreRubricID = "photography_v2"

type photographyScoreModelJSON struct {
	ColorScore       float64           `json:"color_score"`
	CompositionScore float64         `json:"composition_score"`
	TechniqueScore   float64         `json:"technique_score"`
	TextAnalysis     string          `json:"text_analysis"`
	DimensionNotes   map[string]string `json:"dimension_notes"`
}

func clampScoreInt(x int) int {
	if x < 0 {
		return 0
	}
	if x > 100 {
		return 100
	}
	return x
}

func roundClampScore(f float64) int {
	return clampScoreInt(int(math.Round(f)))
}

// weightedOverallPhotographyScore 固定权重，保证同一张图的三项子分转为总分时口径一致（与模型可能给出的总分解耦）。
func weightedOverallPhotographyScore(color, composition, technique int) int {
	v := 0.30*float64(color) + 0.35*float64(composition) + 0.35*float64(technique)
	return clampScoreInt(int(math.Round(v)))
}

func extractJSONFromModelText(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	var b strings.Builder
	inBlock := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") {
			if !inBlock {
				inBlock = true
				continue
			}
			break
		}
		if inBlock {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func parsePhotographyScoreModelJSON(jsonStr string) (photographyScoreModelJSON, error) {
	var p photographyScoreModelJSON
	jsonStr = strings.TrimSpace(jsonStr)
	if jsonStr == "" {
		return p, fmt.Errorf("模型输出为空")
	}
	if err := json.Unmarshal([]byte(jsonStr), &p); err != nil {
		return p, err
	}
	return p, nil
}

func (k *KimiController) finishKimiScoreFromUpstream(c *gin.Context, status int, respBody []byte, model string, rawImg []byte) {
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
	text, err := parseKimiChatResponse(respBody)
	if err != nil {
		libx.Err(c, http.StatusBadGateway, "解析 Kimi 响应失败", err)
		return
	}
	jsonStr := extractJSONFromModelText(text)
	payload, err := parsePhotographyScoreModelJSON(jsonStr)
	if err != nil {
		libx.Err(c, http.StatusBadGateway, "模型未返回合法 JSON，请重试或检查提示词冲突", err)
		return
	}
	cScore := roundClampScore(payload.ColorScore)
	coScore := roundClampScore(payload.CompositionScore)
	tScore := roundClampScore(payload.TechniqueScore)
	llmC, llmCo, llmT := cScore, coScore, tScore

	fusionH := gin.H{
		"enabled":         false,
		"method":          "llm_cv_weighted_v1",
		"fusion_version":  "cv_metrics_v3",
		"weights_digest":  photoscorer.FusionWeightsNote,
	}
	if len(rawImg) > 0 {
		var m photoscorer.Metrics
		var errCV error
		if v, ok := c.Get(ctxKeyPhotographyMetrics); ok {
			if pm, ok2 := v.(photoscorer.Metrics); ok2 {
				m = pm
			} else {
				m, errCV = photoscorer.MetricsFromBytes(rawImg)
			}
		} else {
			m, errCV = photoscorer.MetricsFromBytes(rawImg)
		}
		if errCV != nil {
			fusionH["cv_decode_error"] = errCV.Error()
		} else {
			fr := photoscorer.FuseLLMWithMetrics(llmC, llmCo, llmT, m)
			cScore, coScore, tScore = fr.FinalColor, fr.FinalComposition, fr.FinalTechnique
			fusionH["enabled"] = true
			fusionH["objective_metrics"] = fr.Metrics
			fusionH["llm_subscores"] = gin.H{
				"color": llmC, "composition": llmCo, "technique": llmT,
			}
			fusionH["blend_weights"] = gin.H{
				"color_llm": fr.ColorLLMWeight, "technique_llm": fr.TechniqueLLMWeight,
				"composition_llm": fr.CompositionLLMWeight,
			}
		}
	} else {
		fusionH["hint"] = "无原始图像字节，跳过像素级客观融合"
	}

	overall := weightedOverallPhotographyScore(cScore, coScore, tScore)
	notes := payload.DimensionNotes
	if notes == nil {
		notes = map[string]string{}
	}
	lowDiff := cScore >= 46 && cScore <= 54 && coScore >= 46 && coScore <= 54 && tScore >= 46 && tScore <= 54
	disp := gin.H{"low_differentiation": lowDiff}
	if lowDiff {
		disp["hint"] = "三项子分均落在 46–54，易为模型「安全中段」或客观量也居中；请对照 dimension_notes 与 algorithm_fusion 人工复核。"
	}
	var ragOut any
	if v, exists := c.Get(rag.CtxKeyScoreRAG); exists {
		ragOut = v
	}
	libx.Ok(c, "ok", gin.H{
		"rubric_id":         photographyScoreRubricID,
		"model":             model,
		"color_score":       cScore,
		"composition_score": coScore,
		"technique_score":   tScore,
		"overall_score":     overall,
		"overall_weights":   gin.H{"color": 0.30, "composition": 0.35, "technique": 0.35},
		"text_analysis":     strings.TrimSpace(payload.TextAnalysis),
		"dimension_notes":   notes,
		"score_dispersion":  disp,
		"algorithm_fusion":  fusionH,
		"rag":               ragOut,
	})
}

// PhotographyScoreImage 多模态摄影评分：色彩 / 构图 / 技术三项 0–100 整数 + 文字分析，JSON 结构由服务端校验并统一计算 overall。
func (k *KimiController) PhotographyScoreImage(c *gin.Context) {
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
	userLine := photographyScoreUserBase
	if extra != "" {
		userLine = photographyScoreUserBase + "\n\n【用户补充】 " + extra
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

	mCV, errCV := photoscorer.MetricsFromBytes(raw)
	if errCV == nil {
		c.Set(ctxKeyPhotographyMetrics, mCV)
	}
	ragAugment := ""
	ragMeta := map[string]any{"enabled": false, "reason": "cv_decode_or_disabled"}
	if errCV == nil {
		aug, meta := rag.BuildPhotographyScoreRAG(c.Request.Context(), k.httpClient, &cfg.Rag, key, base, mCV, extra)
		ragAugment = aug
		ragMeta = meta
	} else {
		ragMeta["cv_error"] = errCV.Error()
	}
	c.Set(rag.CtxKeyScoreRAG, ragMeta)

	systemPrompt := photographyScoreSystemPrompt
	if strings.TrimSpace(ragAugment) != "" {
		systemPrompt = photographyScoreSystemPrompt + "\n\n" + ragAugment
	}

	userContent := buildKimiUserContent(userLine, []string{dataURL}, nil)
	msgs := []map[string]any{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": userContent},
	}

	extras := map[string]any{
		// kimi-k2.6 多模态等：上游仅接受 temperature=1，其它值会报 invalid temperature
		"temperature": 1,
		"response_format": map[string]string{
			"type": "json_object",
		},
	}

	status, respBody, err := k.postKimiChat(c.Request.Context(), model, base, key, msgs, extras)
	if err != nil {
		log.Printf("kimi upstream error: %v", err)
		libx.Err(c, http.StatusBadGateway, "调用 Kimi 失败", publicKimiNetworkErr(err))
		return
	}
	k.finishKimiScoreFromUpstream(c, status, respBody, model, raw)
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

	status, respBody, err := k.postKimiChat(c.Request.Context(), model, base, key, msgs, nil)
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
