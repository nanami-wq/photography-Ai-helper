package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	App    AppConfig    `mapstructure:"app"`
	JWT    JWTConfig    `mapstructure:"jwt"`
	MySQL  MySQLConfig  `mapstructure:"mysql"`
	Gemini GeminiConfig `mapstructure:"gemini"`
}

type GeminiConfig struct {
	APIKey string `mapstructure:"api_key"`
	Model  string `mapstructure:"model"`
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

	// Unmarshal 在部分环境下可能未写入嵌套字段；以 viper 键为准回填
	if strings.TrimSpace(cfg.Gemini.APIKey) == "" {
		cfg.Gemini.APIKey = strings.TrimSpace(viper.GetString("gemini.api_key"))
	}
	if strings.TrimSpace(cfg.Gemini.Model) == "" {
		cfg.Gemini.Model = strings.TrimSpace(viper.GetString("gemini.model"))
	}

	if k := strings.TrimSpace(os.Getenv("GEMINI_API_KEY")); k != "" {
		cfg.Gemini.APIKey = k
	}
	if m := strings.TrimSpace(os.Getenv("GEMINI_MODEL")); m != "" {
		cfg.Gemini.Model = m
	}

	current = &cfg
	return current, nil
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
