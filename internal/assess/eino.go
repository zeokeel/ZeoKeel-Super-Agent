package assess

import (
	"context"
	"errors"
	"fmt"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// EinoConfig 指向一个 OpenAI 兼容端点。
type EinoConfig struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

// NewEinoGenerator 用 eino 的 openai 组件做研判调用。
//
// 不用流式：研判的输出是一个小 JSON，流式只会让校验更难；Worker 的回答才需要
// 逐字出现（design §5.1）。
func NewEinoGenerator(ctx context.Context, cfg EinoConfig) (Generator, error) {
	if cfg.Model == "" {
		return nil, errors.New("assess: a model is required")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 90 * time.Second
	}
	mb, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		BaseURL: cfg.BaseURL,
		APIKey:  cfg.APIKey,
		Model:   cfg.Model,
		Timeout: cfg.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("assess: model: %w", err)
	}
	return &einoGenerator{mb: mb}, nil
}

type einoGenerator struct {
	mb model.BaseChatModel
}

// Generate 实现 Generator。
func (g *einoGenerator) Generate(ctx context.Context, system, user string) (string, error) {
	msg, err := g.mb.Generate(ctx, []*schema.Message{
		schema.SystemMessage(system),
		schema.UserMessage(user),
	})
	if err != nil {
		return "", err
	}
	if msg == nil {
		return "", errors.New("assess: empty reply")
	}
	return msg.Content, nil
}
