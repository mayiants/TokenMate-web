// Package plugin provides a compile-time, config-driven request/response rewrite
// chain for ai-proxy-go. Plugins are plain Go types that implement the Plugin
// interface and are registered in cmd/server/main.go based on configuration.
package plugin

import (
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
)

// Context carries the request metadata that plugins use to decide whether they
// should act on a given body.
type Context struct {
	// ClientFormat is the API format the client originally sent (e.g. "openai",
	// "anthropic", "responses").
	ClientFormat string
	// UpstreamFormat is the API format the selected upstream provider expects.
	UpstreamFormat string
	// HostKey is a stable identifier for the upstream/host side. In practice it
	// is set to the upstream key ID or key hash.
	HostKey string
	// ClientName is a stable identifier for the client side. In practice it is
	// set to the user ID or User-Agent.
	ClientName string
}

// Plugin is the interface implemented by all rewrite plugins.
type Plugin interface {
	// Name returns the plugin identifier used in logs and config.
	Name() string
	// Transform rewrites the raw JSON request body. It must return the original
	// body unchanged when the plugin does not apply to the given context.
	Transform(body []byte, ctx Context) ([]byte, error)
}

// Chain runs a list of plugins in order. A nil Chain is safe to use and simply
// returns the input body unchanged.
type Chain struct {
	plugins []Plugin
	logger  *zap.Logger
}

// NewChain creates a plugin chain. Passing a nil logger is safe.
func NewChain(logger *zap.Logger, plugins ...Plugin) *Chain {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Chain{
		plugins: plugins,
		logger:  logger,
	}
}

// Empty reports whether the chain contains no plugins.
func (c *Chain) Empty() bool {
	return c == nil || len(c.plugins) == 0
}

// Apply runs all registered plugins in order. Each plugin receives the output
// of the previous plugin. If a plugin panics or returns an error, the error is
// logged and the original body passed to that plugin is returned, so that a
// single failing plugin cannot break the request.
func (c *Chain) Apply(body []byte, ctx Context) []byte {
	if c.Empty() {
		return body
	}

	for _, p := range c.plugins {
		name := p.Name()
		out, changed, err := applyPlugin(p, body, ctx)
		if err != nil {
			c.logger.Warn("plugin failed, using body before plugin",
				zap.String("plugin", name),
				zap.Error(err),
			)
			continue
		}
		if changed {
			c.logger.Info("plugin rewrote request body",
				zap.String("plugin", name),
				zap.Int("before", len(body)),
				zap.Int("after", len(out)),
			)
			body = out
		}
	}

	return body
}

func applyPlugin(p Plugin, body []byte, ctx Context) ([]byte, bool, error) {
	out, err := func() (out []byte, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic: %v", r)
			}
		}()
		return p.Transform(body, ctx)
	}()
	if err != nil {
		return nil, false, err
	}
	return out, !bytesEqual(out, body), nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// RawJSON is a small helper used by plugins to operate on generic JSON bodies
// without defining full request structs.
type RawJSON = json.RawMessage
