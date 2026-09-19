package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const tokenEnvironmentVariable = "TADMIN_BOT_TOKEN"

type Config struct {
	OwnerUserID      int64  `toml:"owner_user_id"`
	DatabasePath     string `toml:"database_path"`
	ListenAddress    string `toml:"listen_address"`
	PublicBaseURL    string `toml:"public_base_url"`
	ManagementChatID int64  `toml:"management_chat_id"`
	DefaultLanguage  string `toml:"default_language"`
	BotToken         string `toml:"-"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config file %q: %w", path, err)
	}

	return Parse(data, os.Getenv(tokenEnvironmentVariable))
}

func Parse(data []byte, botToken string) (Config, error) {
	cfg := Config{
		ListenAddress:   "127.0.0.1:8080",
		DefaultLanguage: "zh-CN",
		BotToken:        strings.TrimSpace(botToken),
	}

	decoder := toml.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		var unknownFields *toml.StrictMissingError
		if errors.As(err, &unknownFields) {
			keys := make([]string, 0, len(unknownFields.Errors))
			for i := range unknownFields.Errors {
				keys = append(keys, strings.Join(unknownFields.Errors[i].Key(), "."))
			}
			return Config{}, fmt.Errorf("decode TOML config: unknown field(s): %s", strings.Join(keys, ", "))
		}
		return Config{}, fmt.Errorf("decode TOML config: %w", err)
	}

	cfg.DatabasePath = strings.TrimSpace(cfg.DatabasePath)
	cfg.ListenAddress = strings.TrimSpace(cfg.ListenAddress)
	cfg.PublicBaseURL = strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")
	cfg.DefaultLanguage = strings.TrimSpace(cfg.DefaultLanguage)

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	var validationErrors []error

	if c.OwnerUserID <= 0 {
		validationErrors = append(validationErrors, errors.New("owner_user_id must be a positive Telegram user ID"))
	}
	if c.DatabasePath == "" {
		validationErrors = append(validationErrors, errors.New("database_path must not be empty"))
	}
	if err := validateListenAddress(c.ListenAddress); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if err := validatePublicBaseURL(c.PublicBaseURL); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if c.DefaultLanguage != "zh-CN" && c.DefaultLanguage != "en" {
		validationErrors = append(validationErrors, errors.New("default_language must be either \"zh-CN\" or \"en\""))
	}
	if strings.TrimSpace(c.BotToken) == "" {
		validationErrors = append(validationErrors, fmt.Errorf("%s environment variable must be set", tokenEnvironmentVariable))
	}

	if len(validationErrors) > 0 {
		return fmt.Errorf("invalid configuration: %w", errors.Join(validationErrors...))
	}
	return nil
}

func validateListenAddress(address string) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("listen_address must be a host:port pair: %w", err)
	}

	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("listen_address port must be between 1 and 65535")
	}

	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("listen_address must use a loopback host such as 127.0.0.1 or ::1")
	}
	return nil
}

func validatePublicBaseURL(value string) error {
	if value == "" {
		return nil
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("public_base_url is invalid: %w", err)
	}
	if !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" {
		return errors.New("public_base_url must be an absolute HTTPS URL")
	}
	if parsed.User != nil {
		return errors.New("public_base_url must not contain credentials")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return errors.New("public_base_url must be an HTTPS origin without a path")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("public_base_url must not contain a query string or fragment")
	}
	return nil
}
