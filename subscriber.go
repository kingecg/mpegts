package mpegts

import (
	"bytes"
	"net"
	"net/http"
	"strings"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	. "m7s.live/engine/v4"
	"m7s.live/engine/v4/codec"
	"m7s.live/engine/v4/codec/mpegts"
	"m7s.live/engine/v4/track"
)

type TsSubscriberMeta struct {
	asc                codec.AudioSpecificConfig
	packet             mpegts.MpegTsPESPacket
	video_cc, audio_cc uint16
}
type TsSubscriber struct {
	meta      TsSubscriberMeta
	pmt       []byte
	vcodec    codec.VideoCodecID
	acodec    codec.AudioCodecID
	tinterval float64
	Subscriber
}

func (c *TsSubscriber) OnEvent(event any) {
	var err error
	switch v := event.(type) {
	case AudioDeConf:
		c.meta.asc, err = DecodeAudioSpecificConfig(v.AVCC[0])
		if err != nil {
			return
		}
	case *track.Video:
		c.vcodec = v.CodecID
		var buffer bytes.Buffer
		mpegts.WritePMTPacket(&buffer, c.vcodec, c.acodec)
		c.pmt = buffer.Bytes()
		c.AddTrack(v)
	case *track.Audio:
		c.acodec = v.CodecID
		var buffer bytes.Buffer
		mpegts.WritePMTPacket(&buffer, c.vcodec, c.acodec)
		c.pmt = buffer.Bytes()
		c.AddTrack(v)
	case *AudioFrame:
		if c.meta.packet, err = AudioPacketToPES(v, c.meta.asc); err != nil {
			plugin.Error("Make audio pes error:" + err.Error())
			return
		}
		pes := &mpegts.MpegtsPESFrame{
			Pid:                       0x102,
			IsKeyFrame:                false,
			ContinuityCounter:         byte(c.meta.audio_cc % 16),
			ProgramClockReferenceBase: uint64(v.DTS - c.SkipTS*90),
		}
		if err = mpegts.WritePESPacket(c.Subscriber, pes, c.meta.packet); err != nil {
			return
		}
		c.meta.audio_cc = uint16(pes.ContinuityCounter)
	case *VideoFrame:
		pbuffer := &bytes.Buffer{}
		c.meta.packet, err = VideoPacketToPES(v, c.Video.Track.DecoderConfiguration, c.SkipTS)
		if err != nil {
			plugin.Error("Write video pes error:" + err.Error())
			return
		}
		if v.IFrame {
			ts := float64(v.AbsTime - c.SkipTS)
			if ts > c.tinterval {
				tbuffer := net.Buffers{
					mpegts.DefaultPATPacket,
					c.pmt,
				}
				tbuffer.WriteTo(pbuffer)
			}
		}
		pes := &mpegts.MpegtsPESFrame{
			Pid:                       0x101,
			IsKeyFrame:                v.IFrame,
			ContinuityCounter:         byte(c.meta.video_cc % 16),
			ProgramClockReferenceBase: uint64(v.DTS - c.SkipTS*90),
		}
		if err = mpegts.WritePESPacket(pbuffer, pes, c.meta.packet); err != nil {
			return
		}
		n, err := c.Write(pbuffer.Bytes())
		if err != nil {
			plugin.Error("Send packet error:" + err.Error())
			c.Stop()
		} else {
			plugin.Debug("send bytes count:" + string(rune(n)))
		}
		c.meta.video_cc = uint16(pes.ContinuityCounter)
	default:
		c.Subscriber.OnEvent(event)
	}
}

func (p *MpegtsConfig) handleWs(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		plugin.Error("Web socket upgrade error")
		return
	}
	streamPath := strings.TrimPrefix(r.URL.Path, "/ts")
	streamPath = strings.TrimPrefix(streamPath, "/")
	baseStream := Subscriber{}
	baseStream.SetIO(conn)               //注入writer
	baseStream.SetParentCtx(r.Context()) //注入context
	baseStream.ID = r.RemoteAddr
	// var specific ISubscriber
	specific := &TsSubscriber{meta: TsSubscriberMeta{}, Subscriber: baseStream}
	specific.tinterval = float64(p.Tinterval) * 1000
	go func() {
		defer conn.Close()

		for {
			msg, op, err := wsutil.ReadClientData(conn)
			if err != nil {
				return
			}
			if string(msg) == "ping" {
				err = wsutil.WriteServerMessage(conn, op, []byte("pong"))
				if err != nil {
					return
				}
			}
		}
	}()
	// specific := &TsSubscriber{baseStream, TsSubscriberMeta{}}
	if err := plugin.Subscribe(streamPath, specific); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
	specific.PlayRaw()
}

func (p *MpegtsConfig) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	if r.Header.Get("Upgrade") == "websocket" {
		p.handleWs(w, r)
	} else {
		streamPath := strings.TrimPrefix(r.URL.Path, "/ts")
		streamPath = strings.TrimPrefix(streamPath, "/")
		baseStream := Subscriber{}
		baseStream.SetIO(w)                  //注入writer
		baseStream.SetParentCtx(r.Context()) //注入context
		baseStream.ID = r.RemoteAddr
		// var specific ISubscriber
		specific := &TsSubscriber{meta: TsSubscriberMeta{}, Subscriber: baseStream}
		specific.tinterval = float64(p.Tinterval) * 1000
		if err := plugin.Subscribe(streamPath, specific); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		specific.PlayRaw()
	}
}
