//go:build !windows && !linux && !darwin

package main

type noInput struct{}

func newInput() Input                          { return noInput{} }
func (noInput) Move(x, y int)                  {}
func (noInput) Button(int, bool, int, int)     {}
func (noInput) Wheel(dx, dy float64)           {}
func (noInput) Key(string, string, bool, bool) {}
func (noInput) ReleaseAll()                    {}
