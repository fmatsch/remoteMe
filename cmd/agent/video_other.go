//go:build !darwin

package main

// Kein nativer Encoder: Windows/Linux nutzen ffmpeg (video_ffmpeg.go).
func newNativeVideo(p *videoParams) (videoPipe, error) { return nil, errNoVideo }
