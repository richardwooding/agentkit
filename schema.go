package agentkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/richardwooding/llmkit/core"
)

// FuncOption tunes schema generation for Func and NewFunc.
type FuncOption func(*funcConfig)

type funcConfig struct {
	schema     json.RawMessage
	forOpts    jsonschema.ForOptions
	strict     bool
	noValidate bool
}

// WithSchema supplies the full parameter schema instead of deriving it.
func WithSchema(params json.RawMessage) FuncOption {
	return func(c *funcConfig) { c.schema = params }
}

// WithForOptions passes options through to jsonschema.For.
func WithForOptions(o *jsonschema.ForOptions) FuncOption {
	return func(c *funcConfig) {
		if o != nil {
			c.forOpts = *o
		}
	}
}

// WithEnum constrains every field of type T to the given values.
func WithEnum[T ~string](values ...T) FuncOption {
	return func(c *funcConfig) {
		if c.forOpts.TypeSchemas == nil {
			c.forOpts.TypeSchemas = map[reflect.Type]*jsonschema.Schema{}
		}
		enum := make([]any, len(values))
		for i, v := range values {
			enum[i] = string(v)
		}
		c.forOpts.TypeSchemas[reflect.TypeFor[T]()] = &jsonschema.Schema{Type: "string", Enum: enum}
	}
}

// WithStrict marks the tool for OpenAI strict mode. Every property must then
// be required, so avoid omitempty fields.
func WithStrict() FuncOption { return func(c *funcConfig) { c.strict = true } }

// WithoutValidation skips schema validation of incoming arguments.
func WithoutValidation() FuncOption { return func(c *funcConfig) { c.noValidate = true } }

// SchemaFor derives the JSON Schema Func would use for T.
func SchemaFor[T any](opts ...FuncOption) (json.RawMessage, error) {
	cfg := applyFuncOptions(opts)
	s, _, err := buildSchema[T](&cfg)
	return s, err
}

func applyFuncOptions(opts []FuncOption) funcConfig {
	var cfg funcConfig
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

func buildSchema[T any](cfg *funcConfig) (json.RawMessage, *jsonschema.Resolved, error) {
	var schema *jsonschema.Schema
	if len(cfg.schema) > 0 {
		schema = &jsonschema.Schema{}
		if err := json.Unmarshal(cfg.schema, schema); err != nil {
			return nil, nil, fmt.Errorf("agentkit: parse schema: %w", err)
		}
	} else {
		var err error
		if schema, err = jsonschema.For[T](&cfg.forOpts); err != nil {
			return nil, nil, fmt.Errorf("agentkit: schema for %s: %w", reflect.TypeFor[T](), err)
		}
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, nil, fmt.Errorf("agentkit: encode schema: %w", err)
	}
	if cfg.noValidate {
		return raw, nil, nil
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("agentkit: resolve schema: %w", err)
	}
	return raw, resolved, nil
}

type funcTool[In, Out any] struct {
	def      core.Tool
	resolved *jsonschema.Resolved
	fn       func(context.Context, In) (Out, error)
}

// Definition implements Tool.
func (t *funcTool[In, Out]) Definition() core.Tool { return t.def }

// Call implements Tool.
func (t *funcTool[In, Out]) Call(ctx context.Context, args json.RawMessage) (Output, error) {
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	if t.resolved != nil {
		var instance any
		if err := json.Unmarshal(args, &instance); err != nil {
			return Errorf("invalid JSON arguments: %v", err), fmt.Errorf("%w: %w", ErrInvalidArgs, err)
		}
		if err := t.resolved.Validate(instance); err != nil {
			return Errorf("invalid arguments: %v", err), fmt.Errorf("%w: %w", ErrInvalidArgs, err)
		}
	}
	var in In
	if err := json.Unmarshal(args, &in); err != nil {
		return Errorf("invalid arguments: %v", err), fmt.Errorf("%w: %w", ErrInvalidArgs, err)
	}
	out, err := t.fn(ctx, in)
	if err != nil {
		return Errorf("%v", err), err
	}
	return marshalOut(out)
}

func marshalOut(out any) (Output, error) {
	switch v := out.(type) {
	case Output:
		return v, nil
	case string:
		return Text(v), nil
	case []core.Part:
		return Output{Content: v}, nil
	case nil:
		return Text("ok"), nil
	}
	if reflect.TypeOf(out).Size() == 0 {
		return Text("ok"), nil
	}
	return JSON(out)
}

// Func builds a Tool whose arguments are decoded into In and whose result is
// Out. The schema is derived from In; arguments are validated against it
// before decoding. Func panics when In cannot be represented; use NewFunc for
// a recoverable error.
func Func[In, Out any](name, description string, fn func(context.Context, In) (Out, error), opts ...FuncOption) Tool {
	t, err := NewFunc(name, description, fn, opts...)
	if err != nil {
		panic(err)
	}
	return t
}

// NewFunc is Func returning an error instead of panicking.
func NewFunc[In, Out any](name, description string, fn func(context.Context, In) (Out, error), opts ...FuncOption) (Tool, error) {
	if name == "" {
		return nil, errors.New("agentkit: tool name is required")
	}
	if fn == nil {
		return nil, fmt.Errorf("agentkit: tool %q has no function", name)
	}
	cfg := applyFuncOptions(opts)
	raw, resolved, err := buildSchema[In](&cfg)
	if err != nil {
		return nil, err
	}
	return &funcTool[In, Out]{
		def:      core.Tool{Name: name, Description: description, Parameters: raw, Strict: cfg.strict},
		resolved: resolved,
		fn:       fn,
	}, nil
}
