package video

// #cgo pkg-config: libavcodec libavformat libswscale libavutil
// #cgo CFLAGS: -I${SRCDIR}/../../ffmpeg/include
// #cgo LDFLAGS: -L${SRCDIR}/../../ffmpeg/lib -lavformat -lavcodec -lavutil -lswscale
/*
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavutil/imgutils.h>
#include <libswscale/swscale.h>
#include <libavutil/frame.h>
#include <libavutil/mem.h>
#include <stdlib.h>
*/
import "C"
import (
	"fmt"
	"image"
	"time"
	"unsafe"
)

type VideoDecoder struct {
	formatContext *C.AVFormatContext
	codecContext  *C.AVCodecContext
	swsContext    *C.struct_SwsContext
	frame         *C.AVFrame
	frameRGB      *C.AVFrame
	packet        *C.AVPacket
	buffer        unsafe.Pointer
	videoStream   int
	frameBuffer   chan *image.RGBA
	quit          chan bool
	videoPTS      float64
	targetFPS     int
	frameTime     time.Duration
}

func NewVideoDecoder() *VideoDecoder {
	return &VideoDecoder{
		videoStream: -1,
		frameBuffer: make(chan *image.RGBA, 30),
		quit:        make(chan bool),
		targetFPS:   24,
		frameTime:   time.Second / 24,
		videoPTS:    0,
	}
}

func (v *VideoDecoder) Initialize(filepath string) error {
	v.formatContext = C.avformat_alloc_context()
	if v.formatContext == nil {
		return fmt.Errorf("falha ao alocar formato context")
	}

	cFilePath := C.CString(filepath)
	defer C.free(unsafe.Pointer(cFilePath))

	if C.avformat_open_input(&v.formatContext, cFilePath, nil, nil) != 0 {
		return fmt.Errorf("falha ao abrir arquivo de vídeo")
	}

	if C.avformat_find_stream_info(v.formatContext, nil) < 0 {
		return fmt.Errorf("falha ao encontrar informações do stream")
	}

	for i := 0; i < int(v.formatContext.nb_streams); i++ {
		stream := (**C.AVStream)(unsafe.Pointer(v.formatContext.streams))
		streamVal := (*[1 << 30]*C.AVStream)(unsafe.Pointer(stream))[i]

		if streamVal.codecpar.codec_type == C.AVMEDIA_TYPE_VIDEO {
			v.videoStream = i
			break
		}
	}

	if v.videoStream == -1 {
		return fmt.Errorf("nenhum stream de vídeo encontrado")
	}

	stream := (**C.AVStream)(unsafe.Pointer(v.formatContext.streams))
	streamVal := (*[1 << 30]*C.AVStream)(unsafe.Pointer(stream))[v.videoStream]
	codec := C.avcodec_find_decoder(streamVal.codecpar.codec_id)
	if codec == nil {
		return fmt.Errorf("codec não suportado")
	}

	v.codecContext = C.avcodec_alloc_context3(codec)
	C.avcodec_parameters_to_context(v.codecContext, streamVal.codecpar)

	if C.avcodec_open2(v.codecContext, codec, nil) < 0 {
		return fmt.Errorf("não foi possível abrir codec")
	}

	v.frame = C.av_frame_alloc()
	v.frameRGB = C.av_frame_alloc()

	v.swsContext = C.sws_getContext(
		v.codecContext.width,
		v.codecContext.height,
		v.codecContext.pix_fmt,
		v.codecContext.width,
		v.codecContext.height,
		C.AV_PIX_FMT_RGB24,
		C.SWS_BILINEAR,
		nil,
		nil,
		nil,
	)

	numBytes := C.av_image_get_buffer_size(
		C.AV_PIX_FMT_RGB24,
		v.codecContext.width,
		v.codecContext.height,
		1,
	)

	v.buffer = C.av_malloc(C.size_t(numBytes))
	C.av_image_fill_arrays(
		(**C.uint8_t)(&v.frameRGB.data[0]),
		(*C.int)(&v.frameRGB.linesize[0]),
		(*C.uint8_t)(v.buffer),
		C.AV_PIX_FMT_RGB24,
		v.codecContext.width,
		v.codecContext.height,
		1,
	)

	return nil
}

func (v *VideoDecoder) DecodeFrames() {
	v.packet = C.av_packet_alloc()
	defer C.av_packet_free(&v.packet)
	defer close(v.frameBuffer)

	for {
		select {
		case <-v.quit:
			return
		default:
			if C.av_read_frame(v.formatContext, v.packet) < 0 {
				return
			}

			if int(v.packet.stream_index) == v.videoStream {
				timeBase := float64((*[1 << 30]*C.AVStream)(unsafe.Pointer(v.formatContext.streams))[v.videoStream].time_base.num) /
					float64((*[1 << 30]*C.AVStream)(unsafe.Pointer(v.formatContext.streams))[v.videoStream].time_base.den)
				v.videoPTS = float64(v.packet.pts) * timeBase

				response := C.avcodec_send_packet(v.codecContext, v.packet)
				if response < 0 {
					C.av_packet_unref(v.packet)
					continue
				}

				for {
					response = C.avcodec_receive_frame(v.codecContext, v.frame)
					if response == -11 || response == -541478725 {
						break
					}

					C.sws_scale(
						v.swsContext,
						(**C.uint8_t)(unsafe.Pointer(&v.frame.data[0])),
						(*C.int)(unsafe.Pointer(&v.frame.linesize[0])),
						0,
						v.codecContext.height,
						(**C.uint8_t)(unsafe.Pointer(&v.frameRGB.data[0])),
						(*C.int)(unsafe.Pointer(&v.frameRGB.linesize[0])),
					)

					img := v.frameToImage()
					v.frameBuffer <- img
				}
			}
			C.av_packet_unref(v.packet)
		}
	}
}

func (v *VideoDecoder) frameToImage() *image.RGBA {
	width := int(v.codecContext.width)
	height := int(v.codecContext.height)
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	frameData := (*[1 << 30]uint8)(v.buffer)

	for y := 0; y < height; y++ {
		srcIdx := y * width * 3
		dstIdx := y * img.Stride

		for x := 0; x < width; x++ {
			img.Pix[dstIdx+x*4] = frameData[srcIdx+x*3]     // R
			img.Pix[dstIdx+x*4+1] = frameData[srcIdx+x*3+1] // G
			img.Pix[dstIdx+x*4+2] = frameData[srcIdx+x*3+2] // B
			img.Pix[dstIdx+x*4+3] = 255                     // A
		}
	}

	return img
}

func (v *VideoDecoder) Start() {
	go v.DecodeFrames()
}

func (v *VideoDecoder) GetFrameBuffer() chan *image.RGBA {
	return v.frameBuffer
}

func (v *VideoDecoder) GetDimensions() (width, height int) {
	return int(v.codecContext.width), int(v.codecContext.height)
}

func (v *VideoDecoder) Cleanup() {
	v.quit <- true

	if v.codecContext != nil {
		C.avcodec_free_context(&v.codecContext)
	}
	if v.formatContext != nil {
		C.avformat_close_input(&v.formatContext)
	}
	if v.frame != nil {
		C.av_frame_free(&v.frame)
	}
	if v.frameRGB != nil {
		C.av_frame_free(&v.frameRGB)
	}
	if v.buffer != nil {
		C.av_free(v.buffer)
	}
}
