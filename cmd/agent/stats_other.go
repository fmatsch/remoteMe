//go:build !windows && !linux && !darwin

package main

func takeSample() sample { return sample{} }
