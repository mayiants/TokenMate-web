package plugin

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

type appendPlugin struct {
	name string
	char byte
}

func (p appendPlugin) Name() string { return p.name }
func (p appendPlugin) Transform(body []byte, ctx Context) ([]byte, error) {
	return append(body, p.char), nil
}

type failingPlugin struct{}

func (failingPlugin) Name() string { return "failing" }
func (failingPlugin) Transform(body []byte, ctx Context) ([]byte, error) {
	return nil, errors.New("boom")
}

type panicPlugin struct{}

func (panicPlugin) Name() string { return "panic" }
func (panicPlugin) Transform(body []byte, ctx Context) ([]byte, error) {
	panic("oops")
}

func TestChain_RunsPluginsInOrder(t *testing.T) {
	chain := NewChain(zap.NewNop(), appendPlugin{"a", 'a'}, appendPlugin{"b", 'b'})
	out := chain.Apply([]byte("x"), Context{})
	assert.Equal(t, "xab", string(out))
}

func TestChain_FailingPluginIsSkipped(t *testing.T) {
	chain := NewChain(zap.NewNop(), appendPlugin{"a", 'a'}, failingPlugin{}, appendPlugin{"b", 'b'})
	out := chain.Apply([]byte("x"), Context{})
	assert.Equal(t, "xab", string(out))
}

func TestChain_PanickingPluginIsSkipped(t *testing.T) {
	chain := NewChain(zap.NewNop(), appendPlugin{"a", 'a'}, panicPlugin{}, appendPlugin{"b", 'b'})
	out := chain.Apply([]byte("x"), Context{})
	assert.Equal(t, "xab", string(out))
}

func TestChain_NilIsNoop(t *testing.T) {
	var chain *Chain
	out := chain.Apply([]byte("x"), Context{})
	assert.Equal(t, "x", string(out))
}
