package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	key := strings.TrimSpace(os.Getenv("KIMI_API_KEY"))
	if key == "" {
		key = strings.TrimSpace(os.Getenv("MOONSHOT_API_KEY"))
	}
	if key == "" {
		fmt.Fprintln(os.Stderr, "错误: 未设置 KIMI_API_KEY 或 MOONSHOT_API_KEY。请在环境变量或 .env 中填入密钥。")
		os.Exit(1)
	}

	base := strings.TrimSpace(os.Getenv("KIMI_BASE_URL"))
	if base == "" {
		base = "https://api.moonshot.ai/v1"
	}
	base = strings.TrimRight(base, "/")

	req, err := http.NewRequest(http.MethodGet, base+"/models", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "请求构建失败:", err)
		os.Exit(1)
	}
	req.Header.Set("Authorization", "Bearer "+key)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "请求失败:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Kimi API 返回 %s\n%s\n", resp.Status, string(body))
		os.Exit(1)
	}

	fmt.Println("ping: ok — 密钥可用，已列出模型（HTTP 200）")
}
