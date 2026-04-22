package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/joho/godotenv"
)

const listModelsURL = "https://generativelanguage.googleapis.com/v1/models"

func main() {
	_ = godotenv.Load()

	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "错误: 未设置 GEMINI_API_KEY。请复制 .env.example 为 .env 并填入密钥，或设置环境变量。")
		os.Exit(1)
	}

	u, err := url.Parse(listModelsURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "URL 解析失败:", err)
		os.Exit(1)
	}
	q := u.Query()
	q.Set("key", key)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "请求构建失败:", err)
		os.Exit(1)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "请求失败:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Gemini API 返回 %s\n%s\n", resp.Status, string(body))
		os.Exit(1)
	}

	fmt.Println("ping: ok — 密钥可用，已列出模型（HTTP 200）")
}
