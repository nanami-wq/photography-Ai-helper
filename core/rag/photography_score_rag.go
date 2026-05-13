package rag

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"

	"go-service-starter/config"
	"go-service-starter/core/photoscorer"
)

const CtxKeyScoreRAG = "photography_score_rag"

var (
	indexOnce   sync.Once
	indexErr    error
	chunkEmbeds [][]float64
)
func metricsToQueryText(m photoscorer.Metrics, userExtra string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "摄影评审 客观特征 清晰度%.0f 曝光%.0f 技术综合%.0f ",
		m.Sharpness0_100, m.Exposure0_100, m.TechniqueCV0_100)
	fmt.Fprintf(&b, "色彩丰富%.0f 色彩度%.0f 白平衡一致性%.0f 饱和度适宜%.0f 色彩综合%.0f ",
		m.ColorRichness0_100, m.ColorColorfulness0_100, m.ColorBalance0_100,
		m.ColorSaturationIdeal0_100, m.ColorCV0_100)
	fmt.Fprintf(&b, "构图客观%.0f 构图焦点集中度%.2f 构图线距离%.2f",
		m.CompositionCV0_100, m.CompositionFocusRatio, m.CompositionThirdsDist)
	if userExtra != "" {
		b.WriteString(" 用户关注点 ")
		b.WriteString(userExtra)
	}
	return b.String()
}

func inferKeywordTerms(m photoscorer.Metrics) []string {
	var terms []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		for _, existing := range terms {
			if existing == s {
				return
			}
		}
		terms = append(terms, s)
	}
	if m.Sharpness0_100 < 32 {
		add("虚焦")
		add("模糊")
		add("清晰度")
		add("对焦")
	}
	if m.Exposure0_100 < 38 {
		add("曝光")
		add("高光")
		add("阴影")
		add("死黑")
		add("死白")
	}
	if m.ColorBalance0_100 < 42 {
		add("偏色")
		add("白平衡")
		add("灰罩")
	}
	if m.ColorRichness0_100 < 38 && m.ColorColorfulness0_100 < 40 {
		add("饱和")
		add("发灰")
		add("发闷")
	}
	if m.ColorSaturationIdeal0_100 < 40 {
		add("过艳")
		add("溢色")
	}
	if m.CompositionCV0_100 < 42 {
		add("构图")
		add("留白")
		add("主体")
		add("失衡")
		add("裁切")
	}
	if m.CompositionThirdsDist > 0.22 {
		add("三分")
		add("黄金分割")
	}
	return terms
}

func keywordScores(queryTerms []string, topK int) ([]chunkrec, []string) {
	type scored struct {
		c     chunkrec
		score int
	}
	var rows []scored
	for _, ch := range photographyScoreChunks {
		s := 0
		for _, t := range queryTerms {
			if t != "" && strings.Contains(ch.text, t) {
				s++
			}
		}
		if s > 0 {
			rows = append(rows, scored{c: ch, score: s})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].score != rows[j].score {
			return rows[i].score > rows[j].score
		}
		return rows[i].c.id < rows[j].c.id
	})
	if len(rows) == 0 {
		for _, ch := range photographyScoreChunks[:minInt(topK, len(photographyScoreChunks))] {
			rows = append(rows, scored{c: ch, score: 0})
		}
	}
	if topK <= 0 {
		topK = 4
	}
	var out []chunkrec
	var ids []string
	for i := 0; i < len(rows) && len(out) < topK; i++ {
		out = append(out, rows[i].c)
		ids = append(ids, rows[i].c.id)
	}
	return out, ids
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func ensureChunkIndex(ctx context.Context, client *http.Client, base, apiKey, model string) error {
	indexOnce.Do(func() {
		texts := make([]string, len(photographyScoreChunks))
		for i := range photographyScoreChunks {
			texts[i] = photographyScoreChunks[i].text
		}
		var err error
		chunkEmbeds, err = MoonshotEmbedBatch(ctx, client, base, apiKey, model, texts)
		indexErr = err
		if indexErr != nil {
			log.Printf("rag: 向量索引构建失败（将降级关键词检索）: %v", indexErr)
			chunkEmbeds = nil
		}
	})
	return indexErr
}

func vectorTopK(queryVec []float64, topK int) ([]chunkrec, []float64, []string) {
	if topK <= 0 {
		topK = 4
	}
	type scored struct {
		i int
		s float64
	}
	var rows []scored
	for i := range chunkEmbeds {
		if len(chunkEmbeds[i]) != len(queryVec) {
			continue
		}
		rows = append(rows, scored{i: i, s: cosineSim(queryVec, chunkEmbeds[i])})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].s > rows[j].s })
	var out []chunkrec
	var sims []float64
	var ids []string
	for j := 0; j < len(rows) && len(out) < topK; j++ {
		idx := rows[j].i
		out = append(out, photographyScoreChunks[idx])
		sims = append(sims, rows[j].s)
		ids = append(ids, photographyScoreChunks[idx].id)
	}
	return out, sims, ids
}

// BuildPhotographyScoreRAG 根据客观量构造 query，检索 topK 条锚点，拼成追加到 system 的正文；meta 写入 gin上下文供响应展示。
func BuildPhotographyScoreRAG(ctx context.Context, httpClient *http.Client, cfg *config.RagConfig, apiKey, baseURL string, m photoscorer.Metrics, userExtra string) (systemAugment string, meta map[string]any) {
	meta = map[string]any{"enabled": false}
	if cfg == nil || !cfg.Enabled {
		return "", meta
	}
	topK := cfg.TopK
	if topK <= 0 {
		topK = 4
	}
	q := metricsToQueryText(m, userExtra)
	model := strings.TrimSpace(cfg.EmbeddingModel)
	if model == "" {
		model = "moonshot-embedding-v1"
	}

	var (
		chunks []chunkrec
		method string
		scores any
		ids    []string
	)

	if err := ensureChunkIndex(ctx, httpClient, baseURL, apiKey, model); err == nil && len(chunkEmbeds) > 0 {
		qv, qerr := MoonshotEmbed(ctx, httpClient, baseURL, apiKey, model, q)
		if qerr == nil && len(qv) > 0 {
			var sims []float64
			chunks, sims, ids = vectorTopK(qv, topK)
			method = "vector"
			scores = sims
			systemAugment = formatRAGBlock(chunks)
			meta = map[string]any{
				"enabled":           true,
				"method":            method,
				"top_k":             topK,
				"embedding_model": model,
				"retrieved_ids":     ids,
				"similarity":        scores,
				"query_fingerprint": hashPreview(q),
			}
			return systemAugment, meta
		}
		log.Printf("rag: query embed 失败，降级关键词: %v", qerr)
	}

	kw := inferKeywordTerms(m)
	chunks, ids = keywordScores(kw, topK)
	method = "keyword"
	scores = kw
	systemAugment = formatRAGBlock(chunks)
	meta = map[string]any{
		"enabled":           true,
		"method":            method,
		"top_k":             topK,
		"embedding_model":   model,
		"retrieved_ids":     ids,
		"matched_keywords":  kw,
		"query_fingerprint": hashPreview(q),
	}
	return systemAugment, meta
}

func hashPreview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 120 {
		return s
	}
	return s[:120] + "…"
}

func formatRAGBlock(chunks []chunkrec) string {
	if len(chunks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("【RAG 检索到的评分锚点（须与上图印证后再采用，不可机械照搬）】\n")
	for i, ch := range chunks {
		fmt.Fprintf(&b, "%d) [%s] %s\n", i+1, ch.id, ch.text)
	}
	b.WriteString("以上仅作标尺参考，最终分数仍以画面事实与量表一致性为准。")
	return b.String()
}
