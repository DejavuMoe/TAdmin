// Package config loads TAdmin's deployment configuration from an optional
// TOML file and environment variables. Per-group behaviour lives in the
// database and is edited from the Telegram control panel.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Duration is a TOML string duration such as "45s" or "6h".
type Duration struct{ time.Duration }

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(strings.TrimSpace(string(b)))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// Config is the deployment configuration.
type Config struct {
	OwnerID   int64  `toml:"owner_id"`
	DataDir   string `toml:"data_dir"`
	Language  string `toml:"language"`
	Timezone  string `toml:"timezone"`
	LogFormat string `toml:"log_format"`
	LogLevel  string `toml:"log_level"`
	BotAPIURL string `toml:"bot_api_url"`

	Web     Web     `toml:"web"`
	Userbot Userbot `toml:"userbot"`
	Preview Preview `toml:"preview"`

	BotToken       string         `toml:"-"`
	TypeSafeAPIKey string         `toml:"-"`
	Location       *time.Location `toml:"-"`
}

// Web configures the optional Mini App server.
type Web struct {
	PublicURL string `toml:"public_url"`
	Listen    string `toml:"listen"`
}

// Userbot configures the optional MTProto account used for preview refresh.
type Userbot struct {
	APIID   int    `toml:"api_id"`
	APIHash string `toml:"api_hash"`
}

// Preview configures the link preview refresher.
type Preview struct {
	CheckInterval Duration `toml:"check_interval"`
	MinGap        Duration `toml:"min_gap"`
	DailyLimit    int      `toml:"daily_limit"`
	AllowPrivate  bool     `toml:"allow_private"`
}

// DatabasePath is the SQLite file inside DataDir.
func (c *Config) DatabasePath() string { return filepath.Join(c.DataDir, "tadmin.db") }

// WebEnabled reports whether the Mini App is configured.
func (c *Config) WebEnabled() bool { return c.Web.PublicURL != "" }

// UserbotEnabled reports whether MTProto credentials are configured.
func (c *Config) UserbotEnabled() bool { return c.Userbot.APIID > 0 && c.Userbot.APIHash != "" }

// Default returns a configuration with defaults applied.
func Default() Config {
	return Config{
		DataDir:   "data",
		Language:  "zh-CN",
		Timezone:  "Local",
		LogFormat: "json",
		LogLevel:  "info",
		Web:       Web{Listen: "127.0.0.1:8080"},
		Preview: Preview{
			CheckInterval: Duration{6 * time.Hour},
			MinGap:        Duration{45 * time.Second},
			DailyLimit:    300,
		},
	}
}

// Load reads path (which may be empty or missing when optional is true)
// and overlays environment variables.
func Load(path string, optional bool) (Config, error) {
	var data []byte
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			data = b
		case errors.Is(err, os.ErrNotExist) && optional:
		default:
			return Config{}, fmt.Errorf("read config %q: %w", path, err)
		}
	}
	return Parse(data, os.LookupEnv)
}

// Parse decodes TOML data and applies environment overrides via lookup.
func Parse(data []byte, lookup func(string) (string, bool)) (Config, error) {
	cfg := Default()
	if len(bytes.TrimSpace(data)) > 0 {
		dec := toml.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			var strict *toml.StrictMissingError
			if errors.As(err, &strict) {
				keys := make([]string, 0, len(strict.Errors))
				for i := range strict.Errors {
					keys = append(keys, strings.Join(strict.Errors[i].Key(), "."))
				}
				return Config{}, fmt.Errorf("config: unknown field(s): %s", strings.Join(keys, ", "))
			}
			return Config{}, fmt.Errorf("config: %w", err)
		}
	}
	if err := applyEnv(&cfg, lookup); err != nil {
		return Config{}, err
	}
	cfg.Web.PublicURL = strings.TrimRight(strings.TrimSpace(cfg.Web.PublicURL), "/")
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// EnvVar documents one environment variable. The table drives both
// configuration loading and the generated documentation.
type EnvVar struct {
	Name     string `json:"name"`
	TOML     string `json:"toml,omitempty"` // equivalent config.toml key
	Group    string `json:"group"`          // required | core | web | userbot | ai | advanced
	Desc     string `json:"desc"`           // zh-CN description for docs
	Example  string `json:"example,omitempty"`
	Secret   bool   `json:"secret,omitempty"`
	Required bool   `json:"required,omitempty"`
	set      func(c *Config, v string) error
}

// EnvVars lists every environment variable TAdmin reads, in apply order.
var EnvVars = []EnvVar{
	{Name: "TADMIN_BOT_TOKEN", Group: "required", Required: true, Secret: true, Example: "123456:ABC-DEF…",
		Desc: "Bot Token（@BotFather 获取）。只能通过环境变量提供，不会写入配置文件或日志。",
		set:  func(c *Config, v string) error { c.BotToken = v; return nil }},
	{Name: "TADMIN_BOT_TOKEN_FILE", Group: "required", Secret: true, Example: "/run/secrets/tadmin_token",
		Desc: "从文件读取 Bot Token，适合 Docker secrets / systemd credentials；仅在未设置 TADMIN_BOT_TOKEN 时生效。",
		set: func(c *Config, v string) error {
			if c.BotToken != "" {
				return nil
			}
			b, err := os.ReadFile(v)
			if err != nil {
				return fmt.Errorf("read TADMIN_BOT_TOKEN_FILE: %w", err)
			}
			c.BotToken = strings.TrimSpace(string(b))
			return nil
		}},
	{Name: "TADMIN_OWNER_ID", TOML: "owner_id", Group: "required", Required: true, Example: "123456789",
		Desc: "所有者的 Telegram 数字 ID（@userinfobot 可查），拥有全部权限。",
		set: func(c *Config, v string) error {
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return errors.New("TADMIN_OWNER_ID must be a numeric Telegram user id")
			}
			c.OwnerID = id
			return nil
		}},
	{Name: "TADMIN_DATA_DIR", TOML: "data_dir", Group: "core", Example: "/var/lib/tadmin",
		Desc: "数据目录，存放 tadmin.db（含 WAL 文件）与单实例锁。默认 data；Docker 镜像内为 /data。",
		set:  func(c *Config, v string) error { c.DataDir = v; return nil }},
	{Name: "TADMIN_TIMEZONE", TOML: "timezone", Group: "core", Example: "Asia/Shanghai",
		Desc: "统计按天划分与时间显示使用的时区（二进制内置时区数据库）。",
		set:  func(c *Config, v string) error { c.Timezone = v; return nil }},
	{Name: "TZ", Group: "core", Example: "Asia/Shanghai",
		Desc: "标准时区变量；仅在未设置 TADMIN_TIMEZONE 且配置为 Local 时作为回退。",
		set: func(c *Config, v string) error {
			if c.Timezone == "Local" {
				c.Timezone = v
			}
			return nil
		}},
	{Name: "TADMIN_LANGUAGE", TOML: "language", Group: "core", Example: "zh-CN",
		Desc: "默认语言：zh-CN 或 en。每个群还可以在控制台单独设置。",
		set:  func(c *Config, v string) error { c.Language = v; return nil }},
	{Name: "TADMIN_PUBLIC_URL", TOML: "web.public_url", Group: "web", Example: "https://verify.example.com",
		Desc: "可选。反向代理后的 HTTPS 源（不带路径）；配置后启用 Mini App 大图验证与 Guard 零窗口入群。",
		set:  func(c *Config, v string) error { c.Web.PublicURL = v; return nil }},
	{Name: "TADMIN_LISTEN", TOML: "web.listen", Group: "web", Example: "127.0.0.1:8080",
		Desc: "HTTP 监听地址，提供 /healthz 与 Mini App。默认 127.0.0.1:8080；容器内为 0.0.0.0:8080。",
		set:  func(c *Config, v string) error { c.Web.Listen = v; return nil }},
	{Name: "TADMIN_API_ID", TOML: "userbot.api_id", Group: "userbot", Example: "1234567",
		Desc: "可选。预览刷新用户号的 api_id（my.telegram.org 申请）。",
		set: func(c *Config, v string) error {
			id, err := strconv.Atoi(v)
			if err != nil {
				return errors.New("TADMIN_API_ID must be numeric")
			}
			c.Userbot.APIID = id
			return nil
		}},
	{Name: "TADMIN_API_HASH", TOML: "userbot.api_hash", Group: "userbot", Secret: true, Example: "0123456789abcdef…",
		Desc: "可选。与 api_id 配对的 api_hash。",
		set:  func(c *Config, v string) error { c.Userbot.APIHash = v; return nil }},
	{Name: "TADMIN_TYPESAFE_API_KEY", Group: "ai", Secret: true, Example: "ts_…",
		Desc: "可选。TypeSafe Jev API Key，启用 AI 入群资料风险提示（只升级处理，绝不自动放行或封禁）。",
		set:  func(c *Config, v string) error { c.TypeSafeAPIKey = v; return nil }},
	{Name: "TADMIN_BOT_API_URL", TOML: "bot_api_url", Group: "advanced", Example: "http://127.0.0.1:8081",
		Desc: "可选。自建 Local Bot API Server 的地址；默认 https://api.telegram.org。",
		set:  func(c *Config, v string) error { c.BotAPIURL = v; return nil }},
	{Name: "TADMIN_CONFIG", Group: "advanced", Example: "/etc/tadmin/config.toml",
		Desc: "配置文件路径（等同 -config 参数），文件不存在时忽略。默认 config.toml。"},
}

func applyEnv(cfg *Config, lookup func(string) (string, bool)) error {
	for _, e := range EnvVars {
		if e.set == nil {
			continue
		}
		v, ok := lookup(e.Name)
		v = strings.TrimSpace(v)
		if !ok || v == "" {
			continue
		}
		if err := e.set(cfg, v); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks the configuration. Missing bot token is reported here so
// that `tadmin check` can explain it.
func (c *Config) Validate() error {
	var errs []error
	if c.OwnerID <= 0 {
		errs = append(errs, errors.New("owner_id (or TADMIN_OWNER_ID) must be your numeric Telegram user id — ask @userinfobot"))
	}
	if strings.TrimSpace(c.DataDir) == "" {
		errs = append(errs, errors.New("data_dir must not be empty"))
	}
	if c.Language != "zh-CN" && c.Language != "en" {
		errs = append(errs, errors.New(`language must be "zh-CN" or "en"`))
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		errs = append(errs, fmt.Errorf("timezone %q: %w", c.Timezone, err))
	} else {
		c.Location = loc
	}
	if c.LogFormat != "json" && c.LogFormat != "text" {
		errs = append(errs, errors.New(`log_format must be "json" or "text"`))
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, errors.New("log_level must be debug, info, warn or error"))
	}
	if err := validatePublicURL(c.Web.PublicURL); err != nil {
		errs = append(errs, err)
	}
	if c.BotAPIURL != "" {
		if u, err := url.Parse(c.BotAPIURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			errs = append(errs, errors.New("bot_api_url must be an http(s) URL of a Bot API server"))
		}
	}
	if _, _, err := net.SplitHostPort(c.Web.Listen); err != nil {
		errs = append(errs, fmt.Errorf("web.listen: %w", err))
	}
	if (c.Userbot.APIID > 0) != (c.Userbot.APIHash != "") {
		errs = append(errs, errors.New("userbot.api_id and userbot.api_hash must be set together (https://my.telegram.org)"))
	}
	if c.Preview.CheckInterval.Duration < 10*time.Minute {
		errs = append(errs, errors.New("preview.check_interval must be at least 10m"))
	}
	if c.Preview.MinGap.Duration < 5*time.Second {
		errs = append(errs, errors.New("preview.min_gap must be at least 5s"))
	}
	if c.Preview.DailyLimit < 1 {
		errs = append(errs, errors.New("preview.daily_limit must be positive"))
	}
	if c.BotToken == "" {
		errs = append(errs, errors.New("TADMIN_BOT_TOKEN (or TADMIN_BOT_TOKEN_FILE) must be set — get a token from @BotFather"))
	} else if !strings.Contains(c.BotToken, ":") {
		errs = append(errs, errors.New("TADMIN_BOT_TOKEN does not look like a bot token"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid configuration:\n  - %w", joinLines(errs))
	}
	return nil
}

func joinLines(errs []error) error {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return errors.New(strings.Join(parts, "\n  - "))
}

func validatePublicURL(v string) error {
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil {
		return fmt.Errorf("web.public_url: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "https") || u.Host == "" || u.User != nil {
		return errors.New("web.public_url must be an absolute https:// origin")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("web.public_url must not contain a path, query or fragment")
	}
	return nil
}

// Sample is an annotated example configuration.
const Sample = `# TAdmin 配置（可选文件；所有项都能用环境变量覆盖）
# Bot Token 只从环境变量读取：TADMIN_BOT_TOKEN 或 TADMIN_BOT_TOKEN_FILE

owner_id = 123456789          # 你的 Telegram 数字 ID（@userinfobot 可查）；环境变量 TADMIN_OWNER_ID
data_dir = "data"             # SQLite 数据目录；环境变量 TADMIN_DATA_DIR
language = "zh-CN"            # 默认语言：zh-CN / en
timezone = "Asia/Shanghai"    # 统计与日报使用的时区
log_format = "json"           # json / text
log_level = "info"
# bot_api_url = "http://127.0.0.1:8081"   # 可选：自建 Local Bot API Server

[web]
# 可选：配置 HTTPS 域名后启用 Mini App 验证页与 Guard 零窗口入群。
# public_url = "https://verify.example.com"
listen = "127.0.0.1:8080"

[userbot]
# 可选：预览刷新需要一个用户号（建议小号）。在 https://my.telegram.org 申请。
# 登录在机器人私聊里扫码完成。api_hash 也可用环境变量 TADMIN_API_HASH。
# api_id = 0
# api_hash = ""

[preview]
check_interval = "6h"         # 订阅源检查间隔
min_gap = "45s"               # 两次刷新之间的最小间隔（另加随机抖动）
daily_limit = 300             # 每日最多刷新次数
allow_private = false         # 是否允许抓取内网地址
`
