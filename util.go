package mpegts

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	. "m7s.live/engine/v4/codec/mpegts"
	"m7s.live/engine/v4/util"
)

func PESToTs(frame *MpegtsPESFrame, packet MpegTsPESPacket) (tsPkts []byte, err error) {
	if packet.Header.PacketStartCodePrefix != 0x000001 {
		err = errors.New("packetStartCodePrefix != 0x000001")
		return
	}
	bwTsHeader := &bytes.Buffer{}
	bwPESPkt := &bytes.Buffer{}
	_, err = WritePESHeader(bwPESPkt, packet.Header)
	if err != nil {
		return
	}

	if _, err = bwPESPkt.Write(packet.Payload); err != nil {
		return
	}

	var tsHeaderLength int
	for i := 0; bwPESPkt.Len() > 0; i++ {
		bwTsHeader.Reset()
		tsHeader := MpegTsHeader{
			SyncByte:                   0x47,
			TransportErrorIndicator:    0,
			PayloadUnitStartIndicator:  0,
			TransportPriority:          0,
			Pid:                        frame.Pid,
			TransportScramblingControl: 0,
			AdaptionFieldControl:       1,
			ContinuityCounter:          frame.ContinuityCounter,
		}

		frame.ContinuityCounter++
		frame.ContinuityCounter = frame.ContinuityCounter % 16

		// 每一帧的开头,当含有pcr的时候,包含调整字段
		if i == 0 {
			tsHeader.PayloadUnitStartIndicator = 1

			// 当PCRFlag为1的时候,包含调整字段
			if frame.IsKeyFrame {
				tsHeader.AdaptionFieldControl = 0x03
				tsHeader.AdaptationFieldLength = 7
				tsHeader.PCRFlag = 1
				tsHeader.RandomAccessIndicator = 1
				tsHeader.ProgramClockReferenceBase = frame.ProgramClockReferenceBase
			}
		}

		pesPktLength := bwPESPkt.Len()

		// 每一帧的结尾,当不满足188个字节的时候,包含调整字段
		if pesPktLength < TS_PACKET_SIZE-4 {
			var tsStuffingLength uint8

			tsHeader.AdaptionFieldControl = 0x03
			tsHeader.AdaptationFieldLength = uint8(TS_PACKET_SIZE - 4 - 1 - pesPktLength)

			// TODO:如果第一个TS包也是最后一个TS包,是不是需要考虑这个情况?
			// MpegTsHeader最少占6个字节.(前4个走字节 + AdaptationFieldLength(1 byte) + 3个指示符5个标志位(1 byte))
			if tsHeader.AdaptationFieldLength >= 1 {
				tsStuffingLength = tsHeader.AdaptationFieldLength - 1
			} else {
				tsStuffingLength = 0
			}

			// error
			tsHeaderLength, err = WriteTsHeader(bwTsHeader, tsHeader)
			if err != nil {
				return
			}
			stuffing := util.GetFillBytes(0xff, TS_PACKET_SIZE)
			if tsStuffingLength > 0 {
				if _, err = bwTsHeader.Write(stuffing[:tsStuffingLength]); err != nil {
					return
				}
			}

			tsHeaderLength += int(tsStuffingLength)
		} else {
			tsHeaderLength, err = WriteTsHeader(bwTsHeader, tsHeader)
			if err != nil {
				return
			}
		}

		tsPayloadLength := TS_PACKET_SIZE - tsHeaderLength

		//fmt.Println("tsPayloadLength :", tsPayloadLength)

		// 这里不断的减少PES包
		tsHeaderByte := bwTsHeader.Bytes()
		tsPayloadByte := bwPESPkt.Next(tsPayloadLength)

		// tmp := tsHeaderByte[3] << 2
		// tmp = tmp >> 6
		// if tmp == 2 {
		// 	fmt.Println("fuck you mother.")
		// }
		tsPktByteLen := len(tsHeaderByte) + len(tsPayloadByte)

		if tsPktByteLen != TS_PACKET_SIZE {
			err = errors.New(fmt.Sprintf("%s, packet size=%d", "TS_PACKET_SIZE != 188,", tsPktByteLen))
			return
		}
		tsPkts = append(tsPkts, tsHeaderByte...)
		tsPkts = append(tsPkts, tsPayloadByte...)
	}

	return
}

func WritePESPacket(w io.Writer, frame *MpegtsPESFrame, packet MpegTsPESPacket) (err error) {
	var tsPkts []byte
	if tsPkts, err = PESToTs(frame, packet); err != nil {
		return
	}

	// bw.Bytes == PES Packet
	if _, err = w.Write(tsPkts); err != nil {
		return
	}

	return
}
