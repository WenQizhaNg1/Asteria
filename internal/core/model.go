package core

import "context"

// Model returns structured decisions. Knowledge files and sources are untrusted
// inputs; application rules are always enforced after the model returns.
type Model interface {
	Complete(ctx context.Context, instruction string, input any, output any) error
}

// ModelFunc makes the protocol replaceable and supports deterministic tests.
type ModelFunc func(context.Context, string, any, any) error

func (fn ModelFunc) Complete(ctx context.Context, instruction string, input any, output any) error {
	return fn(ctx, instruction, input, output)
}
