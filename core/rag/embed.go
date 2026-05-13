package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// MoonshotEmbed 调用与 OpenAI 兼容的 POST /v1/embeddings。
func MoonshotEmbed(ctx context.Context, client *http.Client, base, apiKey, model, input string) ([]float64, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, fmt.Errorf("embedding model 为空")
	}
	if input == "" {
		return nil, fmt.Errorf("embedding input 为空")
	}
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	payload := map[string]any{
		"model": model,
		"input": input,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/embeddings", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings http %d: %s", resp.StatusCode, string(body))
	}
	var root struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	if root.Error != nil && root.Error.Message != "" {
		return nil, fmt.Errorf("%s", root.Error.Message)
	}
	if len(root.Data) == 0 || len(root.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embeddings 返回为空")
	}
	return root.Data[0].Embedding, nil
}

// MoonshotEmbedBatch 单次请求嵌入多条文本（顺序与返回一致）。
func MoonshotEmbedBatch(ctx context.Context, client *http.Client, base, apiKey, model string, inputs []string) ([][]float64, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, fmt.Errorf("embedding model 为空")
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("inputs 为空")
	}
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	payload := map[string]any{"model": model, "input": inputs}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/embeddings", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings http %d: %s", resp.StatusCode, string(body))
	}
	var root struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	if root.Error != nil && root.Error.Message != "" {
		return nil, fmt.Errorf("%s", root.Error.Message)
	}
	out := make([][]float64, len(inputs))
	for _, d := range root.Data {
		if d.Index >= 0 && d.Index < len(out) && len(d.Embedding) > 0 {
			out[d.Index] = d.Embedding
		}
	}
	// 若上游未填 index，按 Data 顺序兜底
	if len(root.Data) == len(inputs) {
		for i := range root.Data {
			if len(out[i]) == 0 && len(root.Data[i].Embedding) > 0 {
				out[i] = root.Data[i].Embedding
			}
		}
	}
	for i := range out {
		if len(out[i]) == 0 {
			return nil, fmt.Errorf("embeddings 第 %d 条为空", i)
		}
	}
	return out, nil
}
