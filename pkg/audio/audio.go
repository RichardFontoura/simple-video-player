package audio

// #cgo pkg-config: libavcodec libavformat libavutil
// #cgo CFLAGS: -I${SRCDIR}/../../ffmpeg/include
// #cgo LDFLAGS: -L${SRCDIR}/../../ffmpeg/lib -lavformat -lavcodec -lavutil
/*
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavutil/frame.h>
*/
import "C"
import (
	"bytes"
	"fmt"
	"time"
	"unsafe"

	"github.com/hajimehoshi/oto/v2"
)

type AudioDecoder struct {
	formatContext   *C.AVFormatContext
	audioContext    *C.AVCodecContext
	audioFrame      *C.AVFrame
	audioStream     int
	audioQueue      chan []byte
	otoCtx          *oto.Context
	ready           chan struct{}
	audioPTS        float64
	audioBufferSize int
	audioStartTime  time.Time
}

func NewAudioDecoder() *AudioDecoder {
	return &AudioDecoder{
		audioStream:     -1,
		audioQueue:      make(chan []byte, 8192),
		audioBufferSize: 8192,
		audioStartTime:  time.Time{},
		audioPTS:        0,
		ready:           make(chan struct{}),
	}
}

func (a *AudioDecoder) Initialize(filepath string) error {
	a.formatContext = C.avformat_alloc_context()
	if a.formatContext == nil {
		return fmt.Errorf("falha ao alocar formato context para áudio")
	}

	cFilePath := C.CString(filepath)
	defer C.free(unsafe.Pointer(cFilePath))

	if C.avformat_open_input(&a.formatContext, cFilePath, nil, nil) != 0 {
		return fmt.Errorf("falha ao abrir arquivo de áudio")
	}

	for i := 0; i < int(a.formatContext.nb_streams); i++ {
		stream := (**C.AVStream)(unsafe.Pointer(a.formatContext.streams))
		streamVal := (*[1 << 30]*C.AVStream)(unsafe.Pointer(stream))[i]

		if streamVal.codecpar.codec_type == C.AVMEDIA_TYPE_AUDIO {
			a.audioStream = i
			break
		}
	}

	if a.audioStream == -1 {
		return fmt.Errorf("nenhum stream de áudio encontrado")
	}

	stream := (**C.AVStream)(unsafe.Pointer(a.formatContext.streams))
	streamVal := (*[1 << 30]*C.AVStream)(unsafe.Pointer(stream))[a.audioStream]

	codec := C.avcodec_find_decoder(streamVal.codecpar.codec_id)
	if codec == nil {
		return fmt.Errorf("codec de áudio não suportado")
	}

	a.audioContext = C.avcodec_alloc_context3(codec)
	if a.audioContext == nil {
		return fmt.Errorf("falha ao alocar contexto do codec de áudio")
	}

	C.avcodec_parameters_to_context(a.audioContext, streamVal.codecpar)

	if C.avcodec_open2(a.audioContext, codec, nil) < 0 {
		return fmt.Errorf("não foi possível abrir codec de áudio")
	}

	a.audioFrame = C.av_frame_alloc()

	var err error
	a.otoCtx, a.ready, err = oto.NewContext(
		int(a.audioContext.sample_rate),
		2,
		2,
	)
	if err != nil {
		return fmt.Errorf("erro ao criar contexto de áudio: %v", err)
	}

	return nil
}

func (a *AudioDecoder) PlayAudio() {
	<-a.ready
	const bufferSize = 8192
	audioBuffer := make([]byte, 0, bufferSize*4)
	var currentPlayer oto.Player

	for data := range a.audioQueue {
		audioBuffer = append(audioBuffer, data...)

		for len(audioBuffer) >= bufferSize {
			if currentPlayer != nil {
				currentPlayer.Close()
			}

			currentPlayer = a.otoCtx.NewPlayer(bytes.NewReader(audioBuffer[:bufferSize]))
			currentPlayer.Play()

			samplesPerBuffer := bufferSize / 4
			duration := time.Duration(float64(samplesPerBuffer) / float64(a.audioContext.sample_rate) * float64(time.Second))
			time.Sleep(duration)

			audioBuffer = audioBuffer[bufferSize:]
		}
	}

	if currentPlayer != nil {
		currentPlayer.Close()
	}
}

func (a *AudioDecoder) ProcessAudioFrame() error {
	packet := C.av_packet_alloc()
	defer C.av_packet_free(&packet)

	for C.av_read_frame(a.formatContext, packet) >= 0 {
		if int(packet.stream_index) == a.audioStream {
			timeBase := float64((*[1 << 30]*C.AVStream)(unsafe.Pointer(a.formatContext.streams))[a.audioStream].time_base.num) /
				float64((*[1 << 30]*C.AVStream)(unsafe.Pointer(a.formatContext.streams))[a.audioStream].time_base.den)
			a.audioPTS = float64(packet.pts) * timeBase

			response := C.avcodec_send_packet(a.audioContext, packet)
			if response < 0 {
				continue
			}

			for response >= 0 {
				response = C.avcodec_receive_frame(a.audioContext, a.audioFrame)
				if response == -11 || response == -541478725 {
					break
				}

				size := int(a.audioFrame.nb_samples) * 2 * 2
				audioData := make([]byte, size)

				for i := 0; i < int(a.audioFrame.nb_samples); i++ {
					for ch := 0; ch < 2; ch++ {
						sample := (*[1 << 30]float32)(unsafe.Pointer(a.audioFrame.data[ch]))[i]
						sample = a.processAudioSample(sample)

						pcm := int16(sample * 32767.0)
						idx := i*4 + ch*2
						audioData[idx] = byte(pcm)
						audioData[idx+1] = byte(pcm >> 8)
					}
				}

				a.audioQueue <- audioData
			}
		}
		C.av_packet_unref(packet)
	}

	return nil
}

func (a *AudioDecoder) processAudioSample(sample float32) float32 {
	sample = sample * 0.75

	if sample > 0.8 {
		sample = 0.8
	} else if sample < -0.8 {
		sample = -0.8
	}

	return sample
}

func (a *AudioDecoder) Start() {
	go a.ProcessAudioFrame()
	go a.PlayAudio()
}

func (a *AudioDecoder) Cleanup() {
	close(a.audioQueue)

	if a.audioFrame != nil {
		C.av_frame_free(&a.audioFrame)
	}
	if a.audioContext != nil {
		C.avcodec_free_context(&a.audioContext)
	}
	if a.formatContext != nil {
		C.avformat_close_input(&a.formatContext)
	}
}
