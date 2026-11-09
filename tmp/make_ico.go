package main

import (
	"bytes"
	"encoding/binary"
	"io/ioutil"
)

func main() {
	pngData, _ := ioutil.ReadFile(`winres/icon2.png`)
	buf := new(bytes.Buffer)
	// Header: 6 bytes
	binary.Write(buf, binary.LittleEndian, uint16(0)) // Reserved 0
	binary.Write(buf, binary.LittleEndian, uint16(1)) // Type 1 (.ico)
	binary.Write(buf, binary.LittleEndian, uint16(1)) // Image count 1

	// Dir entry: 16 bytes
	buf.WriteByte(0)                                             // width 0 (256)
	buf.WriteByte(0)                                             // height 0 (256)
	buf.WriteByte(0)                                             // color count
	buf.WriteByte(0)                                             // reserved
	binary.Write(buf, binary.LittleEndian, uint16(1))            // planes
	binary.Write(buf, binary.LittleEndian, uint16(32))           // bpp
	binary.Write(buf, binary.LittleEndian, uint32(len(pngData))) // size
	binary.Write(buf, binary.LittleEndian, uint32(22))           // offset

	buf.Write(pngData)
	ioutil.WriteFile(`icon2.ico`, buf.Bytes(), 0644)
}
