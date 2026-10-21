package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kbinani/screenshot"
)

// ffmpegPath sucht ffmpeg im PATH, neben dem Agent und an üblichen Orten (GUI-Start hat kaum PATH).
func ffmpegPath() string {
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name = "ffmpeg.exe"
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, p := range []string{filepath.Join(dir, name), filepath.Join(dir, "..", "Resources", name)} {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg", "/usr/bin/ffmpeg"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// ffmpegEncoders: Kandidaten je Plattform, Hardware zuerst. Fehlgeschlagene werden übersprungen.
func ffmpegEncoders() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"h264_videotoolbox", "libx264"}
	case "windows":
		return []string{"h264_nvenc", "h264_qsv", "h264_amf", "libx264"}
	}
	return []string{"h264_nvenc", "libx264"}
}

func encoderArgs(enc string, bps int, fps int) []string {
	b := strconv.Itoa(bps)
	buf := strconv.Itoa(bps / 2)
	gop := strconv.Itoa(fps * 4)
	switch enc {
	case "h264_videotoolbox":
		return []string{"-c:v", enc, "-realtime", "1", "-prio_speed", "1", "-profile:v", "main", "-b:v", b, "-maxrate", b, "-g", gop, "-bf", "0"}
	case "h264_nvenc":
		return []string{"-c:v", enc, "-preset", "p1", "-tune", "ull", "-zerolatency", "1", "-profile:v", "main", "-b:v", b, "-maxrate", b, "-bufsize", buf, "-g", gop, "-bf", "0"}
	case "h264_qsv":
		return []string{"-c:v", enc, "-preset", "veryfast", "-profile:v", "main", "-b:v", b, "-maxrate", b, "-g", gop, "-bf", "0"}
	case "h264_amf":
		return []string{"-c:v", enc, "-usage", "ultralowlatency", "-profile:v", "main", "-b:v", b, "-maxrate", b, "-g", gop, "-bf", "0"}
	}
	return []string{"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-profile:v", "main", "-b:v", b, "-maxrate", b, "-bufsize", buf, "-g", gop, "-bf", "0"}
}

type ffmpegVideo struct {
	p      *videoParams
	ctx    context.Context
	cancel context.CancelFunc
	enc    string
	bin    string

	alive   atomic.Bool
	bps     atomic.Int64
	restart atomic.Bool
	wg      sync.WaitGroup
}

func newFFmpegVideo(ctx context.Context, p *videoParams) (videoPipe, error) {
	bin := ffmpegPath()
	if bin == "" {
		return nil, fmt.Errorf("%w (ffmpeg nicht gefunden)", errNoVideo)
	}
	if screenshot.NumActiveDisplays() == 0 {
		return nil, errors.New("kein Bildschirm")
	}
	img, err := screenshot.CaptureRect(p.Bounds)
	if err != nil {
		return nil, err
	}
	p.OutW, p.OutH = even(img.Rect.Dx()), even(img.Rect.Dy())
	cctx, cancel := context.WithCancel(ctx)
	f := &ffmpegVideo{p: p, ctx: cctx, cancel: cancel, bin: bin, enc: ffmpegEncoders()[0]}
	f.bps.Store(int64(p.Bitrate))
	f.alive.Store(true)
	f.wg.Add(1)
	go f.run()
	return f, nil
}

func (f *ffmpegVideo) Name() string       { return "ffmpeg/" + f.enc }
func (f *ffmpegVideo) Alive() bool        { return f.alive.Load() }
func (f *ffmpegVideo) SetBitrate(bps int) { f.bps.Store(int64(bps)); f.restart.Store(true) }
func (f *ffmpegVideo) RequestKey()        { f.restart.Store(true) }
func (f *ffmpegVideo) Close()             { f.cancel(); f.wg.Wait() }

// run probiert Encoder der Reihe nach; ein Lauf endet bei Neustartwunsch (neues Schlüsselbild/Bitrate).
func (f *ffmpegVideo) run() {
	defer f.wg.Done()
	defer f.alive.Store(false)
	cands := ffmpegEncoders()
	ci := 0
	for f.ctx.Err() == nil && ci < len(cands) {
		f.enc = cands[ci]
		produced, err := f.once()
		if f.ctx.Err() != nil {
			return
		}
		if err != nil && !produced {
			log.Printf("ffmpeg/%s: %v", f.enc, err)
			ci++
			continue
		}
		if !f.restart.Load() { // unerwartet beendet
			log.Printf("ffmpeg/%s beendet: %v", f.enc, err)
			return
		}
	}
}

// once startet ffmpeg einmal und füttert es mit Bildschirmbildern.
func (f *ffmpegVideo) once() (produced bool, err error) {
	f.restart.Store(false)
	p := f.p
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "rawvideo", "-pix_fmt", "bgra",
		"-video_size", fmt.Sprintf("%dx%d", p.OutW, p.OutH), "-framerate", strconv.Itoa(p.FPS), "-i", "-", "-an"}
	args = append(args, encoderArgs(f.enc, int(f.bps.Load()), p.FPS)...)
	args = append(args, "-pix_fmt", "yuv420p", "-bsf:v", "h264_metadata=aud=insert", "-f", "h264", "-")
	cmd := exec.CommandContext(f.ctx, f.bin, args...)
	hideWindow(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		return false, err
	}

	var got atomic.Bool
	done := make(chan struct{})
	go func() { // Ausgabe in Access Units zerlegen
		defer close(done)
		splitAU(stdout, func(au []byte, key bool) {
			got.Store(true)
			p.Sink(au, key)
		})
	}()

	stop := make(chan struct{})
	go func() { // Bildschirm abgreifen
		defer stdin.Close()
		var prev []byte
		idle := 0
		tick := time.NewTicker(time.Second / time.Duration(p.FPS))
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-f.ctx.Done():
				return
			case <-tick.C:
			}
			if f.restart.Load() {
				return
			}
			img, err := screenshot.CaptureRect(p.Bounds)
			if err != nil || img.Rect.Dx() < p.OutW || img.Rect.Dy() < p.OutH {
				continue
			}
			pix := img.Pix
			if img.Stride != p.OutW*4 || img.Rect.Dy() != p.OutH { // auf Kodiergröße zuschneiden
				pix = make([]byte, 0, p.OutW*p.OutH*4)
				for y := 0; y < p.OutH; y++ {
					o := y * img.Stride
					pix = append(pix, img.Pix[o:o+p.OutW*4]...)
				}
			}
			if prev != nil && bytes.Equal(prev, pix) {
				// Unverändert: noch einige Wiederholungen, damit der Encoder verfeinert und
				// das letzte Bild ausgegeben wird, danach ruhen.
				if idle++; idle > 6 {
					continue
				}
			} else {
				idle = 0
			}
			prev = append(prev[:0], pix...)
			if _, err := stdin.Write(pix); err != nil {
				return
			}
		}
	}()

	<-done
	close(stop)
	err = cmd.Wait()
	if err != nil && stderr.Len() > 0 {
		err = fmt.Errorf("%v: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return got.Load(), err
}

// splitAU liest einen Annex-B-Strom und liefert je Access Unit (beginnt mit AUD, NAL-Typ 9).
func splitAU(r io.Reader, emit func(au []byte, key bool)) {
	var buf []byte
	tmp := make([]byte, 256<<10)
	flush := func(end int) {
		if end <= 0 {
			return
		}
		au := buf[:end]
		emit(append([]byte(nil), au...), hasIDR(au))
		buf = append(buf[:0], buf[end:]...)
	}
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		// AUD-Startcodes ab Position 1 suchen: 00 00 01 09 (ggf. mit führender 0)
		for i := 1; i+3 < len(buf); {
			j := bytes.Index(buf[i:], []byte{0, 0, 1, 9})
			if j < 0 {
				break
			}
			j += i
			start := j
			if start > 0 && buf[start-1] == 0 {
				start--
			}
			if start > 0 {
				flush(start)
				i = 1
			} else {
				i = j + 4
			}
		}
		if err != nil {
			return
		}
	}
}

func hasIDR(au []byte) bool {
	for i := 0; i+4 < len(au); i++ {
		if au[i] == 0 && au[i+1] == 0 && au[i+2] == 1 && au[i+3]&0x1f == 5 {
			return true
		}
	}
	return false
}
