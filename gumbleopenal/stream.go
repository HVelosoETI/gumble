package gumbleopenal // import "github.com/talkkonnect/gumble/gumbleopenal"

import (
	"encoding/binary"
	"errors"
	hd44780 "github.com/talkkonnect/go-hd44780"
	"github.com/talkkonnect/go-openal/openal"
	"github.com/talkkonnect/gpio"
	"github.com/talkkonnect/gumble/gumble"
	"log"
	"time"
)

var (
	ErrState                   = errors.New("gumbleopenal: invalid state")
	lastspeaker                = "Nil"
	lcdtext                    = [4]string{"nil", "nil", "nil", ""} //global variable declaration for 4 lines of LCD
	BackLightTime  *time.Timer
	LCDBackLightTimeoutSecs int = 0
	BackLightPin          uint = 0
	RxVoiceActivityLedPin uint = 0
	RSPin                 int  = 0
	EPin                  int  = 0
	D4Pin                 int  = 0
	D5Pin                 int  = 0
	D6Pin                 int  = 0
	D7Pin                 int  = 0
)

type Stream struct {
	client *gumble.Client
	link   gumble.Detacher

	deviceSource    *openal.CaptureDevice
	sourceFrameSize int
	sourceStop      chan bool

	deviceSink  *openal.Device
	contextSink *openal.Context
}

func New(client *gumble.Client, voiceActivityLedPin uint, backLightPin uint, BackLightTimer *time.Timer, lCDBackLightTimeoutSecs int, PRSPin int, PEPin int, PD4Pin int, PD5Pin int, PD6Pin int, PD7Pin int) (*Stream, error) {
	LCDBackLightTimeoutSecs = lCDBackLightTimeoutSecs
	RxVoiceActivityLedPin = voiceActivityLedPin
	BackLightPin = backLightPin
	BackLightTime = BackLightTimer
	RSPin = PRSPin
	EPin  = PEPin
	D4Pin = PD4Pin
	D5Pin = PD5Pin
	D6Pin = PD6Pin
	D7Pin = PD7Pin

	s := &Stream{
		client:          client,
		sourceFrameSize: client.Config.AudioFrameSize(),
	}
	s.deviceSource = openal.CaptureOpenDevice("", gumble.AudioSampleRate, openal.FormatMono16, uint32(s.sourceFrameSize))

	s.deviceSink = openal.OpenDevice("")

	s.contextSink = s.deviceSink.CreateContext()

	s.contextSink.Activate()

	s.link = client.Config.AttachAudio(s)

	return s, nil
}

func (s *Stream) Destroy() {
	s.link.Detach()
	if s.deviceSource != nil {
		s.StopSource()
		s.deviceSource.CaptureCloseDevice()
		s.deviceSource = nil
	}
	if s.deviceSink != nil {
		s.contextSink.Destroy()
		s.deviceSink.CloseDevice()
		s.contextSink = nil
		s.deviceSink = nil
	}
}

func (s *Stream) StartSourceFile() error {
	if s.sourceStop != nil {
		return ErrState
	}
	s.deviceSource.CaptureStart()
	s.sourceStop = make(chan bool)
	go s.sourceRoutine()
	return nil
}

func (s *Stream) StartSource() error {
	if s.sourceStop != nil {
		return ErrState
	}
	s.deviceSource.CaptureStart()
	s.sourceStop = make(chan bool)
	go s.sourceRoutine()
	return nil
}

func (s *Stream) StopSource() error {
	if s.sourceStop == nil {
		return ErrState
	}
	close(s.sourceStop)
	s.sourceStop = nil
	s.deviceSource.CaptureStop()

	time.Sleep(100 * time.Millisecond)

	s.deviceSource.CaptureCloseDevice()
	s.deviceSource = nil

	s.deviceSource = openal.CaptureOpenDevice("", gumble.AudioSampleRate, openal.FormatMono16, uint32(s.sourceFrameSize))

	return nil
}

func (s *Stream) OnAudioStream(e *gumble.AudioStreamEvent) {
	pinA := gpio.NewOutput(RxVoiceActivityLedPin, false)
	pinB := gpio.NewOutput(BackLightPin, false)
	pinA.Low()
	pinB.Low()

	timertalkled := time.NewTimer(time.Millisecond * 20)


	var watchpin = true

	go func() {
		for watchpin {
			<-timertalkled.C
			//log.Printf("warn: Inside Stream Address of Timer %#v\n",BackLightTimer)
			pinA.Low()
			lastspeaker = "Nil"
		}
	}()

	go func() {
		source := openal.NewSource()
		emptyBufs := openal.NewBuffers(8)
		reclaim := func() {
			if n := source.BuffersProcessed(); n > 0 {
				reclaimedBufs := make(openal.Buffers, n)
				source.UnqueueBuffers(reclaimedBufs)
				emptyBufs = append(emptyBufs, reclaimedBufs...)
			}
		}
		var raw [gumble.AudioMaximumFrameSize * 2]byte

		for packet := range e.C {
			samples := len(packet.AudioBuffer)
			pinA.High()
			pinB.High()

			timertalkled.Reset(time.Second)
			if samples > cap(raw) {
				continue
			}
			for i, value := range packet.AudioBuffer {
				binary.LittleEndian.PutUint16(raw[i*2:], uint16(value))
			}
			reclaim()
			if len(emptyBufs) == 0 {
				continue
			}
			last := len(emptyBufs) - 1
			buffer := emptyBufs[last]
			emptyBufs = emptyBufs[:last]
			buffer.SetData(openal.FormatMono16, raw[:samples*2], gumble.AudioSampleRate)
			source.QueueBuffer(buffer)
			if source.State() != openal.Playing {
				source.Play()
				if lastspeaker != e.User.Name {
					log.Println("Speaking:", e.User.Name)
					lastspeaker = e.User.Name
					t := time.Now()
					lcdtext = [4]string{"nil", "", "", e.User.Name + " " + t.Format("15:04:05")}
					BackLightTime.Reset(time.Duration(LCDBackLightTimeoutSecs) * time.Second)
					go hd44780.LcdDisplay(lcdtext, RSPin, EPin, D4Pin, D5Pin, D6Pin, D7Pin)
					//log.Printf("debug: LCD Backlight Timer Address %v", BackLightTime, " Reset By Audio Stream\n")
				}
			}
		}
		watchpin = false
		reclaim()
		emptyBufs.Delete()
		source.Delete()
	}()
}

func (s *Stream) sourceRoutine() {
	interval := s.client.Config.AudioInterval
	frameSize := s.client.Config.AudioFrameSize()

	if frameSize != s.sourceFrameSize {
		s.deviceSource.CaptureCloseDevice()
		s.sourceFrameSize = frameSize
		s.deviceSource = openal.CaptureOpenDevice("", gumble.AudioSampleRate, openal.FormatMono16, uint32(s.sourceFrameSize))
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	stop := s.sourceStop

	outgoing := s.client.AudioOutgoing()
	defer close(outgoing)

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			buff := s.deviceSource.CaptureSamples(uint32(frameSize))
			if len(buff) != frameSize*2 {
				continue
			}
			int16Buffer := make([]int16, frameSize)
			for i := range int16Buffer {
				int16Buffer[i] = int16(binary.LittleEndian.Uint16(buff[i*2 : (i+1)*2]))
			}
			outgoing <- gumble.AudioBuffer(int16Buffer)
		}
	}
}
