package main

import (
	"bytes"
	"encoding/binary"
)

// encodeICO packs PNG images into an ICO container. Windows has accepted
// PNG-compressed entries since Vista, so no BMP encoding is needed.
func encodeICO(images map[int][]byte, sizes []int) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(0))          // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1))          // type: icon
	binary.Write(&buf, binary.LittleEndian, uint16(len(sizes))) // image count

	offset := 6 + 16*len(sizes)
	for _, size := range sizes {
		data := images[size]
		dim := byte(size)
		if size >= 256 {
			dim = 0 // 0 means 256 in the ICO header
		}
		buf.WriteByte(dim)                                         // width
		buf.WriteByte(dim)                                         // height
		buf.WriteByte(0)                                           // palette size
		buf.WriteByte(0)                                           // reserved
		binary.Write(&buf, binary.LittleEndian, uint16(1))         // colour planes
		binary.Write(&buf, binary.LittleEndian, uint16(32))        // bits per pixel
		binary.Write(&buf, binary.LittleEndian, uint32(len(data))) // size in bytes
		binary.Write(&buf, binary.LittleEndian, uint32(offset))    // offset
		offset += len(data)
	}
	for _, size := range sizes {
		buf.Write(images[size])
	}
	return buf.Bytes()
}
