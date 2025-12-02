//go:build nogstreamer
// +build nogstreamer

package gstadapter

import (
	"fmt"

	"github.com/huabtc/quicktime_video_hack/screencapture/coremedia"
)

// Stubbed adapter used when building with -tags nogstreamer to avoid CGO deps.
type GstAdapter struct{}

const (
	MP3 = "mp3"
	OGG = "ogg"
)

func New() *GstAdapter {
	panic("gstreamer disabled: rebuild without '-tags nogstreamer' or install gstreamer")
}

func NewWithAudioPipeline(string, string) (*GstAdapter, error) {
	return nil, fmt.Errorf("gstreamer disabled: rebuild without '-tags nogstreamer' or install gstreamer")
}

func NewWithCustomPipeline(string) (*GstAdapter, error) {
	return nil, fmt.Errorf("gstreamer disabled: rebuild without '-tags nogstreamer' or install gstreamer")
}

func (GstAdapter) Consume(coremedia.CMSampleBuffer) error {
	return fmt.Errorf("gstreamer disabled: rebuild without '-tags nogstreamer' or install gstreamer")
}

func (GstAdapter) Stop() {}
