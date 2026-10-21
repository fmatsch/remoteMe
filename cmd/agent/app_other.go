//go:build !darwin

package main

import "context"

func runApp(ctx context.Context, cancel context.CancelFunc, a *Agent, showSetup bool) { <-ctx.Done() }

const nativeUI = false
