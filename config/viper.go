package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

type Config struct {
	App   AppConfig   `mapstructure:"app"`
	JWT   JWTConfig   `mapstructure:"jwt"`
	MySQL MySQLConfig `mapstructure:"mysql"`
	Kimi  KimiConfig  `mapstructure:"kimi"`
}

type KimiConfig struct {
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
	BaseURL string `mapstructure:"base_url"`
}

type AppConfig struct {
	Name string `mapstructure:"name"`
	Port string `mapstructure:"port"`
	URL  string `mapstructure:"url"`
}

type JWTConfig struct {
	Secret      string `mapstructure:"secret"`
	AccessHours int    `mapstructure:"access_hours"`
}

type MySQLConfig struct {
	Skip     bool   `mapstructure:"skip"`
	Host     string `mapstructure:"host"`
	Port     string `mapstructure:"port"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	Database string `mapstructure:"database"`
}

var current *Config

func Load() (*Config, error) {
	viper.SetConfigName("app")
	viper.SetConfigType("yaml")
	// 多路径：避免从资源管理器启动 exe 时工作目录不是项目根，读不到 ./config/app.yaml
	if wd, err := os.Getwd(); err == nil {
		viper.AddConfigPath(filepath.Join(wd, "config"))
	}
	viper.AddConfigPath("./config")
	if exe, err := os.Executable(); err == nil {
		viper.AddConfigPath(filepath.Join(filepath.Dir(exe), "config"))
	}
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AllowEmptyEnv(false)
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		log.Println("Error reading config file:", err)
		return nil, fmt.Errorf("config: 未找到可读的配置文件（请在项目根或 exe 同目录下放置 config/app.yaml）: %w", err)
	}
	log.Printf("config: 使用 %s", viper.ConfigFileUsed())

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config failed: %w", err)
	}


	if strings.TrimSpace(cfg.Kimi.APIKey) == "" {
		cfg.Kimi.APIKey = strings.TrimSpace(viper.GetString("kimi.api_key"))
	}
	if strings.TrimSpace(cfg.Kimi.Model) == "" {
		cfg.Kimi.Model = strings.TrimSpace(viper.GetString("kimi.model"))
	}
	if strings.TrimSpace(cfg.Kimi.BaseURL) == "" {
		cfg.Kimi.BaseURL = strings.TrimSpace(viper.GetString("kimi.base_url"))
	}

	if k := strings.TrimSpace(os.Getenv("MOONSHOT_API_KEY")); k != "" {
		cfg.Kimi.APIKey = k
	}
	if k := strings.TrimSpace(os.Getenv("KIMI_API_KEY")); k != "" {
		cfg.Kimi.APIKey = k
	}
	if m := strings.TrimSpace(os.Getenv("KIMI_MODEL")); m != "" {
		cfg.Kimi.Model = m
	}
	if u := strings.TrimSpace(os.Getenv("KIMI_BASE_URL")); u != "" {
		cfg.Kimi.BaseURL = u
	}

	supplementKimiFromYAMLFile(&cfg, viper.ConfigFileUsed())

	if strings.TrimSpace(cfg.Kimi.APIKey) == "" {
		log.Println("config: kimi.api_key 为空：请在磁盘 config/app.yaml 保存密钥或使用环境变量 KIMI_API_KEY（仅在编辑器里改未保存时仍会为空）")
	}

	current = &cfg
	return current, nil
}

func supplementKimiFromYAMLFile(cfg *Config, configPath string) {
	if configPath == "" || cfg == nil {
		return
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Printf("config: 读取 %s 作 kimi 回填失败: %v", configPath, err)
		return
	}
	var doc struct {
		Kimi *struct {
			APIKey  string `yaml:"api_key"`
			Model   string `yaml:"model"`
			BaseURL string `yaml:"base_url"`
		} `yaml:"kimi"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		log.Printf("config: 解析 kimi 段失败: %v", err)
		return
	}
	if doc.Kimi == nil {
		return
	}
	if strings.TrimSpace(cfg.Kimi.APIKey) == "" && strings.TrimSpace(doc.Kimi.APIKey) != "" {
		cfg.Kimi.APIKey = strings.TrimSpace(doc.Kimi.APIKey)
	}
	if strings.TrimSpace(cfg.Kimi.Model) == "" && strings.TrimSpace(doc.Kimi.Model) != "" {
		cfg.Kimi.Model = strings.TrimSpace(doc.Kimi.Model)
	}
	if strings.TrimSpace(cfg.Kimi.BaseURL) == "" && strings.TrimSpace(doc.Kimi.BaseURL) != "" {
		cfg.Kimi.BaseURL = strings.TrimSpace(doc.Kimi.BaseURL)
	}
}

func MustLoad() *Config {
	cfg, err := Load()
	if err != nil {
		panic(err)
	}
	return cfg
}

func GetConfig() *Config {
	if current == nil {
		return MustLoad()
	}
	return current
}
