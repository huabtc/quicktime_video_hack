//go:build windows && !nogstreamer
// +build windows,!nogstreamer

package gstadapter

import "github.com/danielpaulus/gst"

func setupLivePlayAudio(pl *gst.Pipeline) {
	autoaudiosink := gst.ElementFactoryMake("autoaudiosink", "autoaudiosink_01")
	checkElem(autoaudiosink, "autoaudiosink_01")
	autoaudiosink.SetProperty("sync", false)
	pl.Add(autoaudiosink)
	pl.GetByName("queue2").Link(autoaudiosink)
}

func setUpVideoPipeline(pl *gst.Pipeline) *gst.AppSrc {
	asrc := gst.NewAppSrc("my-video-src")
	asrc.SetProperty("is-live", true)

	queue1 := gst.ElementFactoryMake("queue", "queue_11")
	checkElem(queue1, "queue_11")

	h264parse := gst.ElementFactoryMake("h264parse", "h264parse_01")
	checkElem(h264parse, "h264parse")

	// On Windows, use avdec_h264 (software decoder) or d3d11h264dec (hardware decoder if available)
	// Fallback to avdec_h264 for compatibility
	avdecH264 := gst.ElementFactoryMake("avdec_h264", "avdec_h264_01")
	checkElem(avdecH264, "avdec_h264_01")

	queue2 := gst.ElementFactoryMake("queue", "queue_12")
	checkElem(queue2, "queue_12")

	videoconvert := gst.ElementFactoryMake("videoconvert", "videoconvert_01")
	checkElem(videoconvert, "videoconvert_01")

	queue3 := gst.ElementFactoryMake("queue", "queue_13")
	checkElem(queue3, "queue_13")

	// On Windows, use autovideosink which will automatically select the best video sink
	// (usually d3dvideosink or dshowvideosink)
	sink := gst.ElementFactoryMake("autovideosink", "autovideosink_01")
	// setting this to true, creates extremely choppy video
	// I probably messed up something regarding the time stamps
	sink.SetProperty("sync", false)
	checkElem(sink, "autovideosink_01")

	pl.Add(asrc.AsElement(), queue1, h264parse, avdecH264, queue2, videoconvert, queue3, sink)

	asrc.Link(queue1)
	queue1.Link(h264parse)
	h264parse.Link(avdecH264)
	avdecH264.Link(queue2)
	queue2.Link(videoconvert)
	videoconvert.Link(queue3)
	queue3.Link(sink)
	return asrc
}


