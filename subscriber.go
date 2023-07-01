package mpegts

import (
	"bytes"
	"net"
	"net/http"
	"strings"
	"time"

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
	IsWebSocket bool
	isPmtSend   bool
}

func (c *TsSubscriber) WriteOut(data []byte) {

	if c.IsWebSocket {
		var header ws.Header = ws.Header{
			Fin:    true,
			OpCode: ws.OpBinary,
			Length: int64(len(data)),
		}
		err := ws.WriteHeader(c, header)
		defer func() {
			if err != nil {
				c.Stop()
			}
		}()
	}

	c.Writer.(net.Conn).SetWriteDeadline(time.Now().Add(60 * time.Second))
	_, err := c.Write(data)
	if err != nil {
		defer c.Stop()
	}
}
func (c *TsSubscriber) OnEvent(event any) {
	var err error

	switch v := event.(type) {
	case AudioDeConf:

		c.meta.asc, err = DecodeAudioSpecificConfig(v.WithOutRTMP())
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
	case AudioFrame:

		if c.meta.packet, err = AudioPacketToPES(v, c.meta.asc); err != nil {
			plugin.Error("Make audio pes error:" + err.Error())
			return
		}
		pes := &mpegts.MpegtsPESFrame{
			Pid:                       0x102,
			IsKeyFrame:                false,
			ContinuityCounter:         byte(c.meta.audio_cc % 16),
			ProgramClockReferenceBase: uint64(v.DTS - uint32(c.AudioReader.SkipTs)*90),
		}
		// if err = WritePESPacket(c.Subscriber, pes, c.meta.packet); err != nil {
		// 	return
		// }
		pbuffer := &bytes.Buffer{}
		WritePESPacket(pbuffer, pes, c.meta.packet)
		c.WriteOut(pbuffer.Bytes())

		c.meta.audio_cc = uint16(pes.ContinuityCounter)
	case VideoFrame:

		pbuffer := &bytes.Buffer{}

		c.meta.packet, err = VideoPacketToPES(v, uint32(c.VideoReader.SkipTs))

		if err != nil {
			plugin.Error("Write video pes error:" + err.Error())
			return
		}

		if v.IFrame && !c.isPmtSend {
			// ts := float64(v.AbsTime - uint32(c.VideoReader.SkipTs))

			tbuffer := net.Buffers{
				mpegts.DefaultPATPacket,
				c.pmt,
			}
			tbuffer.WriteTo(pbuffer)
			c.isPmtSend = true
		}
		pes := &mpegts.MpegtsPESFrame{
			Pid:                       0x101,
			IsKeyFrame:                v.IFrame,
			ContinuityCounter:         byte(c.meta.video_cc % 16),
			ProgramClockReferenceBase: uint64(v.DTS - uint32(c.VideoReader.SkipTs)*90),
		}
		c.meta.video_cc = uint16(pes.ContinuityCounter)
		if err = WritePESPacket(pbuffer, pes, c.meta.packet); err != nil {
			plugin.Error("Send packet error r:" + err.Error())
			return
		}
		tcb := pbuffer.Bytes()
		c.WriteOut(tcb)

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
	specific := &TsSubscriber{meta: TsSubscriberMeta{}, Subscriber: baseStream, IsWebSocket: true}
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

	if err := plugin.Subscribe(streamPath, specific); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
	specific.PlayRaw()
}

func (p *MpegtsConfig) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	if r.Header.Get("Upgrade") == "websocket" {
		p.handleWs(w, r)
	} else {
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Transfer-Encoding", "identity")
		w.WriteHeader(http.StatusOK)
		var conn net.Conn
		if hijacker, ok := w.(http.Hijacker); ok {
			conn, _, _ = hijacker.Hijack()
		} else {
			w.(http.Flusher).Flush()
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
		if err := plugin.Subscribe(streamPath, specific); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		specific.PlayRaw()
	}
}
