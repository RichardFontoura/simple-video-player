package main

import (
	"fmt"
	"image"
	"log"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"github.com/golang/freetype"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/math/fixed"

	"video-player/pkg/audio"
	"video-player/pkg/video"
)

type Player struct {
	videoDecoder  *video.VideoDecoder
	audioDecoder  *audio.AudioDecoder
	window        fyne.Window
	canvas        *canvas.Image
	frameCounter  int
	lastFPSUpdate time.Time
	currentFPS    float64
	font          *truetype.Font
}

func NewPlayer() *Player {
	f, _ := truetype.Parse(goregular.TTF)
	return &Player{
		videoDecoder:  video.NewVideoDecoder(),
		audioDecoder:  audio.NewAudioDecoder(),
		frameCounter:  0,
		lastFPSUpdate: time.Now(),
		currentFPS:    0,
		font:          f,
	}
}

func (p *Player) Initialize(filepath string) error {
	if err := p.videoDecoder.Initialize(filepath); err != nil {
		return fmt.Errorf("erro ao inicializar vídeo: %v", err)
	}

	if err := p.audioDecoder.Initialize(filepath); err != nil {
		return fmt.Errorf("erro ao inicializar áudio: %v", err)
	}

	return nil
}

func (p *Player) drawFPS(img *image.RGBA) {
	c := freetype.NewContext()
	c.SetDPI(72)
	c.SetFont(p.font)
	c.SetFontSize(20)
	c.SetClip(img.Bounds())
	c.SetDst(img)
	c.SetHinting(font.HintingFull)

	text := fmt.Sprintf("FPS: %.1f", p.currentFPS)

	pt := freetype.Pt(10, 30)

	c.SetSrc(image.Black)
	shadowPt := freetype.Pt(
		int(pt.X+fixed.Int26_6(1.5*64))>>6,
		int(pt.Y+fixed.Int26_6(1.5*64))>>6,
	)
	_, _ = c.DrawString(text, shadowPt)

	c.SetSrc(image.White)
	_, _ = c.DrawString(text, pt)
}

func (p *Player) updateFPS() {
	p.frameCounter++
	now := time.Now()

	if now.Sub(p.lastFPSUpdate) >= time.Second {
		p.currentFPS = float64(p.frameCounter) / now.Sub(p.lastFPSUpdate).Seconds()
		p.frameCounter = 0
		p.lastFPSUpdate = now
	}
}

func (p *Player) playVideo() {
	frameBuffer := p.videoDecoder.GetFrameBuffer()
	p.videoDecoder.Start()

	nextFrameTime := time.Now()
	frameTime := time.Second / time.Duration(24)

	for frame := range frameBuffer {
		time.Sleep(time.Until(nextFrameTime))

		p.updateFPS()

		p.drawFPS(frame)

		p.canvas.Image = frame
		p.canvas.Refresh()

		nextFrameTime = nextFrameTime.Add(frameTime)
	}
}

func (p *Player) Start() {
	myApp := app.New()
	p.window = myApp.NewWindow("Video Player")
	p.window.SetFixedSize(true)
	p.window.CenterOnScreen()

	width, height := p.videoDecoder.GetDimensions()

	p.canvas = canvas.NewImageFromImage(nil)
	p.canvas.FillMode = canvas.ImageFillOriginal
	p.canvas.Resize(fyne.NewSize(float32(width), float32(height)))

	content := container.NewVBox(
		p.canvas,
	)

	p.window.SetContent(content)
	p.window.Resize(fyne.NewSize(800, 600))

	go p.audioDecoder.Start()
	go p.playVideo()

	p.window.ShowAndRun()
}

func (p *Player) Cleanup() {
	p.videoDecoder.Cleanup()
	p.audioDecoder.Cleanup()
}

func main() {
	player := NewPlayer()
	err := player.Initialize("InMyRemainsAMV.mp4")
	if err != nil {
		log.Printf("Erro ao inicializar player: %v\n", err)
		return
	}
	defer player.Cleanup()

	player.Start()
}
